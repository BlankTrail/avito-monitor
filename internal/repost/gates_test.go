// SPDX-License-Identifier: AGPL-3.0-or-later

package repost

import (
	"strings"
	"testing"
	"unicode"
)

const thirtyDays int64 = 30 * 24 * 60 * 60

// aVanished is a predecessor: seen between two moments, then gone.
func aVanished(id, firstSeen, lastSeen int64) Listing {
	return Listing{
		ItemID: id, SellerKey: "brand:one", SellerQuality: "slug",
		RepostKey: "k", Title: "iphone 15 128 гб",
		CategoryRootID: 9, State: "vanished",
		FirstSeenAt: firstSeen, LastSeenAt: lastSeen,
		PriceMinor: 4_000_000,
	}
}

// anArrival is a successor: first seen now.
func anArrival(id, firstSeen int64) Listing {
	l := aVanished(id, firstSeen, firstSeen)
	l.State = "active"
	return l
}

// TestAPairThatCoexistedIsTwoThingsAndNotOneThingTwice.
//
// The strongest gate, and the one the donor lost. Thread-and-Window put this
// veto in pass membership and then thinned pass membership at seven days, so it
// quietly stopped working after week one. Here it reads the listings' own
// observation interval, which is permanent and covers every watch.
//
// Measured: one ordinary page carried FOUR pairs sharing a full repost key with
// both members alive at the same moment — same seller, same category, same
// place, the same title word for word. This gate is what refuses them, and they
// are not the edge case.
func TestAPairThatCoexistedIsTwoThingsAndNotOneThingTwice(t *testing.T) {
	older := aVanished(100, 1000, 2000)

	overlapping := anArrival(200, 1500)
	got := Gates(overlapping, older, false, thirtyDays)
	if got.Passed {
		t.Error("two listings alive at the same moment were linked")
	}
	if got.Refused != GateCoexisted {
		t.Errorf("refused by %q, want %q", got.Refused, GateCoexisted)
	}

	// Strictly after, so intervals that merely touch are still two things: the
	// two were both observed at that second.
	if Gates(anArrival(200, 2000), older, false, thirtyDays).Passed {
		t.Error("intervals that touch were treated as disjoint")
	}
	if !Gates(anArrival(200, 2001), older, false, thirtyDays).Passed {
		t.Error("a genuinely later listing was refused")
	}
}

