// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aPass(watchID int64, at int64) Pass {
	return Pass{
		WatchID: watchID, StartedAt: at, FinishedAt: at + 2,
		Page: 1, Surface: avito.SurfaceMobile, Class: "ok",
		HTTPStatus: 200, Path: "dom", Mode: "direct",
		Session: "chrome-133-android#0/abc", Egress: "direct",
		ElapsedMS: 2000, Bytes: 1170000, Evidential: true,
	}
}

// listingsFor builds a result set in rank order. Rank is the slice index and
// not a field: avito.Listing deliberately has none, because a listing's rank
// belongs to the watch that saw it and not to the listing — the same reason
// Fingerprint leaves position out.
func listingsFor(ids ...int64) []avito.Listing {
	out := make([]avito.Listing, 0, len(ids))
	for _, id := range ids {
		l := aListing()
		l.ItemID = id
		out = append(out, l)
	}
	return out
}

func countRows(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSavePassWritesThePassItsItemsAndItsListings(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))
	if err != nil {
		t.Fatalf("SavePass: %v", err)
	}
	if res.PassID == 0 {
		t.Fatal("SavePass returned no pass id")
	}
	for _, c := range []struct {
		what string
		got  int
		want int
	}{
		{"passes", countRows(t, s, `SELECT count(*) FROM passes WHERE id = ?`, res.PassID), 1},
		{"pass_items", countRows(t, s, `SELECT count(*) FROM pass_items WHERE pass_id = ?`, res.PassID), 3},
		{"listings", countRows(t, s, `SELECT count(*) FROM listings`), 3},
		{"listing_snapshots", countRows(t, s, `SELECT count(*) FROM listing_snapshots`), 3},
		{"watch_members", countRows(t, s, `SELECT count(*) FROM watch_members WHERE watch_id = ?`, wid), 3},
	} {
		if c.got != c.want {
			t.Errorf("%s rows = %d, want %d", c.what, c.got, c.want)
		}
	}
	if res.Arrivals != 3 {
		t.Errorf("Arrivals = %d, want 3 on a first pass", res.Arrivals)
	}

	// Rank is the slice index, one-based, and it must reach the row.
	var pos int
	if err := s.DB().QueryRow(
		`SELECT position FROM pass_items WHERE pass_id = ? AND item_id = 3`,
		res.PassID).Scan(&pos); err != nil {
		t.Fatal(err)
	}
	if pos != 3 {
		t.Errorf("position of the third listing = %d, want 3", pos)
	}
}

// TestASecondIdenticalPassWritesNoSnapshots is the whole volume argument. If
// this test ever fails, the file grows linearly in the number of passes and the
// product becomes one somebody deletes.
func TestASecondIdenticalPassWritesNoSnapshots(t *testing.T) {
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
	if n := countRows(t, s, `SELECT count(*) FROM listing_snapshots`); n != 3 {
		t.Errorf("listing_snapshots = %d after two identical passes, want 3 — "+
			"a row is written only when the fingerprint moves", n)
	}
	if res.Arrivals != 0 {
		t.Errorf("Arrivals = %d on an unchanged pass, want 0", res.Arrivals)
	}
	if res.Snapshots != 0 {
		t.Errorf("Snapshots = %d, want 0", res.Snapshots)
	}
}

// TestAPriceChangeWritesExactlyOneSnapshot.
func TestAPriceChangeWritesExactlyOneSnapshot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	moved := listingsFor(1, 2, 3)
	moved[1].Price.Minor = 3500000
	moved[1].PriceLabel = "35 000 ₽"

	res, err := s.SavePass(ctx, aPass(wid, 1300), moved)
	if err != nil {
		t.Fatal(err)
	}
	if res.Snapshots != 1 {
		t.Errorf("Snapshots = %d, want exactly 1", res.Snapshots)
	}
	if n := countRows(t, s, `SELECT count(*) FROM listing_snapshots WHERE item_id = 2`); n != 2 {
		t.Errorf("item 2 has %d snapshots, want 2 — the reading and the change", n)
	}
}

