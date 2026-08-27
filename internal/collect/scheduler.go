// SPDX-License-Identifier: AGPL-3.0-or-later

// Package collect is the scheduler: the goroutine that decides which watch is
// read next.
//
// It holds no transport. The Passer it is given owns the leaser and the pacer,
// and there is exactly one of each for the whole process — the budget is per
// egress address, and a pacer per watch would multiply the ceiling the panel
// promises by the number of watches. That is not a style point: it is the
// arithmetic that earns a ban.
package collect

import (
	"context"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Passer reads one watch.
//
// An interface so this package needs no network to be tested and so
// cmd/avitomon keeps owning the transport wiring — which is where the
// credentials are, and they must not spread.
type Passer interface {
	Pass(ctx context.Context, w store.Watch) error
}

// defaultBatch bounds one turn.
//
// Ten. The pacer decides the rate, so a bigger batch does not collect faster —
// it only makes one turn longer, and a turn that runs for an hour is a
// scheduler that cannot be stopped promptly and a panel that never sees it
// finish. Ten watches at the direct ceiling is about a minute and a half.
const defaultBatch = 10

// Scheduler takes due watches and reads them, one at a time.
//
// Sequential and not parallel, and the spec says why: on this target parallel
// requests to one host through one port coalesce onto a single challenge solve
// and the joiners simply wait, so throughput scales with ports and never with
// goroutines. The pool inside the leaser is the concurrency.
type Scheduler struct {
	Store  *store.Store
	Passer Passer
	Batch  int
	Now    func() time.Time
	Log    func(format string, args ...any)
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) batch() int {
	if s.Batch > 0 {
		return s.Batch
	}
	return defaultBatch
}

// Turn reads whatever is due and returns.
//
// One turn is not one pass: it is however many watches were due, up to the
// batch. Returning between turns is what lets the supervisor stop the loop and
// what makes a backoff mean anything.
func (s *Scheduler) Turn(ctx context.Context) error {
	due, err := s.Store.DueWatches(ctx, s.now().Unix(), s.batch())
	if err != nil {
		return fmt.Errorf("collect: due watches: %w", err)
	}
	// Nothing due is the ordinary answer and not an error. Treated as one it
	// would drive the supervisor's backoff to five minutes on a quiet
	// installation and keep it there.
	if len(due) == 0 {
		return nil
	}

	var failures int
	for _, w := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.Passer.Pass(ctx, w); err != nil {
			// One watch failing does not stop the others. A watch can halt on
			// a wall of its own, and collection that stopped because one watch
			// hit one is collection that never recovers — and the remaining
			// watches may be on a different address entirely.
			failures++
			s.logf("collect: наблюдение %d (%s): %v", w.ID, w.Name, err)
		}
	}
	// The turn itself is reported as failed only when nothing worked, so that
	// the backoff means «this installation is not collecting» rather than «one
	// of ten watches is blocked».
	if failures == len(due) {
		return fmt.Errorf("collect: ни одно из %d наблюдений не прошло", len(due))
	}
	return nil
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
