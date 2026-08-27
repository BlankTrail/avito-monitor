// SPDX-License-Identifier: AGPL-3.0-or-later

package supervise

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects what a supervised loop did, without any real waiting.
type recorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
	panics []string
	logs   []string
}

func (r *recorder) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.sleeps = append(r.sleeps, d)
	r.mu.Unlock()
	// Two hundred turns is enough for any assertion here, and stopping there
	// keeps a broken loop from running the test binary out of memory instead of
	// failing it.
	if len(r.sleeps) > 200 {
		return context.Canceled
	}
	return ctx.Err()
}

func (r *recorder) onPanic(name, stack string) {
	r.mu.Lock()
	r.panics = append(r.panics, name+": "+stack)
	r.mu.Unlock()
}

func (r *recorder) log(format string, args ...any) {
	r.mu.Lock()
	r.logs = append(r.logs, format)
	r.mu.Unlock()
}

func (r *recorder) config(name string, run func(context.Context) error) Config {
	return Config{
		Name: name, Run: run, Every: time.Second,
		OnPanic: r.onPanic, Log: r.log, Sleep: r.sleep,
	}
}

// TestAPanicDoesNotTakeTheProcessWithIt, and the loop comes back.
//
// WB_GO has no panic recovery anywhere in its non-test code, and on a tray
// build that means the icon vanishes with an explanation in a file nobody has
// open. This is the one place this design deliberately does not copy it.
func TestAPanicDoesNotTakeTheProcessWithIt(t *testing.T) {
	r := &recorder{}
	turns := 0
	ctx, cancel := context.WithCancel(context.Background())

	cfg := r.config("сборщик", func(context.Context) error {
		turns++
		if turns == 1 {
			panic("что-то пошло не так")
		}
		if turns >= 3 {
			cancel()
		}
		return nil
	})
	Run(ctx, cfg)

	if turns < 3 {
		t.Errorf("the loop ran %d turns; a panic stopped it", turns)
	}
	if len(r.panics) != 1 {
		t.Fatalf("%d panics recorded, want 1: %v", len(r.panics), r.panics)
	}
}

// TestAPanicIsRecordedWithItsNameAndTheFirstFrames.
//
// The name, because a row saying «something panicked» does not say which of the
// five. The first frames and not the whole stack: a hundred frames in a changes
// row is a row nobody reads.
func TestAPanicIsRecordedWithItsNameAndTheFirstFrames(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0

	Run(ctx, r.config("почтальон", func(context.Context) error {
		turns++
		if turns == 1 {
			panic("боль")
		}
		cancel()
		return nil
	}))

	if len(r.panics) != 1 {
		t.Fatalf("panics = %v", r.panics)
	}
	got := r.panics[0]
	if !strings.HasPrefix(got, "почтальон: ") {
		t.Errorf("the record does not name the goroutine: %q", got)
	}
	if !strings.Contains(got, "боль") {
		t.Errorf("the record does not carry what was panicked with: %q", got)
	}
	if !strings.Contains(got, "supervise") {
		t.Errorf("the record carries no stack at all: %q", got)
	}
	if n := strings.Count(got, "\n"); n > maxStackLines+2 {
		t.Errorf("the record carries %d lines of stack; it is a row, not a dump", n)
	}
}

// TestTheBackoffDoublesAndStopsAtItsCeiling.
//
// Asserted on the recorded sleeps rather than by waiting, so five minutes of
// backoff costs nothing and is still measurable.
func TestTheBackoffDoublesAndStopsAtItsCeiling(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0

	Run(ctx, r.config("сборщик", func(context.Context) error {
		turns++
		if turns > 12 {
			cancel()
			return nil
		}
		return errors.New("не вышло")
	}))

	if len(r.sleeps) < 5 {
		t.Fatalf("sleeps = %v", r.sleeps)
	}
	if r.sleeps[0] != backoffFloor {
		t.Errorf("first backoff = %v, want %v", r.sleeps[0], backoffFloor)
	}
	if r.sleeps[1] != 2*backoffFloor {
		t.Errorf("second backoff = %v, want %v", r.sleeps[1], 2*backoffFloor)
	}
	for i, d := range r.sleeps {
		if d > backoffCeiling {
			t.Fatalf("sleep %d is %v, past the %v ceiling", i, d, backoffCeiling)
		}
	}
	// And it does reach the ceiling rather than stopping short of it.
	reached := false
	for _, d := range r.sleeps {
		if d == backoffCeiling {
			reached = true
		}
	}
	if !reached {
		t.Errorf("the ceiling was never reached: %v", r.sleeps)
	}
}

// TestACleanTurnResetsTheBackoff.
//
// Without it one bad afternoon leaves the scheduler on a five-minute tick for
// ever after, and nothing on any screen would say why collection had slowed
// down.
func TestACleanTurnResetsTheBackoff(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0

	Run(ctx, r.config("сборщик", func(context.Context) error {
		turns++
		switch {
		case turns <= 3:
			return errors.New("не вышло")
		case turns == 4:
			return nil // recovered
		default:
			cancel()
			return nil
		}
	}))

	if len(r.sleeps) < 5 {
		t.Fatalf("sleeps = %v", r.sleeps)
	}
	// Three failures, then a clean turn: the fourth wait is the ordinary
	// interval again and not the backoff it had climbed to.
	if got := r.sleeps[3]; got != time.Second {
		t.Errorf("after a clean turn the wait was %v, want the interval %v",
			got, time.Second)
	}
}

// TestACancelledContextEndsTheLoopAndDoesNotRestartIt.
func TestACancelledContextEndsTheLoopAndDoesNotRestartIt(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	turns := 0
	Run(ctx, r.config("сборщик", func(context.Context) error {
		turns++
		return nil
	}))
	if turns > 1 {
		t.Errorf("a cancelled context ran %d turns", turns)
	}
}

// TestALoopThatReturnsImmediatelyStillWaits.
//
// A Run that does nothing and returns nil is not an error, but spinning on it
// would take a core to itself. The interval applies to success as much as to
// failure.
func TestALoopThatReturnsImmediatelyStillWaits(t *testing.T) {
	r := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0

	Run(ctx, r.config("сборщик", func(context.Context) error {
		turns++
		if turns >= 3 {
			cancel()
		}
		return nil
	}))
	if len(r.sleeps) < 2 {
		t.Fatalf("a loop that always succeeds never waited: %v", r.sleeps)
	}
	for _, d := range r.sleeps {
		if d != time.Second {
			t.Errorf("a clean loop waited %v, want the interval", d)
		}
	}
}

// TestNoRecorderIsNotACrash.
//
// The panic hook is optional: a caller with no store to write to still gets the
// recovery and the log. Requiring it would mean the one place that must never
// panic panics on a nil field.
func TestNoRecorderIsNotACrash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	turns := 0
	Run(ctx, Config{
		Name: "без записи", Every: time.Millisecond,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		Run: func(context.Context) error {
			turns++
			if turns == 1 {
				panic("боль")
			}
			cancel()
			return nil
		},
	})
	if turns < 2 {
		t.Errorf("the loop did not come back without a panic hook: %d turns", turns)
	}
}
