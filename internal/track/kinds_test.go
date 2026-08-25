// SPDX-License-Identifier: AGPL-3.0-or-later

package track

import "testing"

// TestKindsOffersEverythingThisBuildCanEmitAndNothingElse.
//
// A rule written on a kind nothing can emit never fires, and a person whose
// rule never fires concludes nothing is changing rather than that they picked a
// dead kind. So the catalogue is what the panel offers, and it must equal what
// the producers actually write.
func TestKindsOffersEverythingThisBuildCanEmitAndNothingElse(t *testing.T) {
	want := []Kind{
		// The window producer, inside the pass transaction.
		KindListingAppeared, KindListingReturned, KindListingLeftWindow,
		KindListingVanished, KindListingClosed,
		KindListingReposted, KindRepostRetracted,
		// The series producer, out of band over two snapshots.
		KindPriceChanged, KindPriceAppeared, KindPriceWithdrawn,
		KindDiscountChanged,
		KindTitleChanged, KindPromotionChanged, KindReservedChanged,
		KindListingBumped,
		// The transport producer: the collector observing itself.
		KindExtractionFellBack, KindTransportBlocked, KindCollectorShapeUnknown,
		KindCollectorSilent, KindWorkerPanicked, KindWatchUndersampled,
		KindWatchTooBroad,
	}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() has %d entries, want %d", len(got), len(want))
	}
	seen := map[Kind]bool{}
	for _, k := range got {
		if seen[k] {
			t.Errorf("%q appears twice", k)
		}
		seen[k] = true
	}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("%q is missing from Kinds()", k)
		}
	}
}

// TestEveryKindIsSpelledInKebabCase. The kind is written into a TEXT column, is
// matched by rules a person typed, and appears in a Telegram message. One
// spelling convention, enforced, is cheaper than three that drifted.
func TestEveryKindIsSpelledInKebabCase(t *testing.T) {
	for _, k := range Kinds() {
		s := string(k)
		if s == "" {
			t.Error("an empty kind")
			continue
		}
		for i, r := range s {
			if !((r >= 'a' && r <= 'z') || r == '-') {
				t.Errorf("%q has %q at %d; kinds are lower-case kebab", k, r, i)
				break
			}
		}
		if s[0] == '-' || s[len(s)-1] == '-' {
			t.Errorf("%q starts or ends with a dash", k)
		}
	}
}

// TestEveryKindHasARussianLabel.
//
// The rule editor draws its dropdown from Kinds(). A kind added to the
// catalogue and forgotten in the labels would appear there as
// «watch-undersampled», which tells a Russian-speaking person nothing — and the
// only place that would be noticed is in front of one.
func TestEveryKindHasARussianLabel(t *testing.T) {
	for _, k := range Kinds() {
		if Label(k) == string(k) {
			t.Errorf("%s has no label", k)
		}
	}
}
