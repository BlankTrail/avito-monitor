// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"

	"github.com/BlankTrail/avito-monitor/internal/notify"
)

// Lifted in shape from WB_GO's internal/telegram/mtproto.go, which ships with
// its live exchange never verified against real Telegram. See
// docs/measurements/ for what this one's exchange actually did.
//
// gotd/td is by a wide margin the largest line in the dependency budget, and it
// is carried knowingly: api.telegram.org is blocked for part of the Russian
// audience, which is this product's audience, and a user in direct mode behind
// that block otherwise has no path at all.

// SessionStore is where the MTProto session lives.
//
// The signature is gotd's session.Storage exactly, so the store's
// implementation satisfies both without an adapter. It is spelled out here so
// that nothing outside this file has to import gotd to provide one.
type SessionStore interface {
	LoadSession(ctx context.Context) ([]byte, error)
	StoreSession(ctx context.Context, data []byte) error
}

// MTConfig is everything this rung needs.
//
// Three credentials rather than the Bot API's one. APIID and APIHash come from
// my.telegram.org and identify the application; BotToken identifies the bot.
// Telegram's bot login needs both, which is why Check refuses on any one of
// them being absent rather than discovering it at send time.
type MTConfig struct {
	APIID    int
	APIHash  string
	BotToken string
	Session  SessionStore

	// Timeout bounds one exchange. Zero means the default. A rung that hangs
	// is worse than one that fails: the ladder is waiting on it and nothing
	// below it is being tried.
	Timeout time.Duration
}

const defaultMTTimeout = 30 * time.Second

// MTProto delivers over Telegram's own protocol.
type MTProto struct{ cfg MTConfig }

// NewMTProto builds a rung.
func NewMTProto(cfg MTConfig) *MTProto { return &MTProto{cfg: cfg} }

// Name is the rung's name on the settings screen.
func (m *MTProto) Name() string { return "mtproto" }

// ErrNotConfigured is returned when a credential is missing.
var ErrNotConfigured = errors.New("telegram: mtproto не настроен")

// credentials reports every missing field at once.
//
// At once, and before dialling anything. A half-configured rung that fails at
// send time is worse than one that refuses at check time, because the ladder
// would already have committed to it — and the message goes on the settings
// screen, where "не настроен" tells somebody nothing about which box to fill.
func (m *MTProto) credentials() error {
	var missing []string
	if m.cfg.APIID == 0 {
		missing = append(missing, "api id (my.telegram.org)")
	}
	if m.cfg.APIHash == "" {
		missing = append(missing, "api hash (my.telegram.org)")
	}
	if m.cfg.BotToken == "" {
		missing = append(missing, "bot token")
	}
	if m.cfg.Session == nil {
		missing = append(missing, "хранилище сессии")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: не хватает: %s", ErrNotConfigured, strings.Join(missing, ", "))
}

// Check proves the session comes up and Telegram accepts this bot.
//
// It authorises rather than merely dialling, because a connection Telegram
// accepts with a token it rejects is not a connection that can deliver
// anything — the same reasoning as the Bot API rung's getMe.
func (m *MTProto) Check(ctx context.Context) error {
	return m.run(ctx, func(context.Context, *telegram.Client) error { return nil })
}

// SendMessage delivers text to one chat.
func (m *MTProto) SendMessage(ctx context.Context, chat, text string) error {
	return m.run(ctx, func(ctx context.Context, c *telegram.Client) error {
		sender := message.NewSender(tg.NewClient(c))
		var b *message.RequestBuilder
		if strings.HasPrefix(chat, "@") {
			b = sender.Resolve(chat)
		} else {
			peer, err := inputPeer(chat)
			if err != nil {
				return err
			}
			b = sender.To(peer)
		}
		if _, err := b.Text(ctx, truncate(text)); err != nil {
			return fmt.Errorf("telegram: mtproto: отправка: %w", err)
		}
		return nil
	})
}

// Send makes an MTProto into a notify.Transport.
func (m *MTProto) Send(ctx context.Context, msg notify.Message) error {
	return m.SendMessage(ctx, msg.Address, msg.Body)
}

// run opens a session, authorises the bot if needed, and calls f.
//
// The session is loaded from and saved to the store by gotd itself — the
// SessionStorage option is the whole of it, and it is what keeps the blob out
// of a file beside the binary. NoUpdates because this is a one-way transport:
// asking Telegram for the update stream would keep a connection open and
// deliver messages nobody in this program reads.
func (m *MTProto) run(ctx context.Context, f func(context.Context, *telegram.Client) error) error {
	if err := m.credentials(); err != nil {
		return err
	}
	timeout := m.cfg.Timeout
	if timeout <= 0 {
		timeout = defaultMTTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := telegram.NewClient(m.cfg.APIID, m.cfg.APIHash, telegram.Options{
		SessionStorage: m.cfg.Session,
		NoUpdates:      true,
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("telegram: mtproto: состояние сессии: %w", err)
		}
		if !status.Authorized {
			if _, err := client.Auth().Bot(ctx, m.cfg.BotToken); err != nil {
				return fmt.Errorf("telegram: mtproto: вход бота: %w", err)
			}
		}
		return f(ctx, client)
	})
	return m.redact(err)
}

// redact keeps the bot token and the api hash out of the error's text.
//
// gotd quotes request parameters in some of its errors, and the bot token is a
// request parameter of the login call. An error goes to a log, to a screen, and
// sometimes into an issue report.
func (m *MTProto) redact(err error) error {
	if err == nil {
		return nil
	}
	out := err
	if m.cfg.BotToken != "" {
		out = redacted{err: out, token: m.cfg.BotToken}
	}
	if m.cfg.APIHash != "" {
		out = redacted{err: out, token: m.cfg.APIHash}
	}
	return out
}

// inputPeer turns a numeric chat id into the peer gotd wants.
//
// Telegram's own id conventions, which the address column stores verbatim
// because that is what a person copies out of a bot: a positive id is a user, a
// -100 prefix is a supergroup or channel, and any other negative id is a basic
// group. An access hash of zero works for a bot talking to a peer that has
// already talked to it, which is every peer a bot can message at all — a bot
// cannot open a conversation.
func inputPeer(chat string) (tg.InputPeerClass, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(chat), 10, 64)
	if err != nil {
		return nil, fmt.Errorf(
			"telegram: mtproto: адрес %q — не число и не @имя", chat)
	}
	switch {
	case id > 0:
		return &tg.InputPeerUser{UserID: id}, nil
	case strings.HasPrefix(strconv.FormatInt(id, 10), "-100"):
		channel, err := strconv.ParseInt(strconv.FormatInt(id, 10)[4:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("telegram: mtproto: адрес %q: %w", chat, err)
		}
		return &tg.InputPeerChannel{ChannelID: channel}, nil
	default:
		return &tg.InputPeerChat{ChatID: -id}, nil
	}
}
