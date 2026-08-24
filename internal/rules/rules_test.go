// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func priceEvent() Event {
	return Event{
		Change: store.ChangeRow{
			Kind: track.KindPriceChanged, ItemID: 7800000001, WatchID: 1,
			ThreadID: 5000, SellerID: 42,
			Field: "price", Was: "4000000", Now: "3500000",
			Unit: track.UnitMinor, Confidence: 1.0,
			HadBefore: true, HasNow: true,
		},
	}
}

func priceRule() Rule {
	return Rule{
		ID: 1, Name: "цена упала", Kind: track.KindPriceChanged,
		Condition: Node{}, Scope: Scope{Kind: ScopeWatch, ID: 1},
		Enabled: true, Targets: []int64{1},
	}
}

// TestDedupKeyForAPriceKindIsOnTheThread.
//
// This is a bug WB_GO cannot have. Without it a seller who reposts the same
// item at the same price every three days produces a fresh price-changed every
// time, because each incarnation is a new number with no history — and that is
// precisely the noise that gets a monitor switched off in its first week.
func TestDedupKeyForAPriceKindIsOnTheThread(t *testing.T) {
	a := priceEvent()
	b := priceEvent()
	b.Change.ItemID = 7800000999 // a repost: new number, same thread
	b.Change.ThreadID = a.Change.ThreadID

	if DedupKey(priceRule(), a) != DedupKey(priceRule(), b) {
		t.Error("two incarnations of one advertisement produced different dedup " +
			"keys; the second repost at the same price would be announced again")
	}
	if !strings.Contains(DedupKey(priceRule(), a), "5000") {
		t.Error("the key does not carry the thread id")
	}
	// And it still carries the numbers: a price that oscillates between two
	// values all afternoon produces a genuinely identical change each time it
	// comes back, and that IS the repetition deduplication means.
	c := priceEvent()
	c.Change.Now = "3600000"
	if DedupKey(priceRule(), a) == DedupKey(priceRule(), c) {
		t.Error("two different price moves share a key")
	}
}

// TestDedupKeyForAnAppearanceCarriesNoNumbers.
//
// A listing may be announced as new at most once, ever, however many times it
// flaps in and out of the window.
func TestDedupKeyForAnAppearanceCarriesNoNumbers(t *testing.T) {
	r := priceRule()
	r.Kind = track.KindListingAppeared

	a := priceEvent()
	a.Change.Kind, a.Change.Was, a.Change.Now = track.KindListingAppeared, "", ""
	b := a
	b.Change.TS = a.Change.TS + 100_000

	if DedupKey(r, a) != DedupKey(r, b) {
		t.Error("the same listing appearing twice produced two keys; a listing " +
			"that flaps in and out would be announced as new each time")
	}
	if strings.Contains(DedupKey(r, a), "4000000") {
		t.Error("an appearance key carries a number")
	}
}

// TestDedupKeyIsScopedToTheRule. Two rules on one change are two separate
// subscriptions, and one's memory must not silence the other.
func TestDedupKeyIsScopedToTheRule(t *testing.T) {
	first, second := priceRule(), priceRule()
	second.ID = 2
	if DedupKey(first, priceEvent()) == DedupKey(second, priceEvent()) {
		t.Error("two rules share a dedup key")
	}
}

// TestAnUnknownScopeCoversNothing. WB_GO's default, kept deliberately: a scope
// this build does not understand must not turn into "notify on every change in
// the database".
func TestAnUnknownScopeCoversNothing(t *testing.T) {
	s := Scope{Kind: ScopeKind("galaxy"), ID: 1}
	if s.Covers(priceEvent()) {
		t.Error("an unknown scope covered an event")
	}
}

