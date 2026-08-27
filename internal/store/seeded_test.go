// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestAWatchStopsBeingUnseededOnceItIsSeeded.
//
// seeded_at was read in two places and written in none, so the panel said «ещё
// не засеяно» about every watch for ever — and the planner's «page two only
// while seeded_at IS NULL» was stuck open in the direction that spends
// requests.
func TestAWatchStopsBeingUnseededOnceItIsSeeded(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Watch(ctx, wid); w.SeededAt != nil {
		t.Fatalf("наблюдение засеяно до первого прохода: %v", *w.SeededAt)
	}

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	w, err := s.Watch(ctx, wid)
	if err != nil {
		t.Fatal(err)
	}
	if w.SeededAt == nil {
		t.Fatal("посевной проход прошёл, а наблюдение всё ещё не засеяно")
	}
	if *w.SeededAt != 1000 {
		t.Errorf("seeded_at = %d, ожидалось 1000", *w.SeededAt)
	}

	// A later pass must not move it. The date a watch first had a baseline is
	// not a thing that happens twice.
	if _, err := s.SavePass(ctx, aPass(wid, 2000), listingsFor(1, 2, 4)); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Watch(ctx, wid); *w.SeededAt != 1000 {
		t.Errorf("seeded_at сдвинулось на %d", *w.SeededAt)
	}
}

// TestASeedingPassWithNoCountStillSeeds.
//
// The rarest case and the hardest to notice: the count guard returns early, so
// anything written after it never runs for a pass that carried no total_count —
// which is every pass on a surface that failed to report one.
func TestASeedingPassWithNoCountStillSeeds(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	p := aPass(wid, 1000)
	p.TotalCount = nil
	if _, err := s.SavePass(ctx, p, listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Watch(ctx, wid); w.SeededAt == nil {
		t.Fatal("проход без total_count не засеял наблюдение")
	}
}