// TestSavePassIsAtomic. A pass whose pass_items are missing reads, to
// increment 4, as every listing having vanished. So a failure must leave
// nothing rather than half of it.
func TestSavePassIsAtomic(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	// Watch 999 does not exist, so the passes insert violates its foreign key.
	if _, err := s.SavePass(ctx, aPass(999, 1000), listingsFor(1, 2, 3)); err == nil {
		t.Fatal("SavePass accepted a pass for a watch that does not exist")
	}
	for _, table := range []string{"passes", "pass_items", "listings", "listing_snapshots", "watch_members"} {
		if n := countRows(t, s, `SELECT count(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows after a failed SavePass, want 0", table, n)
		}
	}
}

// TestWatchMembersTracksPositionAndAbsence. The counter is per watch and not
// per listing: a listing belongs to several watches and absence from one is not
// absence from the listing. One counter for both is the shortest path to
// announcing removals that never happened.
func TestWatchMembersTracksPositionAndAbsence(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(1, 3)); err != nil { // 2 is gone
		t.Fatal(err)
	}

	var streak, lastPos, present int
	var missingSince *int64
	err := s.DB().QueryRowContext(ctx, `
		SELECT missing_streak, last_position, present, missing_since
		FROM watch_members WHERE watch_id = ? AND item_id = 2`, wid).
		Scan(&streak, &lastPos, &present, &missingSince)
	if err != nil {
		t.Fatalf("the member row for the absent listing is gone; it is the "+
			"evidence behind every disappearance: %v", err)
	}
	if streak != 1 {
		t.Errorf("missing_streak = %d, want 1", streak)
	}
	if lastPos != 2 {
		t.Errorf("last_position = %d, want 2 — the rank it was last seen at is "+
			"what makes displacement computable", lastPos)
	}
	if present != 0 {
		t.Errorf("present = %d, want 0", present)
	}
	if missingSince == nil || *missingSince != 1300 {
		t.Errorf("missing_since = %v, want the pass that first missed it; the "+
			"absence gate needs an elapsed span and not only a count", missingSince)
	}

	// A listing that came back resets the streak and the span.
	if _, err := s.SavePass(ctx, aPass(wid, 1600), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	err = s.DB().QueryRowContext(ctx, `
		SELECT missing_streak, present, missing_since
		FROM watch_members WHERE watch_id = ? AND item_id = 2`, wid).
		Scan(&streak, &present, &missingSince)
	if err != nil {
		t.Fatal(err)
	}
	if streak != 0 || present != 1 || missingSince != nil {
		t.Errorf("after the listing came back: streak=%d present=%d since=%v; "+
			"want 0, 1, nil", streak, present, missingSince)
	}
}

// TestANonEvidentialPassNeverAdvancesAnAbsenceCounter.
//
// A soft block or a mangled body is our failure to read, not the market's
// decision to remove. Letting it count is exactly how a monitor announces
// removals that never happened.
func TestANonEvidentialPassNeverAdvancesAnAbsenceCounter(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	blocked := aPass(wid, 1300)
	blocked.Class, blocked.Evidential = "soft-block", false
	if _, err := s.SavePass(ctx, blocked, nil); err != nil {
		t.Fatal(err)
	}

	var maxStreak int
	if err := s.DB().QueryRow(
		`SELECT coalesce(max(missing_streak), 0) FROM watch_members WHERE watch_id = ?`,
		wid).Scan(&maxStreak); err != nil {
		t.Fatal(err)
	}
	if maxStreak != 0 {
		t.Errorf("a soft block advanced an absence counter to %d; it is our "+
			"failure to read, not the market removing anything", maxStreak)
	}
}

// TestAnAnchorRowIsWrittenWhenOneIsDue.
func TestAnAnchorRowIsWrittenWhenOneIsDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	// A day and a second later, with nothing changed.
	res, err := s.SavePass(ctx, aPass(wid, 1000+86401), listingsFor(1))
	if err != nil {
		t.Fatal(err)
	}
	if res.Snapshots != 1 {
		t.Fatalf("Snapshots = %d a day later with nothing changed, want 1 anchor",
			res.Snapshots)
	}
	var anchor int
	if err := s.DB().QueryRow(
		`SELECT anchor FROM listing_snapshots WHERE item_id = 1 ORDER BY ts DESC LIMIT 1`).
		Scan(&anchor); err != nil {
		t.Fatal(err)
	}
	if anchor != 1 {
		t.Error("the row written a day later is not marked as an anchor; retention " +
			"orders anchors after real changes, and an unmarked one would " +
			"survive a period at the expense of the change it duplicates")
	}
}

// TestFirstSeenAtIsNeverRewritten. A listing's first sighting is the anchor of
// its whole lifecycle, and an upsert that refreshed it would make every listing
// look new on every pass.
func TestFirstSeenAtIsNeverRewritten(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 9000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	var first, last int64
	if err := s.DB().QueryRow(
		`SELECT first_seen_at, last_seen_at FROM listings WHERE item_id = 1`).
		Scan(&first, &last); err != nil {
		t.Fatal(err)
	}
	if first != 1000 {
		t.Errorf("first_seen_at = %d, want 1000 — it is written once", first)
	}
	if last != 9000 {
		t.Errorf("last_seen_at = %d, want 9000", last)
	}
}