// TestEachScopeCoversItsOwnSubject.
func TestEachScopeCoversItsOwnSubject(t *testing.T) {
	ev := priceEvent()
	cases := []struct {
		name  string
		scope Scope
		want  bool
	}{
		{"the listing itself", Scope{Kind: ScopeListing, ID: 7800000001}, true},
		{"another listing", Scope{Kind: ScopeListing, ID: 999}, false},
		{"the thread", Scope{Kind: ScopeThread, ID: 5000}, true},
		{"the seller", Scope{Kind: ScopeSeller, ID: 42}, true},
		{"the watch", Scope{Kind: ScopeWatch, ID: 1}, true},
		{"another watch", Scope{Kind: ScopeWatch, ID: 2}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.scope.Covers(ev); got != c.want {
				t.Errorf("Covers = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAPriceBandDoesNotIncludeSomethingWhosePriceWasNotRead.
//
// IncludeUnpriced defaults to false, keeping WB_GO's reasoning: a price band
// cannot include something whose price was not read. Here that is a large
// fraction of the catalogue rather than an edge case — «Цена договорная» is
// ordinary on this site — so the flag exists and its default is the safe one.
func TestAPriceBandDoesNotIncludeSomethingWhosePriceWasNotRead(t *testing.T) {
	ev := priceEvent()
	ev.Change.Now = "" // no price read
	s := Scope{Kind: ScopeFilter, Filter: Filter{
		PriceMinMinor: 1000000, PriceMaxMinor: 9000000,
	}}
	if s.Covers(ev) {
		t.Error("a price band covered a change with no price")
	}
	s.Filter.IncludeUnpriced = true
	if !s.Covers(ev) {
		t.Error("IncludeUnpriced did not include it")
	}
}

// TestAPriceBandIsInclusiveOfItsEnds. A person typing 40000 as the top means
// "and forty thousand", not "up to but not including".
func TestAPriceBandIsInclusiveOfItsEnds(t *testing.T) {
	s := Scope{Kind: ScopeFilter, Filter: Filter{
		PriceMinMinor: 3500000, PriceMaxMinor: 3500000,
	}}
	if !s.Covers(priceEvent()) {
		t.Error("a band whose ends equal the price excluded it")
	}
}

// TestValidateRefusesARuleThatCanNeverMatch. A rule that cannot fire is worse
// than no rule: the person believes they are covered.
func TestValidateRefusesARuleThatCanNeverMatch(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Rule)
	}{
		{"no kind", func(r *Rule) { r.Kind = "" }},
		{"a kind nothing emits", func(r *Rule) { r.Kind = track.Kind("price-doubled") }},
		{"no targets", func(r *Rule) { r.Targets = nil }},
		{"an inverted price band", func(r *Rule) {
			r.Scope = Scope{Kind: ScopeFilter, Filter: Filter{
				PriceMinMinor: 9000000, PriceMaxMinor: 1000000,
			}}
		}},
		{"a negative threshold", func(r *Rule) { r.ThresholdPct = -5 }},
		{"a scoped rule with no subject", func(r *Rule) {
			r.Scope = Scope{Kind: ScopeWatch, ID: 0}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := priceRule()
			c.edit(&r)
			if err := r.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestValidateReportsEveryReasonAtOnce. A form that rejects one field at a time
// is a form somebody submits five times.
func TestValidateReportsEveryReasonAtOnce(t *testing.T) {
	r := priceRule()
	r.Kind, r.Targets, r.ThresholdPct = "", nil, -1
	err := r.Validate()
	if err == nil {
		t.Fatal("accepted a rule with three faults")
	}
	for _, want := range []string{"kind", "target", "threshold"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("err = %q; it does not mention %q", err, want)
		}
	}
}

// TestEachOfTheThreeGatesRejectsOnItsOwn.
//
// Matches tests kind, then scope, then the condition, cheapest first — the tree
// is the only one that can be arbitrarily deep. The order is a cost argument
// and not observable from outside, so what is asserted here is that each gate
// rejects independently, which is what a wrong answer would break.
func TestEachOfTheThreeGatesRejectsOnItsOwn(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Rule)
	}{
		{"kind", func(r *Rule) { r.Kind = track.KindListingVanished }},
		{"scope", func(r *Rule) { r.Scope = Scope{Kind: ScopeWatch, ID: 99} }},
		{"condition", func(r *Rule) {
			r.Condition = Node{Op: OpCompare, Field: FieldPricePct,
				Cmp: CmpGreater, Value: 1000}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := priceRule()
			c.edit(&r)
			if r.Matches(priceEvent()) {
				t.Errorf("a rule whose %s does not apply matched", c.name)
			}
		})
	}
}

// TestARuleWithNoConditionFiresOnEveryChangeOfItsKind.
//
// The zero Node is an empty And, which is true. That is what "no condition"
// means, and getting it backwards would make every default rule silent.
func TestARuleWithNoConditionFiresOnEveryChangeOfItsKind(t *testing.T) {
	if !priceRule().Matches(priceEvent()) {
		t.Error("a rule with no condition matched nothing")
	}
}

// TestADisabledRuleMatchesNothing.
func TestADisabledRuleMatchesNothing(t *testing.T) {
	r := priceRule()
	r.Enabled = false
	if r.Matches(priceEvent()) {
		t.Error("a disabled rule matched")
	}
}

// TestAConditionCanAlsoSayYes.
//
// Without this every test above passes against a value() that returns false for
// everything, because each one asserts a rejection. It also pins the sign: a
// fall is a negative percentage, so "упала больше чем на 10%" is written as
// less-than minus ten, and getting that backwards makes every fall rule silent.
func TestAConditionCanAlsoSayYes(t *testing.T) {
	r := priceRule()
	r.Condition = Node{Op: OpCompare, Field: FieldPricePct,
		Cmp: CmpLess, Value: -10} // 4000000 -> 3500000 is -12.5%
	if !r.Matches(priceEvent()) {
		t.Error("a price that fell 12.5% did not satisfy \"fell by more than 10%\"")
	}

	r.Condition = Node{Op: OpAnd, Nodes: []Node{
		{Op: OpCompare, Field: FieldNow, Cmp: CmpLess, Value: 4000000},
		{Op: OpCompare, Field: FieldConfidence, Cmp: CmpGreaterOrEq, Value: 1},
	}}
	if !r.Matches(priceEvent()) {
		t.Error("a two-leaf And whose leaves both hold did not match")
	}
}

// TestALeafOnAFieldWithNoValueIsFalseWhicheverWayItPoints.
//
// This is the decision Eval documents, and it is only observable through a
// pair: "fell more than 5%" and "rose less than 5%" must BOTH be false on a
// price that appeared, which a missing-is-zero leaf gets exactly backwards.
func TestALeafOnAFieldWithNoValueIsFalseWhicheverWayItPoints(t *testing.T) {
	ev := priceEvent()
	ev.Change.Kind = track.KindPriceAppeared
	ev.Change.Was, ev.Change.HadBefore = "", false

	r := priceRule()
	r.Kind = track.KindPriceAppeared
	for _, c := range []Cmp{CmpLess, CmpGreater, CmpEqual, CmpNotEqual} {
		r.Condition = Node{Op: OpCompare, Field: FieldPricePct, Cmp: c, Value: -5}
		if r.Matches(ev) {
			t.Errorf("%q matched a change with no percentage to compare", c)
		}
	}
}

// TestATitleIsNotANumber. Was and Now are TEXT and hold a title as readily as a
// price, so a numeric leaf against one must be false rather than a zero.
func TestATitleIsNotANumber(t *testing.T) {
	ev := priceEvent()
	ev.Change.Kind, ev.Change.Field, ev.Change.Unit = track.KindTitleChanged, "title", track.UnitText
	ev.Change.Was, ev.Change.Now = "iPhone 15, 128 ГБ", "iPhone 15, 256 ГБ"

	r := priceRule()
	r.Kind = track.KindTitleChanged
	r.Condition = Node{Op: OpCompare, Field: FieldNow, Cmp: CmpGreaterOrEq, Value: 0}
	if r.Matches(ev) {
		t.Error("a title satisfied a numeric comparison; it parsed as zero")
	}
}

// TestPositionIsAbsentRatherThanFirst. Most changes do not come from a window
// pass at all, and a zero position would read as "первое место" — the single
// most misleading value the field could take.
func TestPositionIsAbsentRatherThanFirst(t *testing.T) {
	r := priceRule()
	r.Condition = Node{Op: OpCompare, Field: FieldPosition, Cmp: CmpLessOrEq, Value: 3}
	if r.Matches(priceEvent()) {
		t.Error("a change with no position satisfied \"in the top three\"")
	}
	ev := priceEvent()
	two := int64(2)
	ev.Position = &two
	if !r.Matches(ev) {
		t.Error("a listing standing second did not satisfy \"in the top three\"")
	}
}
