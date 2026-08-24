// SPDX-License-Identifier: AGPL-3.0-or-later

package track

import (
	"errors"
	"testing"
)

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }
func boolp(v bool) *bool   { return &v }

func reading(ts int64) Reading {
	return Reading{
		ItemID: 7800000001, Surface: "mobile", TS: ts,
		PriceKind: "exact", PriceMinor: i64(4000000), Currency: "RUB",
		Title: str("iPhone 15, 128 ГБ"),
	}
}

func kindsOf(cs []Change) []Kind {
	out := make([]Kind, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Kind)
	}
	return out
}

func TestDiffReportsAPriceMove(t *testing.T) {
	before := reading(1000)
	after := reading(1300)
	after.PriceMinor = i64(3500000)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindPriceChanged {
		t.Fatalf("kinds = %v, want one price-changed", kindsOf(got))
	}
	c := got[0]
	if c.Was != "4000000" || c.Now != "3500000" {
		t.Errorf("was/now = %q/%q, want the minor units", c.Was, c.Now)
	}
	if c.Unit != UnitMinor || c.Currency != "RUB" {
		t.Errorf("unit=%q currency=%q; a number without its unit is not a price",
			c.Unit, c.Currency)
	}
	if c.TS != 1300 {
		t.Errorf("TS = %d, want the LATER reading's — a change is dated by when "+
			"it was observed", c.TS)
	}
}

func TestDiffSaysNothingWhenNothingMoved(t *testing.T) {
	got, err := Diff(reading(1000), reading(1300))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none", kindsOf(got))
	}
}

// TestTheThreePriceKindsAreThreeDifferentStatements is the whole reason
// PriceKind is on the reading and not inferred from a nil.
//
// «Цена договорная» is the seller saying something and is worth an event. A
// parse gap is our defect and is worth none. Both give a nil price, and a diff
// that could not tell them apart would report "this became negotiable" for
// thirty listings the first time Avito withheld the hydration state.
func TestTheThreePriceKindsAreThreeDifferentStatements(t *testing.T) {
	exact := reading(1000)

	negotiable := reading(1300)
	negotiable.PriceKind, negotiable.PriceMinor = "negotiable", nil

	absent := reading(1300)
	absent.PriceKind, absent.PriceMinor = "absent", nil

	t.Run("exact to negotiable is an event", func(t *testing.T) {
		got, err := Diff(exact, negotiable)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != KindPriceWithdrawn {
			t.Fatalf("kinds = %v, want one price-withdrawn", kindsOf(got))
		}
		if got[0].HasNow {
			t.Error("HasNow is true for a withdrawn price")
		}
	})

	t.Run("negotiable to exact is an event", func(t *testing.T) {
		got, err := Diff(negotiable, exact)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != KindPriceAppeared {
			t.Fatalf("kinds = %v, want one price-appeared", kindsOf(got))
		}
	})

	t.Run("anything to absent is silence", func(t *testing.T) {
		got, err := Diff(exact, absent)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("kinds = %v, want none — we stopped reading the price, and "+
				"our own blindness is not the market moving", kindsOf(got))
		}
	})

	t.Run("absent to anything is silence", func(t *testing.T) {
		got, err := Diff(absent, exact)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("kinds = %v, want none — we could not see the old price, so "+
				"there is nothing to compare against", kindsOf(got))
		}
	})
}

func TestDiffRefusesTwoDifferentListings(t *testing.T) {
	other := reading(1300)
	other.ItemID = 999
	if _, err := Diff(reading(1000), other); !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

// TestDiffRefusesTwoSurfaces. A desktop reading and a mobile one are not two
// samples of one series: they carry different fields, and mobile has no
// description at all.
func TestDiffRefusesTwoSurfaces(t *testing.T) {
	other := reading(1300)
	other.Surface = "desktop"
	if _, err := Diff(reading(1000), other); !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

func TestDiffReportsATitleEdit(t *testing.T) {
	after := reading(1300)
	after.Title = str("iPhone 15, 256 ГБ")

	got, err := Diff(reading(1000), after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindTitleChanged {
		t.Fatalf("kinds = %v, want one title-changed", kindsOf(got))
	}
	if got[0].Unit != UnitText {
		t.Errorf("unit = %q, want text", got[0].Unit)
	}
}

// TestABumpedSortTimeIsItsOwnKind. sortTimeStamp is a bump time and not a
// creation time, so a jump in it with nothing else moving means the seller paid
// to resurface — worth telling somebody about, and emphatically not a new
// listing.
func TestABumpedSortTimeIsItsOwnKind(t *testing.T) {
	before := reading(1000)
	before.SortTSMs = i64(1_700_000_000_000)
	after := reading(1300)
	after.SortTSMs = i64(1_700_000_600_000)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindListingBumped {
		t.Fatalf("kinds = %v, want one listing-bumped", kindsOf(got))
	}
}

// TestAPromotionIsItsOwnKindAndNotAPriceMove. The seller paid for a larger
// card; nothing about what they are selling changed.
func TestAPromotionIsItsOwnKindAndNotAPriceMove(t *testing.T) {
	before := reading(1000)
	before.IsXL = boolp(false)
	after := reading(1300)
	after.IsXL = boolp(true)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindPromotionChanged {
		t.Fatalf("kinds = %v, want one promotion-changed", kindsOf(got))
	}
}

// TestAnUnreadableBooleanIsSilence. The microdata path cannot see these at all,
// and a nil is a silence rather than a false — otherwise every fall back to
// that path would report thirty listings un-promoting themselves.
func TestAnUnreadableBooleanIsSilence(t *testing.T) {
	before := reading(1000)
	before.IsReserved = boolp(true)
	after := reading(1300) // IsReserved stays nil: we could not read it

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none — we stopped being able to read it, "+
			"which is not the seller changing it", kindsOf(got))
	}
}

// TestDiffIsOrderedTheSameWayEveryTime. These rows are written into a table and
// read back by a person: two runs over the same pair must produce the same
// sequence, and a map iteration would not.
func TestDiffIsOrderedTheSameWayEveryTime(t *testing.T) {
	before := reading(1000)
	before.DiscountPct, before.SortTSMs, before.IsXL = i64(10), i64(1), boolp(false)
	after := reading(1300)
	after.PriceMinor, after.DiscountPct = i64(3500000), i64(20)
	after.Title, after.SortTSMs, after.IsXL = str("другое"), i64(2), boolp(true)

	first, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 4 {
		t.Fatalf("only %d changes; this fixture moves five fields", len(first))
	}
	for i := 0; i < 20; i++ {
		again, err := Diff(before, after)
		if err != nil {
			t.Fatal(err)
		}
		for j := range first {
			if again[j].Kind != first[j].Kind {
				t.Fatalf("run %d differs at %d: %q vs %q", i, j, again[j].Kind, first[j].Kind)
			}
		}
	}
}

// TestPercentChangeRefusesAnAppearance. A rise from nothing is not a
// percentage, and reporting one as infinite or as zero are both lies a
// threshold rule would then act on.
func TestPercentChangeRefusesAnAppearance(t *testing.T) {
	if _, ok := PercentChange(0, 100); ok {
		t.Error("PercentChange from zero reported a percentage")
	}
	pct, ok := PercentChange(4000000, 3500000)
	if !ok {
		t.Fatal("PercentChange refused an ordinary move")
	}
	if pct > -12.4 || pct < -12.6 {
		t.Errorf("PercentChange = %v, want about -12.5", pct)
	}
}
