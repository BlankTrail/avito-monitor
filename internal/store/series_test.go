// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// TestTheSeriesProducerTurnsTwoSnapshotsIntoOnePriceChange.
//
// It runs out of band rather than inside the pass transaction, because a price
// move is a property of two snapshots and not of one window, and holding the
// single write lock while diffing every listing on the page would make the pass
// as slow as the market is busy — on a target where the pass has a budget.
func TestTheSeriesProducerTurnsTwoSnapshotsIntoOnePriceChange(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	moved := listingsFor(1)
	moved[0].Price.Minor = 3500000
	moved[0].PriceLabel = "35 000 ₽"
	if _, err := s.SavePass(ctx, aPass(wid, 1300), moved); err != nil {
		t.Fatal(err)
	}

	n, err := s.ProduceSeries(ctx)
	if err != nil {
		t.Fatalf("ProduceSeries: %v", err)
	}
	if n != 1 {
		t.Fatalf("ProduceSeries wrote %d changes, want 1: %v", n, kindsFor(t, s, wid))
	}

	var found *ChangeRow
	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if rows[i].Kind == track.KindPriceChanged {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("no price-changed row: %v", kindsFor(t, s, wid))
	}
	if found.Was != "4000000" || found.Now != "3500000" {
		t.Errorf("was/now = %q/%q, want the minor units", found.Was, found.Now)
	}
	if found.Producer != "series" {
		t.Errorf("producer = %q, want series", found.Producer)
	}
	if found.TS != 1300 {
		t.Errorf("TS = %d, want the later snapshot's — a change is dated by when "+
			"it was observed", found.TS)
	}
}

// TestProduceSeriesIsIdempotent. It runs on a schedule, and a second run over
// the same snapshots must not write the same change twice.
func TestProduceSeriesIsIdempotent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	moved := listingsFor(1)
	moved[0].Price.Minor = 3500000
	if _, err := s.SavePass(ctx, aPass(wid, 1300), moved); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := s.ProduceSeries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a second run wrote %d changes; the producer must remember "+
			"where it got to", n)
	}
}

// TestASnapshotPairSpanningAnAbsentReadingProducesNothing.
//
// Our own blindness is not the market moving. track.Diff already refuses; this
// asserts the store does not work around it by filling the gap with a zero.
func TestASnapshotPairSpanningAnAbsentReadingProducesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	gap := listingsFor(1)
	gap[0].PriceKind = avito.PriceAbsent
	gap[0].Price = avito.Money{}
	gap[0].PriceLabel = ""
	if _, err := s.SavePass(ctx, aPass(wid, 1300), gap); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range kindsFor(t, s, wid) {
		if k == track.KindPriceWithdrawn || k == track.KindPriceChanged {
			t.Errorf("a reading we failed to parse produced %q", k)
		}
	}
}

// TestANegotiablePriceIsAnEventAndAnUnreadableOneIsNot, end to end through the
// store. The two look identical in the price column and are opposite facts.
func TestANegotiablePriceIsAnEventAndAnUnreadableOneIsNot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	nego := listingsFor(1)
	nego[0].PriceKind = avito.PriceNegotiable
	nego[0].Price = avito.Money{}
	nego[0].PriceLabel = "Цена договорная"
	if _, err := s.SavePass(ctx, aPass(wid, 1300), nego); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindPriceWithdrawn); n != 1 {
		t.Errorf("price-withdrawn rows = %d, want 1 — the seller said so", n)
	}
}

// TestSeriesRefusesToCompareAcrossSurfaces. A desktop snapshot and a mobile one
// are readings by two different extractors, not two moments of one series.
func TestSeriesRefusesToCompareAcrossSurfaces(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	desktop := aPass(wid, 1300)
	desktop.Surface, desktop.Path = "desktop", "state"
	moved := listingsFor(1)
	moved[0].Price.Minor = 3500000
	if _, err := s.SavePass(ctx, desktop, moved); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindPriceChanged); n != 0 {
		t.Errorf("price-changed rows = %d across two surfaces; that is a "+
			"difference between two extractors, not between two moments", n)
	}
}

// TestSaturationSaysWhetherTheIntervalIsShortEnough.
//
// items_on_page / arrivals. If a watch of thirty slots takes thirty new
// listings every pass, the window turned over completely between polls and the
// watch is blind to whatever passed through in between — it is sampling the
// market rather than measuring it.
func TestSaturationSaysWhetherTheIntervalIsShortEnough(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// Four passes, each turning over completely. Four and not three: the seed
	// is not admissible, and Saturation reads admissible passes only — a seed
	// reports every listing as an arrival and would read as total turnover
	// whatever the market did.
	for i, ids := range [][]int64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 11, 12}} {
		at := 1000 + int64(i)*300
		if _, err := s.SavePass(ctx, aPass(wid, at), listingsFor(ids...)); err != nil {
			t.Fatal(err)
		}
	}

	ratio, ok, err := s.Saturation(ctx, wid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Saturation reported no answer with three passes to read")
	}
	if ratio < 0.99 {
		t.Errorf("saturation = %v, want about 1.0 — every pass turned over "+
			"completely", ratio)
	}
}

// TestAQuietWatchHasLowSaturation, which is the healthy state: the window
// barely moves between polls, so nothing goes through unseen.
func TestAQuietWatchHasLowSaturation(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	for i := 0; i < 4; i++ {
		at := 1000 + int64(i)*300
		if _, err := s.SavePass(ctx, aPass(wid, at), listingsFor(1, 2, 3)); err != nil {
			t.Fatal(err)
		}
	}
	ratio, ok, err := s.Saturation(ctx, wid, 3)
	if err != nil || !ok {
		t.Fatalf("Saturation: %v ok=%v", err, ok)
	}
	// The seed's arrivals are excluded with it, so this reads three passes in
	// which nothing arrived at all.
	if ratio > 0.4 {
		t.Errorf("saturation = %v on a watch where nothing moved after the "+
			"seed, want well under half", ratio)
	}
}

// TestSaturationRefusesToAnswerWithoutEnoughPasses. A ratio over one pass is
// noise, and a number a person acts on must not be one.
func TestSaturationRefusesToAnswerWithoutEnoughPasses(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.Saturation(ctx, wid, 3); err != nil || ok {
		t.Errorf("Saturation answered from one pass: ok=%v err=%v", ok, err)
	}
}
