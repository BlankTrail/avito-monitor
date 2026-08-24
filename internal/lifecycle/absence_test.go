// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import (
	"strings"
	"testing"
)

// TestAnAbsenceExplainedByArrivalsIsNotEvidence.
//
// In a date-sorted window of k slots, an item at rank r is pushed out by k − r
// arrivals. That is exact arithmetic over two integers the schema already
// stores and one it counts per pass, it costs no extra request, and it is the
// difference between "gone" and "we only look at the first page".
func TestAnAbsenceExplainedByArrivalsIsNotEvidence(t *testing.T) {
	// Rank 28 of a 30-slot window: two slots of headroom, three arrivals.
	got := Explain(AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 28, WindowSize: 30},
		Arrivals:      3,
		Now:           10_000,
		Confirmations: 2, MinSpanSec: 1200,
	})
	if !got.Displaced {
		t.Errorf("an item at rank 28 of 30 with 3 arrivals was not called "+
			"displaced: %s", got.Why)
	}
	if got.Confirmed {
		t.Error("a displaced item was confirmed gone")
	}
	if strings.TrimSpace(got.Why) == "" {
		t.Error("the verdict carries no reason; the panel shows it")
	}
}

// TestAnAbsenceWithHeadroomIsACandidate. Rank 3 of 30 needs 27 arrivals to be
// pushed off, and three does not do it.
func TestAnAbsenceWithHeadroomIsACandidate(t *testing.T) {
	got := Explain(AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 3, WindowSize: 30},
		Arrivals:      3,
		Now:           10_000,
		Confirmations: 2, MinSpanSec: 1200,
	})
	if got.Displaced {
		t.Errorf("an item at rank 3 of 30 was called displaced by 3 arrivals: %s",
			got.Why)
	}
}

// TestTheBoundaryIsExactlyTheHeadroom. k − r arrivals is enough, one fewer is
// not. An off-by-one here is either a false removal or a missed one, every
// tick, on the busiest watches.
func TestTheBoundaryIsExactlyTheHeadroom(t *testing.T) {
	m := Member{ItemID: 1, LastPosition: 27, WindowSize: 30} // headroom 3
	for _, c := range []struct {
		arrivals int
		want     bool
	}{{2, false}, {3, true}, {4, true}} {
		got := Explain(AbsenceInput{
			Member: m, Arrivals: c.arrivals, Now: 10_000,
			Confirmations: 2, MinSpanSec: 1200,
		})
		if got.Displaced != c.want {
			t.Errorf("%d arrivals against headroom 3: displaced = %v, want %v",
				c.arrivals, got.Displaced, c.want)
		}
	}
}

// TestBothGatesAreRequired.
//
// Two passes thirty seconds apart is one ranking blip. The span gate is what
// makes the count mean elapsed time rather than poll count — without it a watch
// on a two-minute interval confirms a disappearance in four minutes and a watch
// on an hourly one takes two hours, on the same evidence.
func TestBothGatesAreRequired(t *testing.T) {
	base := Member{ItemID: 1, LastPosition: 3, WindowSize: 30}
	cases := []struct {
		name   string
		streak int
		since  int64
		want   bool
	}{
		{"neither gate", 1, 9_900, false},
		{"count only", 2, 9_900, false},
		{"span only", 1, 8_000, false},
		{"both", 2, 8_000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base
			m.MissingStreak, m.MissingSince = c.streak, c.since
			got := Explain(AbsenceInput{
				Member: m, Arrivals: 0, Now: 10_000,
				Confirmations: 2, MinSpanSec: 1200,
			})
			if got.Confirmed != c.want {
				t.Errorf("Confirmed = %v, want %v (%s)", got.Confirmed, c.want, got.Why)
			}
		})
	}
}

// TestAZeroWindowIsNeverDisplacement.
//
// A pass that returned nothing has a window of zero, and k − r with k of zero
// would make every absence displacement — the exact opposite of what an empty
// result set means.
func TestAZeroWindowIsNeverDisplacement(t *testing.T) {
	got := Explain(AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 3, WindowSize: 0},
		Arrivals:      0,
		Now:           10_000,
		Confirmations: 2, MinSpanSec: 1200,
	})
	if got.Displaced {
		t.Error("an absence from an empty result set was called displacement")
	}
}

// TestDisplacementNeverConfirms, whatever the gates say. The arithmetic is the
// stronger statement: we know why it is not here.
func TestDisplacementNeverConfirms(t *testing.T) {
	got := Explain(AbsenceInput{
		Member: Member{
			ItemID: 1, LastPosition: 29, WindowSize: 30,
			MissingStreak: 9, MissingSince: 1,
		},
		Arrivals:      5,
		Now:           10_000,
		Confirmations: 2, MinSpanSec: 1200,
	})
	if got.Confirmed {
		t.Error("an item explained by displacement was confirmed gone despite " +
			"both gates passing; the arithmetic is the stronger statement")
	}
}

// TestAMissingSinceOfZeroDoesNotPassTheSpanGate.
//
// Zero is "we have not recorded when it went missing", not "it went missing at
// the epoch". Read the wrong way it makes now − 0 an enormous span and every
// first absence instantly confirmable, which is the false-removal machine this
// whole design exists to avoid.
func TestAMissingSinceOfZeroDoesNotPassTheSpanGate(t *testing.T) {
	got := Explain(AbsenceInput{
		Member: Member{
			ItemID: 1, LastPosition: 3, WindowSize: 30,
			MissingStreak: 5, MissingSince: 0,
		},
		Arrivals:      0,
		Now:           10_000,
		Confirmations: 2, MinSpanSec: 1200,
	})
	if got.Confirmed {
		t.Error("a member with no recorded missing_since was confirmed gone; " +
			"zero is an absent record, not 1970")
	}
}
