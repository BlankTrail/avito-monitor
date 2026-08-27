// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Message is one thing to deliver.
//
// Address rather than a target row, because a transport needs somewhere to send
// and nothing else. Kind is which transport this target wants; a worker with one
// transport ignores it, and the ladder in internal/telegram is what reads it.
type Message struct {
	TargetID int64
	Address  string
	Kind     string
	Body     string
	// Attachment is opaque to this package, which is the point: notify never
	// looks inside an address and it must not start looking inside a payload
	// either. Whatever put the message in the outbox and whatever takes it out
	// agree on the format between themselves.
	Attachment string
}

// Transport is somewhere a message can go.
//
// The error it returns is written into notify_outbox.last_error and shown in
// the panel, so an implementation must not put a credential in it. That is the
// implementation's job rather than this package's: the transport is what holds
// the token, and a redaction here could only guess at what to look for.
type Transport interface {
	Send(context.Context, Message) error
}

// TransportFunc adapts a function.
type TransportFunc func(context.Context, Message) error

func (f TransportFunc) Send(ctx context.Context, m Message) error { return f(ctx, m) }

// Stats is what one drain did.
//
// Failed and GaveUp are separate numbers because they mean different things to
// whoever reads them: Failed is "Telegram was down and we will try again",
// GaveUp is "this message is never going to arrive". A single count would let
// the second hide inside the first.
type Stats struct {
	Sent, Failed, GaveUp int
}

// Worker drains the outbox once per Run.
//
// Once rather than a loop with a ticker, because the caller owns the schedule:
// avitomon run has one, a test has none, and a worker that started its own
// goroutine would be a second scheduler nobody asked for.
type Worker struct {
	Store     *store.Store
	Transport Transport
	Now       func() time.Time

	// Max is how many rows one drain may attempt. Zero means the store's
	// default. It is a bound rather than "everything" so that a queue which
	// built up during a long outage does not turn the first tick after it into
	// a thousand requests.
	Max int
}

// Run sends everything due and returns what happened.
//
// A send that fails does not stop the drain. One unreachable target must not
// hold up the others, and a transport error is a fact about that message rather
// than about the queue.
func (w *Worker) Run(ctx context.Context) (Stats, error) {
	var st Stats
	now := w.now().Unix()

	due, err := w.Store.DueMessages(ctx, now, w.Max)
	if err != nil {
		return st, err
	}
	for _, m := range due {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		sendErr := w.Transport.Send(ctx, Message{
			TargetID: m.TargetID, Address: m.Address, Kind: m.Kind, Body: m.Body,
			Attachment: m.Attachment,
		})
		if sendErr == nil {
			if err := w.Store.MarkSent(ctx, m.ID, now); err != nil {
				return st, err
			}
			st.Sent++
			continue
		}

		attempts := m.Attempts + 1
		reason := sendErr.Error()
		if attempts >= maxAttempts || errors.Is(sendErr, ErrPermanent) {
			if err := w.Store.MarkGivenUp(ctx, m.ID, reason); err != nil {
				return st, err
			}
			st.GaveUp++
			continue
		}
		next := now + int64(Backoff(attempts)/time.Second)
		if err := w.Store.MarkRetry(ctx, m.ID, next, reason); err != nil {
			return st, err
		}
		st.Failed++
	}
	return st, nil
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// String is what the command line prints after a drain.
func (s Stats) String() string {
	return fmt.Sprintf("отправлено %d, повтор %d, отказ %d", s.Sent, s.Failed, s.GaveUp)
}
