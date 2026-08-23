// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// testPacer runs on a clock the test moves, so nothing sleeps and a ten-minute
// window is measured in microseconds.
func testPacer(now *time.Time) *Pacer {
	return NewPacer(PacerConfig{
		Floor:   3500 * time.Millisecond,
		Jitter:  0, // deterministic here; production runs at ±20%
		Ceiling: 60,
		Window:  10 * time.Minute,
		Now:     func() time.Time { return *now },
		SleepUntil: func(_ context.Context, t time.Time) error {
			if t.After(*now) {
				*now = t
			}
			return nil
		},
	})
}

// TestFloorIsPerIdentity. Two identities do not wait for each other: that is
// the whole reason a pool of ports is faster than one port, and getting it
// wrong makes N ports no better than one.
func TestFloorIsPerIdentity(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	start := now

	if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), "b", "addr2"); err != nil {
		t.Fatal(err)
	}
	if !now.Equal(start) {
		t.Errorf("two different identities waited %v for each other; they share "+
			"nothing", now.Sub(start))
	}

	if err := p.Wait(context.Background(), "a", "addr1"); err != nil {
		t.Fatal(err)
	}
	if got := now.Sub(start); got < 3500*time.Millisecond {
		t.Errorf("the same identity waited %v, want at least the 3.5s floor", got)
	}
}

// TestCeilingIsPerAddress. Identities sharing an address share its ceiling —
// which is what stops a larger pool on one IP from looking like a larger
// budget.
func TestCeilingIsPerAddress(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	for i := 0; i < 60; i++ {
		if err := p.Wait(context.Background(), "id"+strconv.Itoa(i), "shared"); err != nil {
			t.Fatal(err)
		}
	}
	before := now
	if err := p.Wait(context.Background(), "fresh", "shared"); err != nil {
		t.Fatal(err)
	}
	if !now.After(before) {
		t.Error("the 61st request on one address did not wait; the ceiling " +
			"belongs to the address, not to the identity")
	}
}

// TestSecondAddressHasItsOwnCeiling. This is the arithmetic the panel quotes
// and the reason one upstream per port matters.
func TestSecondAddressHasItsOwnCeiling(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	for i := 0; i < 60; i++ {
		if err := p.Wait(context.Background(), "id"+strconv.Itoa(i), "addr1"); err != nil {
			t.Fatal(err)
		}
	}
	before := now
	if err := p.Wait(context.Background(), "other", "addr2"); err != nil {
		t.Fatal(err)
	}
	if !now.Equal(before) {
		t.Errorf("a fresh address waited %v for another address's window",
			now.Sub(before))
	}
}

// TestBudgetPublishesTheArithmetic. The number the panel refuses a schedule
// with has to come from the configuration in use rather than from a constant in
// a document.
func TestBudgetPublishesTheArithmetic(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	p.SetAddresses(1)
	b := p.Budget()
	if b.PerAddressPerHour != 360 {
		t.Errorf("PerAddressPerHour = %d, want 360 (sixty per ten minutes)",
			b.PerAddressPerHour)
	}
	if b.WatchesAtFiveMinutes != 30 {
		t.Errorf("WatchesAtFiveMinutes = %d, want 30 — twelve passes an hour each",
			b.WatchesAtFiveMinutes)
	}
	p.SetAddresses(3)
	if got := p.Budget().WatchesAtFiveMinutes; got != 90 {
		t.Errorf("with three addresses = %d, want 90", got)
	}
}

// TestWindowSurvivesARestart. Restarting is otherwise a way to cheat the
// budget, and the budget exists because exceeding it hardens a 429 into a 403.
func TestWindowSurvivesARestart(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := testPacer(&now)
	for i := 0; i < 60; i++ {
		if err := p.Wait(context.Background(), "id"+strconv.Itoa(i), "addr1"); err != nil {
			t.Fatal(err)
		}
	}
	state := p.Snapshot()

	restarted := testPacer(&now)
	restarted.Restore(state)
	before := now
	if err := restarted.Wait(context.Background(), "brand-new", "addr1"); err != nil {
		t.Fatal(err)
	}
	if !now.After(before) {
		t.Error("a restart reset the per-address window; restarting must not be " +
			"a way to buy sixty more requests")
	}
}

// TestJitterKeepsTheFloor. A metronome is itself a signal, so the gap moves —
// but never below the floor, because the floor is the measurement.
func TestJitterKeepsTheFloor(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := NewPacer(PacerConfig{
		Floor: DefaultFloor, Jitter: DefaultJitter,
		Ceiling: 1000, Window: time.Hour,
		Now: func() time.Time { return now },
		SleepUntil: func(_ context.Context, t time.Time) error {
			if t.After(now) {
				now = t
			}
			return nil
		},
	})
	var gaps []time.Duration
	prev := now
	for i := 0; i < 20; i++ {
		if err := p.Wait(context.Background(), "a", "addr"); err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			gaps = append(gaps, now.Sub(prev))
		}
		prev = now
	}
	same := true
	for _, g := range gaps {
		if g < time.Duration(float64(DefaultFloor)*(1-DefaultJitter))-time.Millisecond {
			t.Errorf("a gap of %v fell below the jittered floor", g)
		}
		if g != gaps[0] {
			same = false
		}
	}
	if same {
		t.Error("every gap was identical; jitter exists because a metronome is " +
			"itself a signal")
	}
}
