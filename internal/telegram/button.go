// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The inline button, and the payload that travels with a message to make one.
//
// The spec asks for «Это не перепубликация» on the message itself, so a person
// can correct the product from where they read it rather than having to find
// the panel. The panel's button does the same three acts and is not going
// away — this is the same correction reachable one screen earlier.

// Attachment is what a message carries besides its text.
//
// JSON in one opaque column, because internal/notify and internal/store both
// refuse to look inside it — the outbox is a queue and a queue that understood
// its payloads would have to be changed every time one of them did.
type Attachment struct {
	ButtonText string `json:"button_text,omitempty"`
	Callback   string `json:"callback,omitempty"`
}

// Encode renders an attachment for the outbox, or "" when there is nothing to
// attach. Empty rather than "{}" so a reader can tell «no button» from «a
// button whose fields did not survive».
func (a Attachment) Encode() string {
	if a.ButtonText == "" || a.Callback == "" {
		return ""
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return string(raw)
}

// DecodeAttachment reads one back. A payload this build does not understand
// yields nothing and the message goes out as plain text, which is the right
// failure: a message with no button still says what happened.
func DecodeAttachment(s string) (Attachment, bool) {
	if strings.TrimSpace(s) == "" {
		return Attachment{}, false
	}
	var a Attachment
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return Attachment{}, false
	}
	if a.ButtonText == "" || a.Callback == "" {
		return Attachment{}, false
	}
	return a, true
}

// callbackPrefix names the one action this build understands.
//
// Prefixed rather than bare so that a second action can be added without every
// old button in every old message suddenly meaning it. Telegram keeps callback
// data for as long as the message exists, which on a chat somebody scrolls back
// through is for ever.
const callbackPrefix = "norepost"

// NotARepost is the callback data for «Это не перепубликация» on one pair.
func NotARepost(successor, predecessor int64) string {
	return fmt.Sprintf("%s:%d:%d", callbackPrefix, successor, predecessor)
}

// ParseNotARepost reads that back.
//
// Anything else — a newer build's action, a truncated payload, somebody's
// experiment — yields false rather than an error. A callback this build cannot
// act on is answered politely and ignored; treating it as a failure would
// retry it, and Telegram would keep resending it.
func ParseNotARepost(data string) (successor, predecessor int64, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != callbackPrefix {
		return 0, 0, false
	}
	successor, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || successor <= 0 {
		return 0, 0, false
	}
	predecessor, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil || predecessor <= 0 {
		return 0, 0, false
	}
	return successor, predecessor, true
}

// replyMarkup is Telegram's inline keyboard, one row and one button.
type replyMarkup struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

func markupFor(a Attachment) (string, error) {
	m := replyMarkup{InlineKeyboard: [][]inlineButton{{{
		Text: a.ButtonText, CallbackData: a.Callback,
	}}}}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