// TestAPinCanNeverBeUndone.
//
// A pair a person has said are not the same thing can never be re-linked, by
// any future change to the ladder. That is what makes correcting this product
// feel like correcting it rather than arguing with it.
func TestAPinCanNeverBeUndone(t *testing.T) {
	got := Gates(anArrival(200, 3000), aVanished(100, 1000, 2000), true, thirtyDays)
	if got.Passed {
		t.Error("a pinned pair was linked")
	}
	if got.Refused != GatePinned {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestOnlyAnEndedListingCanHaveASuccessor.
//
// left-window is too weak to build a merge on: it means we stopped seeing it on
// page one, which happens to every listing on a busy query within the hour.
func TestOnlyAnEndedListingCanHaveASuccessor(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{
		{"vanished", true},
		{"closed", true},
		{"left-window", false},
		{"active", false},
	} {
		older := aVanished(100, 1000, 2000)
		older.State = c.state
		got := Gates(anArrival(200, 3000), older, false, thirtyDays)
		if got.Passed != c.want {
			t.Errorf("predecessor %q: passed=%v, want %v", c.state, got.Passed, c.want)
		}
		if !c.want && got.Refused != GateNotEnded {
			t.Errorf("predecessor %q refused by %q", c.state, got.Refused)
		}
	}
}

// TestTheGapMustBeInsideTheRepostWindow, so a listing from March is not linked
// to one in August on the strength of a title.
func TestTheGapMustBeInsideTheRepostWindow(t *testing.T) {
	older := aVanished(100, 1000, 2000)

	if !Gates(anArrival(200, 2000+thirtyDays), older, false, thirtyDays).Passed {
		t.Error("a gap exactly at the window was refused; the bound is inclusive")
	}
	got := Gates(anArrival(200, 2000+thirtyDays+1), older, false, thirtyDays)
	if got.Passed {
		t.Error("a gap outside the window was allowed")
	}
	if got.Refused != GateWindow {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestDifferentSellersIsARejectionAndNotADoubt.
//
// A listing copied verbatim by somebody else is a scam clone, and that is
// precisely the thing a person must be told about — so it must produce an
// ordinary new-listing alert rather than a withheld one.
func TestDifferentSellersIsARejectionAndNotADoubt(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.SellerKey, older.SellerQuality = "brand:one", "slug"
	newer := anArrival(200, 3000)
	newer.SellerKey, newer.SellerQuality = "brand:two", "slug"

	got := Gates(newer, older, false, thirtyDays)
	if got.Passed || got.Undetermined {
		t.Errorf("two different sellers: %+v", got)
	}
	if got.Refused != GateSeller {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestAnUnknownSellerLeavesTheGateUndeterminedRatherThanOpen.
//
// Undetermined caps the outcome at a proposal that can never suppress an alert.
// Treating it as a pass would let the mobile surface — which identifies no
// seller at all, 0 of 30 measured — auto-link on a title, which is the
// false-positive factory this whole design is arranged to avoid.
func TestAnUnknownSellerLeavesTheGateUndeterminedRatherThanOpen(t *testing.T) {
	for _, c := range []struct{ older, newer string }{
		{"", "brand:one"}, {"brand:one", ""}, {"", ""},
	} {
		older := aVanished(100, 1000, 2000)
		older.SellerKey = c.older
		newer := anArrival(200, 3000)
		newer.SellerKey = c.newer

		got := Gates(newer, older, false, thirtyDays)
		if !got.Undetermined {
			t.Errorf("(%q, %q): Undetermined = false", c.older, c.newer)
		}
		if !got.Passed {
			t.Errorf("(%q, %q): an undetermined seller refused the pair outright",
				c.older, c.newer)
		}
	}
}

// TestAHeuristicKeyIsNeverEvidence.
//
// The schema records key_quality precisely so this rule can be enforced.
// Inventing an identity that silently merges two people is worse than declining
// to identify either, so a made-up key is treated as no key at all.
func TestAHeuristicKeyIsNeverEvidence(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.SellerKey, older.SellerQuality = "brand:one", "heuristic"
	newer := anArrival(200, 3000)
	newer.SellerKey, newer.SellerQuality = "brand:one", "slug"

	got := Gates(newer, older, false, thirtyDays)
	if !got.Undetermined {
		t.Errorf("a heuristic key was accepted as a seller match: %+v", got)
	}
	// And it is undetermined rather than a rejection: the two may well be one
	// person, and refusing outright would lose a real link over our own doubt.
	if !got.Passed {
		t.Error("a heuristic key refused the pair outright")
	}
}

// TestTheRootCategoryMustMatch, so a phone is never a successor to a sofa.
func TestTheRootCategoryMustMatch(t *testing.T) {
	older := aVanished(100, 1000, 2000)
	older.CategoryRootID = 9
	newer := anArrival(200, 3000)
	newer.CategoryRootID = 4

	got := Gates(newer, older, false, thirtyDays)
	if got.Passed {
		t.Error("two root categories were linked")
	}
	if got.Refused != GateCategory {
		t.Errorf("refused by %q", got.Refused)
	}
}

// TestAnUnknownCategoryDoesNotRefuse.
//
// The mobile surface carries no category at all, 0 of 30. A zero on either side
// is our silence and not a mismatch, and refusing on it would make the gate
// reject the entire mobile population for a reason that is ours.
func TestAnUnknownCategoryDoesNotRefuse(t *testing.T) {
	for _, c := range []struct{ older, newer int64 }{{0, 9}, {9, 0}, {0, 0}} {
		older := aVanished(100, 1000, 2000)
		older.CategoryRootID = c.older
		newer := anArrival(200, 3000)
		newer.CategoryRootID = c.newer
		if !Gates(newer, older, false, thirtyDays).Passed {
			t.Errorf("(%d, %d) was refused on an unknown category", c.older, c.newer)
		}
	}
}

// TestEveryRefusalSaysWhyInRussian.
//
// The panel shows this verbatim, and «coexisted» is an answer only to whoever
// wrote this code. A new gate with no wording fails here rather than on screen.
func TestEveryRefusalSaysWhyInRussian(t *testing.T) {
	older := aVanished(100, 1000, 2000)

	cases := map[Gate]GateResult{
		GatePinned:    Gates(anArrival(200, 3000), older, true, thirtyDays),
		GateCoexisted: Gates(anArrival(200, 1500), older, false, thirtyDays),
		GateWindow:    Gates(anArrival(200, 2000+thirtyDays+1), older, false, thirtyDays),
	}
	ended := aVanished(100, 1000, 2000)
	ended.State = "left-window"
	cases[GateNotEnded] = Gates(anArrival(200, 3000), ended, false, thirtyDays)

	other := aVanished(100, 1000, 2000)
	other.CategoryRootID = 4
	cases[GateCategory] = Gates(anArrival(200, 3000), other, false, thirtyDays)

	rival := aVanished(100, 1000, 2000)
	rival.SellerKey = "brand:two"
	cases[GateSeller] = Gates(anArrival(200, 3000), rival, false, thirtyDays)

	for gate, got := range cases {
		if got.Passed {
			t.Errorf("%s: expected a refusal, got a pass", gate)
			continue
		}
		if got.Refused != gate {
			t.Errorf("%s: refused by %q instead", gate, got.Refused)
		}
		if got.Why == "" {
			t.Errorf("%s: refused with no reason", gate)
			continue
		}
		if !hasCyrillic(got.Why) {
			t.Errorf("%s: reason is not Russian: %q", gate, got.Why)
		}
	}
	if len(cases) != len(AllGates()) {
		t.Errorf("%d gates exercised, the catalogue has %d",
			len(cases), len(AllGates()))
	}
}

func hasCyrillic(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.Is(unicode.Cyrillic, r)
	})
}
