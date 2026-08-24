// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// TestAddingAWatchCreatesFourRulesScopedToIt.
//
// Per watch and not one shared global copy, for two reasons that pull the same
// way: a rule scoped to a watch that does not exist yet cannot be written in
// advance, and a shared rule would make deleting one watch silence another.
func TestAddingAWatchCreatesFourRulesScopedToIt(t *testing.T) {
	got := DefaultsForWatch(7, []int64{1})
	if len(got) != 4 {
		t.Fatalf("%d rules, want 4", len(got))
	}
	want := map[track.Kind]bool{
		track.KindListingAppeared: true, track.KindPriceChanged: true,
		track.KindListingVanished: true, track.KindListingClosed: true,
	}
	seen := map[track.Kind]bool{}
	for _, r := range got {
		if !want[r.Kind] {
			t.Errorf("unexpected default %q", r.Kind)
		}
		seen[r.Kind] = true
		if r.Scope.Kind != ScopeWatch || r.Scope.ID != 7 {
			t.Errorf("%q is scoped to %+v, want watch 7", r.Kind, r.Scope)
		}
		if !r.FromDefaults {
			t.Errorf("%q is not marked as coming from the default set; the panel "+
				"says which rules did, so that deleting one is an informed act", r.Kind)
		}
		if !r.Enabled {
			t.Errorf("%q is created disabled", r.Kind)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%q does not validate: %v", r.Kind, err)
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("%q is missing from the default set", k)
		}
	}
}

// TestTheAppearanceRuleNeedsNoThresholdAndNoRateLimit.
//
// Its dedup key carries no numbers, so it fires at most once per listing ever.
// A floor and a limit would both be dead configuration a person still has to
// read and reason about on the Правила screen.
func TestTheAppearanceRuleNeedsNoThresholdAndNoRateLimit(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind != track.KindListingAppeared {
			continue
		}
		if r.ThresholdPct != 0 || r.ThresholdMinor != 0 {
			t.Errorf("threshold %d%% / %d minor on an appearance rule",
				r.ThresholdPct, r.ThresholdMinor)
		}
		if r.MinInterval != 0 {
			t.Errorf("MinInterval %v on a rule that fires once per listing ever",
				r.MinInterval)
		}
		return
	}
	t.Fatal("no appearance rule")
}

// TestThePriceRuleHasAFloorSoARoundingMoveIsNotNews.
func TestThePriceRuleHasAFloorSoARoundingMoveIsNotNews(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind != track.KindPriceChanged {
			continue
		}
		if r.ThresholdPct <= 0 && r.ThresholdMinor <= 0 {
			t.Error("the price rule has no floor; a one-rouble move would be a " +
				"message")
		}
		return
	}
	t.Fatal("no price rule")
}

// TestTheVanishedRuleIsEnabledByDefault, per decision 5. The honesty comes from
// the «предположительно» prefix in the message rather than from hiding the
// alert.
func TestTheVanishedRuleIsEnabledByDefault(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind == track.KindListingVanished && !r.Enabled {
			t.Error("the vanished rule is created disabled; decision 5 says it " +
				"ships on, and the prefix is what makes that honest")
		}
	}
}

// TestTheFiveGlobalRulesAreRateLimitedToOnePerSixHours.
//
// An install that has been flagged should say so once rather than either never
// or continuously.
func TestTheFiveGlobalRulesAreRateLimitedToOnePerSixHours(t *testing.T) {
	got := GlobalDefaults([]int64{1})
	if len(got) != 5 {
		t.Fatalf("%d global rules, want 5", len(got))
	}
	want := map[track.Kind]bool{
		track.KindTransportBlocked: true, track.KindExtractionFellBack: true,
		track.KindCollectorShapeUnknown: true, track.KindCollectorSilent: true,
		track.KindWorkerPanicked: true,
	}
	for _, r := range got {
		if !want[r.Kind] {
			t.Errorf("unexpected global default %q", r.Kind)
		}
		if r.MinInterval != 6*time.Hour {
			t.Errorf("%q has MinInterval %v, want six hours", r.Kind, r.MinInterval)
		}
		if r.Scope.Kind != ScopeFilter {
			t.Errorf("%q is scoped to %q; a health rule is about the install and "+
				"not about one watch", r.Kind, r.Scope.Kind)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%q does not validate: %v", r.Kind, err)
		}
	}
}

// TestEveryDefaultRuleNamesAKindThisBuildCanEmit. A default that can never fire
// is worse than no default: the person believes they are covered.
func TestEveryDefaultRuleNamesAKindThisBuildCanEmit(t *testing.T) {
	known := map[track.Kind]bool{}
	for _, k := range track.Kinds() {
		known[k] = true
	}
	all := append(DefaultsForWatch(1, []int64{1}), GlobalDefaults([]int64{1})...)
	for _, r := range all {
		if !known[r.Kind] {
			t.Errorf("%q is not in track.Kinds()", r.Kind)
		}
	}
}

// TestTheNineDefaultsSurviveBeingStored.
//
// Every other test here reads them straight out of the constructor, where a
// rule that cannot be persisted looks perfect. These are the rules a person
// gets without asking for any, and a default that fails on the way into the
// database is a first run that reports an error the person did nothing to cause.
func TestTheNineDefaultsSurviveBeingStored(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := addTarget(t, s)

	want := append(DefaultsForWatch(7, []int64{tgt}), GlobalDefaults([]int64{tgt})...)
	if len(want) != 9 {
		t.Fatalf("%d default rules, want 9 — «набор по умолчанию» has to be a "+
			"number somewhere or it goes missing", len(want))
	}
	for _, r := range want {
		if _, err := Save(ctx, s, r); err != nil {
			t.Fatalf("%q: %v", r.Name, err)
		}
	}

	got, err := All(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 9 {
		t.Fatalf("read back %d rules, want 9", len(got))
	}
	for i, r := range got {
		if !r.FromDefaults {
			t.Errorf("%q came back without its provenance", r.Name)
		}
		if r.Kind != want[i].Kind || !reflect.DeepEqual(r.Scope, want[i].Scope) ||
			r.MinInterval != want[i].MinInterval ||
			r.ThresholdPct != want[i].ThresholdPct ||
			r.ThresholdMinor != want[i].ThresholdMinor {
			t.Errorf("%q came back different:\n got %+v\nwant %+v",
				r.Name, r, want[i])
		}
	}
}

// TestEveryDefaultFiresOnAChangeOfItsKind.
//
// Validate says a rule is well-formed. It does not say the rule will ever
// match, and a default whose condition or scope quietly excludes its own kind
// is the failure this whole task exists to prevent: the person believes they
// are covered.
func TestEveryDefaultFiresOnAChangeOfItsKind(t *testing.T) {
	for _, r := range DefaultsForWatch(7, []int64{1}) {
		ev := priceEvent()
		ev.Change.Kind, ev.Change.WatchID = r.Kind, 7
		if !r.Matches(ev) {
			t.Errorf("the default %q does not match a change of its own kind", r.Name)
		}
	}
	for _, r := range GlobalDefaults([]int64{1}) {
		ev := priceEvent()
		ev.Change.Kind = r.Kind
		// A health change carries no price and no watch, which is the shape
		// that would trip a scope or a floor written for listings.
		ev.Change.Was, ev.Change.Now = "", ""
		ev.Change.HadBefore, ev.Change.HasNow = false, false
		ev.Change.Unit, ev.Change.WatchID = track.UnitNone, 0
		if !r.Matches(ev) {
			t.Errorf("the health default %q does not match its own kind", r.Name)
		}
	}
}
