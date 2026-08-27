// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// The second inbound path into this program, and the authentication question it
// raises, answered.
//
// Long polling and not a webhook. A webhook means a public HTTPS endpoint, a
// certificate and a port open to the internet — on a laptop behind a router
// that is a tunnelling service and a second trust decision, for a product whose
// whole posture is «127.0.0.1 by default, no password, refusing the LAN until
// the user sets one of their own». getUpdates reaches out instead of being
// reached, which needs nothing open.
//
// The authentication question is answered by the chat and not by a secret.
// Telegram states which chat a callback came from, and this build acts only on
// chats that are configured recipients. A stranger who finds a bot's username
// and presses something can therefore make it answer politely and nothing else
// — and the callback data itself is never trusted to name what to do without
// the chat check first.

// Update is the one shape this build reads out of getUpdates.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Callback *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// GetUpdates asks for everything after offset, waiting up to timeout seconds.
//
// allowed_updates is narrowed to callback_query on purpose: this program has no
// use for the messages people send its bot, and asking for them would mean
// carrying somebody's private chat through a queue that logs errors.
func (b *Bot) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	form := url.Values{}
	form.Set("offset", strconv.FormatInt(offset, 10))
	form.Set("timeout", strconv.Itoa(timeoutSec))
	form.Set("allowed_updates", `["callback_query"]`)

	var out []Update
	if err := b.call(ctx, "getUpdates",
		strings.NewReader(form.Encode()), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AnswerCallback closes the spinner on the button somebody pressed.
//
// Always, even for a callback this build ignores. Telegram shows a loading
// state until it is answered and re-delivers an unacknowledged update, so an
// unanswered press is a button that spins for ever and an update that comes
// back on every poll.
func (b *Bot) AnswerCallback(ctx context.Context, id, text string) error {
	form := url.Values{}
	form.Set("callback_query_id", id)
	if text != "" {
		form.Set("text", text)
	}
	return b.call(ctx, "answerCallbackQuery", strings.NewReader(form.Encode()), nil)
}

// EditMessageText is how a pressed button stops being pressable.
//
// Optional in the sense that nothing breaks without it, and not optional in the
// sense that matters: a button that stays after it has been pressed invites a
// second press, and the second press acts on a pair that is already pinned.
func (b *Bot) EditMessageText(ctx context.Context, chat int64, messageID int64, text string) error {
	form := url.Values{}
	form.Set("chat_id", strconv.FormatInt(chat, 10))
	form.Set("message_id", strconv.FormatInt(messageID, 10))
	form.Set("text", truncate(text))
	return b.call(ctx, "editMessageText", strings.NewReader(form.Encode()), nil)
}

// CallbackHandler acts on one press.
//
// The chat has already been checked against the configured recipients before
// this is called. It returns the text to show in the little toast Telegram
// pops over the button.
type CallbackHandler func(ctx context.Context, successor, predecessor int64) (string, error)

// Poller reads callbacks and dispatches them.
type Poller struct {
	Bot *Bot
	// Allowed is the set of chat ids that may act. Configured recipients, and
	// nothing else — this is the whole of the authentication.
	Allowed func(ctx context.Context) (map[int64]bool, error)
	// Offset is where to resume, and Save persists it. Without persistence a
	// restart re-delivers every unacknowledged update, and a press somebody
	// made an hour ago would be acted on again.
	Offset  func(ctx context.Context) (int64, error)
	Save    func(ctx context.Context, offset int64) error
	Handle  CallbackHandler
	Timeout int // seconds to long-poll; zero means 25
	Log     func(format string, args ...any)
}

// Turn polls once and acts on whatever came back.
func (p *Poller) Turn(ctx context.Context) error {
	offset, err := p.Offset(ctx)
	if err != nil {
		return err
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 25
	}
	updates, err := p.Bot.GetUpdates(ctx, offset, timeout)
	if err != nil {
		return err
	}
	allowed, err := p.Allowed(ctx)
	if err != nil {
		return err
	}

	highest := offset
	for _, u := range updates {
		if u.UpdateID >= highest {
			highest = u.UpdateID + 1
		}
		if u.Callback == nil {
			continue
		}
		p.act(ctx, u, allowed)
	}
	if highest != offset {
		// Saved after acting, not before. A crash between the two re-delivers
		// the press, and acting twice on «это не перепубликация» is harmless —
		// the pair is already pinned and the second attempt changes nothing.
		// Saving first would lose a press instead, which is not harmless.
		return p.Save(ctx, highest)
	}
	return nil
}

func (p *Poller) act(ctx context.Context, u Update, allowed map[int64]bool) {
	c := u.Callback
	chat := int64(0)
	if c.Message != nil {
		chat = c.Message.Chat.ID
	}

	// The chat check first, before the data is even parsed. A payload names
	// what to do; only the chat says who is allowed to ask.
	if chat == 0 || !allowed[chat] {
		_ = p.Bot.AnswerCallback(ctx, c.ID, "Этот чат не настроен как получатель.")
		p.logf("telegram: нажатие из ненастроенного чата %d", chat)
		return
	}
	successor, predecessor, ok := ParseNotARepost(c.Data)
	if !ok {
		// Answered politely and ignored. An unanswered update comes back on
		// every poll for ever, so a payload this build does not understand
		// must still be acknowledged.
		_ = p.Bot.AnswerCallback(ctx, c.ID, "Эта кнопка из другой версии.")
		return
	}
	if p.Handle == nil {
		_ = p.Bot.AnswerCallback(ctx, c.ID, "")
		return
	}
	text, err := p.Handle(ctx, successor, predecessor)
	if err != nil {
		p.logf("telegram: %v", err)
		_ = p.Bot.AnswerCallback(ctx, c.ID, "Не получилось: "+err.Error())
		return
	}
	_ = p.Bot.AnswerCallback(ctx, c.ID, text)
}

func (p *Poller) logf(format string, args ...any) {
	if p.Log != nil {
		p.Log(format, args...)
	}
}
