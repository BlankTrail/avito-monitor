// SPDX-License-Identifier: AGPL-3.0-or-later

// Package telegram delivers a message, by whichever route still works.
package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Sender is one way to deliver a message.
//
// An interface from the first commit rather than from the commit that needs
// two, and the reason is structural rather than stylistic: MTProto is not HTTP.
// It cannot be handed an *http.Request, so it could never have been a rung of
// an HTTP transport ladder however the code was arranged. WB_GO built that
// ladder one level too low and had to move it; this starts where it ended up.
//
// Three verbs, which is what M1 needs. SendDocument and SendPhoto belong to
// increment 6's export and chart, and adding them now would be an interface
// with two implementations neither of which anything calls.
type Sender interface {
	// Name is what the settings screen shows. «Каким путём идёт мой телеграм»
	// is a question the user asks, and an unnamed rung can only be described as
	// "the one that worked".
	Name() string

	// Check reports whether this rung can deliver right now. For the Bot API it
	// is getMe; for MTProto it is the session coming up. It is the only check
	// that proves what a message will actually need, which is why the ladder
	// makes it rather than assuming a rung that dialled is a rung that works.
	Check(ctx context.Context) error

	SendMessage(ctx context.Context, chat, text string) error
}

// Ladder tries each rung in order and remembers the one that worked.
//
// It remembers because a live check costs a round trip, and paying it before
// every notification would spend thirty an hour on a question whose answer
// changes about never. It forgets on a send failure, so a rung that stops
// working does not pin the product to it.
type Ladder struct {
	Rungs []Sender

	mu      sync.Mutex
	working Sender
}

// Current is the rung in use, for the settings screen. Empty before anything
// has been sent, which is honest: nothing has proved itself yet.
func (l *Ladder) Current() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.working == nil {
		return ""
	}
	return l.working.Name()
}

// SendMessage delivers by the first rung that can.
//
// A send failure on the remembered rung falls through to a full search rather
// than being returned. The remembered rung is a cached answer to "who works",
// and a failure is exactly the evidence that the cache is stale.
func (l *Ladder) SendMessage(ctx context.Context, chat, text string) error {
	if s := l.remembered(); s != nil {
		if err := s.SendMessage(ctx, chat, text); err == nil {
			return nil
		}
		l.forget()
	}
	return l.search(ctx, chat, text)
}

func (l *Ladder) remembered() Sender {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.working
}

func (l *Ladder) forget() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.working = nil
}

// search checks each rung and sends by the first that passes.
//
// The error names every rung and what each one said. "Не удалось отправить" is
// not something a person can act on; "bot api: api.telegram.org недоступен;
// mtproto: не настроен api id" tells them which of the two to fix.
func (l *Ladder) search(ctx context.Context, chat, text string) error {
	if len(l.Rungs) == 0 {
		return fmt.Errorf("telegram: не настроен ни один способ отправки")
	}
	var refusals []string
	for _, s := range l.Rungs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Check(ctx); err != nil {
			refusals = append(refusals, fmt.Sprintf("%s: %v", s.Name(), err))
			continue
		}
		if err := s.SendMessage(ctx, chat, text); err != nil {
			refusals = append(refusals, fmt.Sprintf("%s: %v", s.Name(), err))
			continue
		}
		l.mu.Lock()
		l.working = s
		l.mu.Unlock()
		return nil
	}
	return fmt.Errorf("telegram: ни один способ не сработал: %s",
		strings.Join(refusals, "; "))
}
