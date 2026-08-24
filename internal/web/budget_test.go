// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"math"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func watch(id int64, intervalSec, pages int, enabled bool) store.Watch {
	return store.Watch{ID: id, Name: "наблюдение", MaxPages: pages,
		IntervalSec: intervalSec, Enabled: enabled}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestOnePageEveryFiveMinutesCostsAFifthOfARequestAMinute.
func TestOnePageEveryFiveMinutesCostsAFifthOfARequestAMinute(t *testing.T) {
	if got := CostPerMinute(watch(1, 300, 1, true)); !near(got, 0.2) {
		t.Errorf("CostPerMinute = %v, want 0.2", got)
	}
	// Three pages costs three times as much: a pass is one request per page.
	if got := CostPerMinute(watch(1, 300, 3, true)); !near(got, 0.6) {
		t.Errorf("CostPerMinute = %v, want 0.6", got)
	}
}

// TestAWatchWithNoIntervalCostsNothingRatherThanInfinity.
//
// Zero is what an unsaved form and a hand-edited row both hold. Divided into
// sixty it is +Inf, which would make every budget full and every save refused —
// a product that cannot be configured because one row is malformed.
func TestAWatchWithNoIntervalCostsNothingRatherThanInfinity(t *testing.T) {
	if got := CostPerMinute(watch(1, 0, 1, true)); got != 0 {
		t.Errorf("CostPerMinute = %v, want 0", got)
	}
	if got := CostPerMinute(watch(1, -5, 1, true)); got != 0 {
		t.Errorf("CostPerMinute = %v, want 0", got)
	}
}

// TestAWatchWithNoPagesStillCostsOne. A pass that fetched nothing would not be
// a pass.
func TestAWatchWithNoPagesStillCostsOne(t *testing.T) {
	if got := CostPerMinute(watch(1, 60, 0, true)); !near(got, 1) {
		t.Errorf("CostPerMinute = %v, want 1", got)
	}
}

// TestADisabledWatchCostsNothing.
func TestADisabledWatchCostsNothing(t *testing.T) {
	b := BudgetFor(6, 1, []store.Watch{watch(1, 60, 1, false)}, 0)
	if b.UsedPerMin != 0 {
		t.Errorf("a disabled watch used %v", b.UsedPerMin)
	}
}

// TestTheCeilingIsTheTighterOfTheTwoLimits.
//
// transport.DefaultCeiling is 60 in a 10-minute window, which is six a minute.
// A person who typed 60 into requests_per_minute meant "as fast as possible"
// and must still get six: quoting the looser of two limits is how a budget gets
// overspent.
func TestTheCeilingIsTheTighterOfTheTwoLimits(t *testing.T) {
	b := BudgetFor(60, 1, nil, 0)
	if !near(b.PerAddressPerMin, 6) {
		t.Errorf("PerAddressPerMin = %v, want 6", b.PerAddressPerMin)
	}
	// And a lower setting is honoured, because that direction is the user
	// choosing to be gentler than the measurement requires.
	if b := BudgetFor(2, 1, nil, 0); !near(b.PerAddressPerMin, 2) {
		t.Errorf("PerAddressPerMin = %v, want 2", b.PerAddressPerMin)
	}
}

// TestAddressesMultiplyTheCeiling.
//
// This is why «I added ports and it got banned faster» has an answer: ports are
// not addresses. Four addresses is four ceilings; four ports on one address is
// one ceiling reached four times as fast.
func TestAddressesMultiplyTheCeiling(t *testing.T) {
	b := BudgetFor(6, 4, nil, 0)
	if !near(b.CapacityPerMin, 24) {
		t.Errorf("CapacityPerMin = %v, want 24", b.CapacityPerMin)
	}
	// Zero addresses is one: the host's own. A budget of nothing would refuse
	// every watch on an install that has not configured a proxy, which is most
	// of them.
	if b := BudgetFor(6, 0, nil, 0); b.Addresses != 1 || !near(b.CapacityPerMin, 6) {
		t.Errorf("with no channels: %+v, want one address and six a minute", b)
	}
}

// TestAScheduleOverTheBudgetDoesNotFit.
func TestAScheduleOverTheBudgetDoesNotFit(t *testing.T) {
	// One address, six a minute. Six watches at one page a minute is exactly
	// the ceiling; a seventh is over it.
	ws := []store.Watch{}
	for i := int64(1); i <= 6; i++ {
		ws = append(ws, watch(i, 60, 1, true))
	}
	b := BudgetFor(6, 1, ws, 0)
	if !near(b.UsedPerMin, 6) {
		t.Errorf("UsedPerMin = %v, want 6", b.UsedPerMin)
	}
	if !near(b.FreePerMin(), 0) {
		t.Errorf("FreePerMin = %v, want 0", b.FreePerMin())
	}
	if b.Fits(1) {
		t.Error("a seventh watch fitted into a full budget")
	}
	// Exactly at the ceiling fits: a person who filled it deliberately must not
	// be told their own arithmetic is wrong.
	if b := BudgetFor(6, 1, ws[:5], 0); !b.Fits(1) {
		t.Error("the sixth watch did not fit into a budget with room for it")
	}
}

// TestEditingAWatchDoesNotCountItTwice.
//
// The save posts the new schedule for a watch that is already in the list. Not
// excluding it makes every edit look like an addition, so tightening an
// interval from ten minutes to five is refused for exceeding a budget that the
// watch's own old cost was filling.
func TestEditingAWatchDoesNotCountItTwice(t *testing.T) {
	ws := []store.Watch{watch(1, 60, 6, true)} // exactly six a minute
	full := BudgetFor(6, 1, ws, 0)
	if full.Fits(6) {
		t.Fatal("the budget is not full; the fixture is wrong")
	}
	editing := BudgetFor(6, 1, ws, 1)
	if !near(editing.UsedPerMin, 0) {
		t.Errorf("UsedPerMin = %v with the edited watch excluded, want 0", editing.UsedPerMin)
	}
	if !editing.Fits(6) {
		t.Error("re-saving a watch with its own schedule was refused")
	}
}

// TestExplainShowsTheSumAndNotJustAVerdict.
//
// Spec: a schedule over the ceiling is refused «with the sum shown». «Слишком
// часто» tells a person nothing about which number to change.
func TestExplainShowsTheSumAndNotJustAVerdict(t *testing.T) {
	ws := []store.Watch{watch(1, 60, 5, true)}
	got := BudgetFor(6, 2, ws, 0).Explain(4)
	for _, want := range []string{"5", "4", "12", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain = %q; it does not carry %q", got, want)
		}
	}
}

// TestARateReadsAsARussianNumber. Every other number this product shows uses a
// comma, and «0.20 запр./мин» beside «40 000 ₽» reads as a copy-paste from
// somewhere else.
func TestARateReadsAsARussianNumber(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{0, "0"}, {1, "1"}, {0.2, "0,2"}, {6, "6"}, {12.5, "12,5"}, {0.25, "0,25"},
	}
	for _, c := range cases {
		if got := num(c.v); got != c.want {
			t.Errorf("num(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}
