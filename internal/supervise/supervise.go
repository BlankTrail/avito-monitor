// SPDX-License-Identifier: AGPL-3.0-or-later

// Package supervise runs a loop that must not die.
//
// The spec names five: the scheduler, the outbox worker, the out-of-band series
// producer, the confirmation sweeper and the retention sweeper. Naming them
// there is the point — a list that lives only in the reader's head grows a
// sixth member nobody wraps — and this package is the one helper all five go
// through.
//
// What it does is exactly what the spec states and nothing more: recover a
// panic, write one row, log one structured line, and restart the loop after
// five seconds doubling to a five-minute ceiling.
//
// The donor has no panic recovery anywhere in its non-test code. On a tray
// build that means the icon vanishes with an explanation in a file nobody has
// open, and this is the one place this design deliberately does not copy it.
package supervise

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

const (
	// backoffFloor is the first wait after a turn that failed. Five seconds:
	// long enough that a loop failing on a locked database does not spin, short
	// enough that a transient failure costs one tick rather than a schedule.
	backoffFloor = 5 * time.Second
	// backoffCeiling bounds it. Five minutes, because a monitor that has backed
	// off past that is a monitor whose owner should be told rather than waited
	// on — and every backoff is visible on Здоровье сбора as a gap.
	backoffCeiling = 5 * time.Minute
	// maxStackLines is how much of a panic's stack becomes a row.
	//
	// Twelve frames. The whole stack is a hundred lines and a hundred lines in
	// a changes row is a row nobody reads; twelve reaches through the wrapper
	// into whatever actually panicked, which is the only part that names the
	// bug.
	maxStackLines = 12
)

// Config is one supervised loop.
type Config struct {
	// Name is what this goroutine is called, and it is written into the row a
	// panic produces. «Something panicked» does not say which of the five.
	Name string

	// Run is one turn. It returns when the turn is done, not when the process
	// is: the loop is here, so a Run that loops internally can never be
	// restarted or stopped.
	Run func(context.Context) error

	// Every is the wait between clean turns.
	Every time.Duration

	// OnPanic records a panic where a person will find it. Optional: a caller
	// with nowhere to write still gets the recovery and the log, because the
	// one place that must never panic must not panic on a nil field.
	OnPanic func(name, stack string)

	Log   func(format string, args ...any)
	Sleep func(context.Context, time.Duration) error
}

// Go starts a supervised loop and returns immediately.
func Go(ctx context.Context, cfg Config) {
	go Run(ctx, cfg)
}

// Run is the loop itself, exported so a test can drive it without a goroutine.
//
// It returns only when the context is done. A caller that wants it to stop
// cancels the context; there is no other stop, deliberately — a second way to
// end a supervised loop is a second way for one to end silently.
func Run(ctx context.Context, cfg Config) {
	every := cfg.Every
	if every <= 0 {
		every = time.Minute
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}

	backoff := time.Duration(0)
	for {
		if ctx.Err() != nil {
			return
		}
		err := turn(ctx, cfg)

		wait := every
		switch {
		case err != nil:
			// Failed, so back off — doubling from the floor, capped at the
			// ceiling.
			if backoff == 0 {
				backoff = backoffFloor
			} else if backoff < backoffCeiling {
				backoff *= 2
			}
			if backoff > backoffCeiling {
				backoff = backoffCeiling
			}
			wait = backoff
			logf(cfg, "supervise: %s: %v; следующая попытка через %s",
				cfg.Name, err, wait)
		default:
			// A clean turn resets it. Without this one bad afternoon leaves
			// the scheduler on a five-minute tick for ever after, and nothing
			// on any screen would say why collection had slowed down.
			backoff = 0
		}

		if err := sleep(ctx, wait); err != nil {
			return
		}
	}
}

// turn runs one turn and converts a panic into an error.
//
// A separate function because the recover has to be in the frame that defers
// it, and putting it in the loop body would mean the loop itself is the frame
// being unwound.
func turn(ctx context.Context, cfg Config) (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := firstFrames(string(debug.Stack()))
		detail := trimNewlines(sprint(r)) + "\n" + stack
		if cfg.OnPanic != nil {
			cfg.OnPanic(cfg.Name, detail)
		}
		logf(cfg, "supervise: %s упал: %s", cfg.Name, detail)
		err = panicError{name: cfg.Name, value: r}
	}()
	if cfg.Run == nil {
		return nil
	}
	return cfg.Run(ctx)
}

// panicError is what a recovered panic looks like to the backoff.
//
// Its own type rather than errors.New so that a caller which cares can tell a
// crash from a turn that merely failed — the two mean different things about
// whether the next turn is likely to work.
type panicError struct {
	name  string
	value any
}

func (e panicError) Error() string {
	return e.name + " упал: " + sprint(e.value)
}

func firstFrames(stack string) string {
	lines := strings.Split(stack, "\n")
	if len(lines) > maxStackLines {
		lines = lines[:maxStackLines]
	}
	return strings.Join(lines, "\n")
}

func trimNewlines(s string) string {
	return strings.TrimRight(s, "\n")
}

func logf(cfg Config, format string, args ...any) {
	if cfg.Log != nil {
		cfg.Log(format, args...)
	}
}

// sprint renders whatever was panicked with.
func sprint(v any) string { return fmt.Sprint(v) }
