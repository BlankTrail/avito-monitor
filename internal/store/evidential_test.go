// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

// evidentialOf reads the stored column, which is what every later query about
// absence reads.
func evidentialOf(t *testing.T, s *Store, passID int64) (bool, string) {
	t.Helper()
	var ev bool
	var why string
	if err := s.DB().QueryRow(
		`SELECT evidential, degraded_why FROM passes WHERE id = ?`, passID).
		Scan(&ev, &why); err != nil {
		t.Fatal(err)
	}
	return ev, why
}

// TestEvidentialIsComputedRatherThanSupplied.
//
// Increment 3's run --once passed a floor in by hand — a class that answered —
// with a comment saying the real tests were increment 4's. They are here, and
// the store applies them, so a caller cannot mark a soft block as evidence by
// filling in a struct field.
func TestEvidentialIsComputedRatherThanSupplied(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	p := aPass(wid, 1000)
	p.Class = "soft-block"
	p.Evidential = true // a caller asserting something it does not get to decide

	res, err := s.SavePass(ctx, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev, why := evidentialOf(t, s, res.PassID)
	if ev {
		t.Error("a soft block was stored as evidential because the caller said so")
	}
	if !strings.Contains(why, "soft-block") {
		t.Errorf("degraded_why = %q; it must name what refused the pass", why)
	}
}

// TestTheFirstPassOfAWatchIsASeedAndSaysSo.
//
// A watch's first pass has no predecessor. It is not evidence that anything
// left, and the store works that out from the absence of an earlier pass rather
// than trusting a caller to set a flag.
func TestTheFirstPassOfAWatchIsASeedAndSaysSo(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	res, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	var isSeed bool
	if err := s.DB().QueryRow(`SELECT is_seed FROM passes WHERE id = ?`, res.PassID).
		Scan(&isSeed); err != nil {
		t.Fatal(err)
	}
	if !isSeed {
		t.Error("the first pass of a watch was not marked a seed")
	}
	if ev, _ := evidentialOf(t, s, res.PassID); ev {
		t.Error("a seeding pass was stored as evidential")
	}
}

// TestTheSecondPassIsEvidential. This is the circular case Admissible's gap
// gate has to avoid: after a seed there is no admissible predecessor, and a
// rule that demanded one would refuse every pass for ever.
func TestTheSecondPassIsEvidential(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	res, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}
	if ev, why := evidentialOf(t, s, res.PassID); !ev {
		t.Errorf("the second pass of a watch was refused: %s — no watch would "+
			"ever get its first admissible pass", why)
	}
}

// TestADesktopDomPassIsStoredAsNotEvidential. The store must apply the
// per-surface rule rather than a simpler one it invented.
func TestADesktopDomPassIsStoredAsNotEvidential(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	w := aWatch()
	w.Surface = "desktop"
	wid, err := s.AddWatch(ctx, w)
	if err != nil {
		t.Fatal(err)
	}

	first := aPass(wid, 1000)
	first.Surface, first.Path = "desktop", "state"
	if _, err := s.SavePass(ctx, first, listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	fell := aPass(wid, 1300)
	fell.Surface, fell.Path = "desktop", "dom"
	res, err := s.SavePass(ctx, fell, listingsFor(1))
	if err != nil {
		t.Fatal(err)
	}
	if ev, _ := evidentialOf(t, s, res.PassID); ev {
		t.Error("a desktop pass answered by the DOM fallback was stored as " +
			"evidential; that fall back is the bot-classification warning")
	}
}

// TestAnIdentityChangeIsStillEvidentialButNotComparable.
//
// The two questions are separate and the store keeps them separate. Comparable
// is a property of a PAIR of passes, so it is deliberately not a column: a
// column on one row would be answering a question about two.
func TestAnIdentityChangeIsStillEvidentialButNotComparable(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	first := aPass(wid, 1000)
	first.Session = "identity-A"
	if _, err := s.SavePass(ctx, first, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	renewed := aPass(wid, 1300)
	renewed.Session = "identity-B"
	res, err := s.SavePass(ctx, renewed, listingsFor(7, 8, 9))
	if err != nil {
		t.Fatal(err)
	}
	if ev, why := evidentialOf(t, s, res.PassID); !ev {
		t.Errorf("a clean pass was refused because its predecessor used another "+
			"identity: %s — that is Comparable's question, not this one", why)
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes WHERE evidential = 1`); n != 1 {
		t.Errorf("evidential passes = %d, want 1 — the seed is not one", n)
	}
}

// TestOnlyAnEvidentialPassMovesLastTotalCount.
//
// The collapse test compares against the last count we believed. Letting a
// blocked pass write it would move the baseline to whatever the block returned,
// and the next real pass would then look like a recovery rather than a reading.
func TestOnlyAnEvidentialPassMovesLastTotalCount(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	good := aPass(wid, 1000)
	good.TotalCount = ptrInt64(40000)
	if _, err := s.SavePass(ctx, good, listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	good2 := aPass(wid, 1300)
	good2.TotalCount = ptrInt64(39000)
	if _, err := s.SavePass(ctx, good2, listingsFor(1)); err != nil {
		t.Fatal(err)
	}

	blocked := aPass(wid, 1600)
	blocked.Class, blocked.TotalCount = "soft-block", ptrInt64(0)
	if _, err := s.SavePass(ctx, blocked, nil); err != nil {
		t.Fatal(err)
	}

	var last *int64
	if err := s.DB().QueryRow(
		`SELECT last_total_count FROM watches WHERE id = ?`, wid).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last == nil || *last != 39000 {
		t.Errorf("last_total_count = %v, want 39000 — a blocked pass must not "+
			"move the baseline the collapse test compares against", last)
	}
}

func ptrInt64(v int64) *int64 { return &v }

// TestAWatchOverTheCapIsFlagged.
//
// Avito pages through at most five thousand results. Above that, page one is
// the top of a firehose and «new listing» is a guess rather than a set
// difference — so the watch is flagged and the panel says to add a price band.
func TestAWatchOverTheCapIsFlagged(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "всё подряд", 300)

	huge := int64(59959)
	p := Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "dom", Mode: "direct",
		TotalCount: &huge,
	}
	if _, err := s.SavePass(ctx, p, listingsFor(100)); err != nil {
		t.Fatal(err)
	}
	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !w.TooBroad {
		t.Error("a watch over the cap was not flagged")
	}
}

// TestAMissingCountNeverFlagsAnything.
//
// The column is NULL when a surface reported no count at all, and a missing
// number must never become arithmetic on zero — the same rule the collapse test
// keeps.
func TestAMissingCountNeverFlagsAnything(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	p := Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "dom", Mode: "direct",
	}
	if _, err := s.SavePass(ctx, p, listingsFor(100)); err != nil {
		t.Fatal(err)
	}
	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if w.TooBroad {
		t.Error("a pass with no count flagged the watch")
	}
}

// TestANarrowWatchStaysUnflagged.
func TestANarrowWatchStaysUnflagged(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "узкое", 300)

	small := int64(120)
	p := Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "dom", Mode: "direct",
		TotalCount: &small,
	}
	if _, err := s.SavePass(ctx, p, listingsFor(100)); err != nil {
		t.Fatal(err)
	}
	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if w.TooBroad {
		t.Error("a narrow watch was flagged")
	}
}
