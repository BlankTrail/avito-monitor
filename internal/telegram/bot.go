// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/BlankTrail/avito-monitor/internal/notify"
)

// Lifted from WB_GO's internal/telegram/bot.go, with SendDocument, SendPhoto
// and splitThread left behind — the first two belong to increment 6's export
// and chart, and nothing in this product's spec asks for a forum topic. redact,
// apiError and truncate are kept exactly, and each has a test.

// Bot talks to the Telegram Bot API.
//
// Written against net/http and encoding/json rather than a library: three calls
// are needed — say who I am, send a message — and a dependency tree to reach
// them is a dependency tree in a public AGPL repository.
type Bot struct {
	// token is in every URL this type builds, which is why nothing here ever
	// returns an unredacted error: a log line carrying a bot token hands over
	// the bot. Unexported so it cannot be printed by a caller's %+v either.
	token string

	// do is how a request reaches Telegram. A function rather than an
	// *http.Client so the ladder can hand this a client that goes out through
	// BlankTrail, and so a test can answer without a network.
	do func(*http.Request) (*http.Response, error)

	// api is the base without a trailing slash. Empty means the real one.
	// Present so a deployment behind an internal mirror can be pointed at it.
	api string
}

// NewBot builds a rung. A nil do means the default HTTP client.
func NewBot(token string, do func(*http.Request) (*http.Response, error)) *Bot {
	if do == nil {
		do = http.DefaultClient.Do
	}
	return &Bot{token: token, do: do}
}

// At points this bot at another base URL.
func (b *Bot) At(api string) *Bot { b.api = api; return b }

// maxMessage is Telegram's own limit on one message, in characters.
//
// Exceeded, the API refuses the whole message rather than truncating it — so a
// long notification would not arrive at all, and the person would be told
// nothing rather than told most of it.
const maxMessage = 4096

// ErrNoToken is returned when the bot has not been configured yet.
var ErrNoToken = errors.New("telegram: не задан токен бота")

func (b *Bot) base() string {
	if b.api != "" {
		return strings.TrimSuffix(b.api, "/")
	}
	return "https://api.telegram.org"
}

// Name is the rung's name on the settings screen.
func (b *Bot) Name() string { return "bot api" }

// Check is getMe: the cheapest call that proves both that the route reaches
// Telegram and that Telegram accepts this token. A route that reaches Telegram
// with a token it rejects is not a route that can deliver anything.
func (b *Bot) Check(ctx context.Context) error {
	_, err := b.GetMe(ctx)
	return err
}

// GetMe asks Telegram who this bot is.
func (b *Bot) GetMe(ctx context.Context) (username string, err error) {
	var out struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", nil, &out); err != nil {
		return "", err
	}
	return out.Username, nil
}

// SendMessage delivers text to one chat.
//
// chat is Telegram's own idea of a recipient and is passed through untouched: a
// numeric id or an @name.
func (b *Bot) SendMessage(ctx context.Context, chat, text string) error {
	form := url.Values{}
	form.Set("chat_id", chat)
	form.Set("text", truncate(text))
	// Plain text, deliberately. Markdown and HTML both make Telegram refuse a
	// message whose body happens to contain an unbalanced bracket or a stray
	// underscore — and titles on this site contain both. A notification that
	// fails to arrive because of punctuation in a title is the worst possible
	// trade for bold text.
	return b.call(ctx, "sendMessage", strings.NewReader(form.Encode()), nil)
}

// Send makes a Bot into a notify.Transport.
//
// The adapter is here rather than in internal/notify because this is the side
// that knows what a Telegram address looks like; notify deliberately never
// looks inside one.
func (b *Bot) Send(ctx context.Context, m notify.Message) error {
	return b.SendMessage(ctx, m.Address, m.Body)
}

func (b *Bot) call(ctx context.Context, method string, body io.Reader, out any) error {
	if b.token == "" {
		return ErrNoToken
	}
	if body == nil {
		body = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.base()+"/bot"+b.token+"/"+method, body)
	if err != nil {
		return b.redact(fmt.Errorf("telegram: %s: %w", method, err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := b.do(req)
	if err != nil {
		// Redacted, not merely un-mentioned. net/http wraps a transport failure
		// in a *url.Error carrying the whole URL, and the URL is where the
		// token lives — so "this function does not print the URL" is not
		// enough, and a test caught exactly that.
		return b.redact(fmt.Errorf("telegram: %s: %w", method, err))
	}
	defer func() { _ = res.Body.Close() }()

	// Bounded: a route that is actually a captive portal answers every request
	// with a login page, and reading an unbounded one into memory is how a
	// misconfigured network becomes an out-of-memory kill.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return b.redact(fmt.Errorf("telegram: %s: чтение ответа: %w", method, err))
	}

	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return b.redact(fmt.Errorf(
			"telegram: %s: ответ не от Bot API (%d): %w", method, res.StatusCode, err))
	}
	if !envelope.OK {
		return b.redact(apiError(method, envelope.ErrorCode, envelope.Description))
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return b.redact(fmt.Errorf("telegram: %s: %w", method, err))
		}
	}
	return nil
}

// redact hides the bot token in an error's text while keeping the error itself
// intact, so errors.Is still finds notify.ErrPermanent underneath.
//
// An error goes to a log, to a screen, and sometimes into an issue report. A
// bot token in one hands over the bot.
func (b *Bot) redact(err error) error {
	if err == nil || b.token == "" {
		return err
	}
	return redacted{err: err, token: b.token}
}

type redacted struct {
	err   error
	token string
}

func (r redacted) Error() string { return strings.ReplaceAll(r.err.Error(), r.token, "<токен>") }
func (r redacted) Unwrap() error { return r.err }

// apiError turns Telegram's own refusal into one the queue can act on.
//
// The distinction matters more than the wording: a 4xx from the Bot API means
// this message will be refused the same way for ever — a chat that no longer
// exists, a bot removed from a group — and retrying it for a day and a half
// delays every message behind it. Anything else is treated as temporary, which
// is the safe direction to be wrong in: a message retried unnecessarily arrives
// late, a message abandoned wrongly never arrives.
//
// 429 is deliberately not in the list. It is the one 4xx that means "later",
// and giving up on it would abandon a message for being too popular.
func apiError(method string, code int, description string) error {
	err := fmt.Errorf("telegram: %s: %s (%d)", method, description, code)
	switch code {
	case http.StatusBadRequest, http.StatusUnauthorized,
		http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("%w: %w", err, notify.ErrPermanent)
	}
	return err
}

// truncate keeps a long message deliverable.
//
// Telegram refuses one over the limit outright rather than shortening it, so
// the choice is between most of the notification and none of it. The marker is
// there because without it a truncated notification listing forty listings
// looks exactly like one listing five.
func truncate(text string) string {
	if utf8.RuneCountInString(text) <= maxMessage {
		return text
	}
	const marker = "\n… сообщение обрезано"
	keep := maxMessage - utf8.RuneCountInString(marker)

	var b strings.Builder
	for i, r := range []rune(text) {
		if i >= keep {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + marker
}
