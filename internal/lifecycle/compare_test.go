// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import (
	"strings"
	"testing"
)

func TestTwoPassesOnOneIdentityAreComparable(t *testing.T) {
	if v := Comparable(admissiblePass()); !v.Admissible {
		t.Errorf("two passes on one session were refused: %s", v.Why)
	}
}

// TestAnIdentityChangeMakesTwoPassesIncomparable.
//
// Measured 2026-08-24: in direct mode the result set is a function of srv_id.
// Six passes over two identities gave 100% overlap within an identity — every
// pair, same order — and 23% across them.
//
// The ladder changes the identity by itself: it renews on a 403 that three
// waits did not clear, and recycles the endpoint on a soft block. Differenced
// against its predecessor, the next pass reads as twenty-three listings
// vanishing and twenty-three arriving, on schedule, for ever.
//
// No confirmation count and no elapsed span can filter this out, because the
// second confirmation is as wrong as the first and arrives on time.
func TestAnIdentityChangeMakesTwoPassesIncomparable(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = "s0"
	v := Comparable(p)
	if v.Admissible {
		t.Fatal("two passes read through different identities were compared")
	}
	if !strings.Contains(v.Why, "identity") && !strings.Contains(v.Why, "session") {
		t.Errorf("Why = %q; it is read by a person and must name the reason", v.Why)
	}
}

// TestNoPredecessorIsIncomparableRatherThanComparable. The first pass of a
// watch has nothing behind it, and treating that as a match would make every
// seeding pass a full set of arrivals AND a full set of departures.
func TestNoPredecessorIsIncomparableRatherThanComparable(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = ""
	if v := Comparable(p); v.Admissible {
		t.Error("a pass with no predecessor was called comparable")
	}
}

// TestComparableIsSeparateFromAdmissible, and both are required.
//
// They answer different questions. Admissible asks whether this pass is a good
// reading; Comparable asks whether it and the one before it are readings of the
// same thing. A pass can be perfect and still be incomparable, which is exactly
// what an identity renewal produces.
func TestComparableIsSeparateFromAdmissible(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = "s0"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("Admissible refused a clean pass because of its predecessor: %s", v.Why)
	}
	if v := Comparable(p); v.Admissible {
		t.Error("Comparable admitted a pass across an identity change")
	}
}

// TestComparableDoesNotCareWhetherThePassWasAdmissible. The two are orthogonal,
// and folding them would hide which one refused — the reasons go to different
// places and mean different things to whoever reads them.
func TestComparableDoesNotCareWhetherThePassWasAdmissible(t *testing.T) {
	p := admissiblePass()
	p.Class = "soft-block" // inadmissible
	if v := Comparable(p); !v.Admissible {
		t.Errorf("Comparable refused because of the class: %s — that is "+
			"Admissible's question", v.Why)
	}
}

func ids(from, to int64) []int64 {
	var out []int64
	for i := from; i < to; i++ {
		out = append(out, i)
	}
	return out
}

// TestAChangedIdentityOverTheSameMarketIsComparable.
//
// Measured on 11 October 2026 through BlankTrail with a rotating pool: passes on
// different sessions and exits shared thirty of thirty, in order. The identity
// rule, taken as written, called every one of them incomparable.
func TestAChangedIdentityOverTheSameMarketIsComparable(t *testing.T) {
	prev := ids(100, 130)
	// Three arrivals on top, the old window below them, three pushed off.
	cur := append([]int64{1, 2, 3}, ids(100, 127)...)
	v := Comparable(PassFacts{Session: "b", PrevSession: "a", PrevItems: prev, Items: cur})
	if !v.Admissible {
		t.Errorf("same market, new identity: %s", v.Why)
	}
}

// TestAChangedIdentityOverADifferentSliceIsNot. The direct-mode measurement
// that made the rule: 23% shared across identities, scattered.
func TestAChangedIdentityOverADifferentSliceIsNot(t *testing.T) {
	prev := ids(100, 130)
	var cur []int64
	for i := int64(0); i < 30; i++ {
		if i%4 == 0 {
			cur = append(cur, 100+i) // one in four shared
		} else {
			cur = append(cur, 900+i)
		}
	}
	v := Comparable(PassFacts{Session: "b", PrevSession: "a", PrevItems: prev, Items: cur})
	if v.Admissible {
		t.Error("a different slice was taken for the same market")
	}
}
