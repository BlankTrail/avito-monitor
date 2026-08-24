// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"testing"
)

const day = int64(86400)

// now is far enough into the epoch that "a year ago" is still positive.
const testNow = int64(400 * 24 * 3600)

// TestThinSnapshotsLeavesOnePerDayPastThirtyAndOnePerWeekPastAYear.
func TestThinSnapshotsLeavesOnePerDayPastThirtyAndOnePerWeekPastAYear(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	// Four snapshots inside one day, 100 days ago: past thirty, inside a year.
	insertSnapshots(t, s, 1, testNow-100*day, 4, 3600)
	// Four inside one day, 500 days ago: past a year.
	insertSnapshots(t, s, 2, testNow-500*day, 4, 3600)
	// Four inside one day, yesterday: inside the fresh window, untouched.
	insertSnapshots(t, s, 3, testNow-day, 4, 3600)

	if _, err := s.ThinSnapshots(ctx, testNow); err != nil {
		t.Fatalf("ThinSnapshots: %v", err)
	}
	for _, c := range []struct {
		item int64
		want int
		why  string
	}{
		{1, 1, "past thirty days, one row per day"},
		{2, 1, "past a year, one row per week"},
		{3, 4, "inside the fresh window, nothing is thinned"},
	} {
		got := countRows(t, s, `SELECT count(*) FROM listing_snapshots WHERE item_id = ?`, c.item)
		if got != c.want {
			t.Errorf("item %d has %d rows, want %d (%s)", c.item, got, c.want, c.why)
		}
	}
}

// TestThinningKeepsTheChangeAndDropsTheAnchor.
//
// An anchor carries the same values as the change before it under a later,
// arbitrary timestamp. Keeping it would preserve the value and lose the moment
// it moved — which is the only thing the series is for.
func TestThinningKeepsTheChangeAndDropsTheAnchor(t *testing.T) {
	s := openTemp(t)
	old := testNow - 100*day

	insertSnapshot(t, s, 1, old, false, "fp-change")
	insertSnapshot(t, s, 1, old+3600, true, "fp-change") // an anchor, same values, later

	if _, err := s.ThinSnapshots(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	var anchor int
	if err := s.DB().QueryRow(
		`SELECT anchor FROM listing_snapshots WHERE item_id = 1`).Scan(&anchor); err != nil {
		t.Fatalf("both rows are gone: %v", err)
	}
	if anchor != 0 {
		t.Error("thinning kept the anchor and dropped the change; the anchor " +
			"carries the same values under a later timestamp, so keeping it " +
			"preserves the value and loses the moment it moved")
	}
}

// TestFailedPassesAreKeptAndHealthyOnesAreThinned is the rule that is
// deliberately not the obvious one.
//
// Keeping only passes that produced a change preferentially destroys the
// evidence that collection FAILED while preserving the evidence that it
// worked, and makes "was collection healthy in early March" unanswerable three
// weeks later.
func TestFailedPassesAreKeptAndHealthyOnesAreThinned(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	old := testNow - 60*day

	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class = "ok"
		insertBarePass(t, s, p)
	}
	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class, p.Evidential = "throttled", false
		insertBarePass(t, s, p)
	}

	if _, err := s.ThinPasses(ctx, testNow); err != nil {
		t.Fatalf("ThinPasses: %v", err)
	}
	ok := countRows(t, s, `SELECT count(*) FROM passes WHERE class = 'ok'`)
	bad := countRows(t, s, `SELECT count(*) FROM passes WHERE class = 'throttled'`)

	if ok != 1 {
		t.Errorf("healthy passes = %d past thirty days, want 1 per day", ok)
	}
	if bad != 10 {
		t.Errorf("failed passes = %d, want all 10 — the health history is the "+
			"only thing that explains what happened, and it must not be the "+
			"first thing retention destroys", bad)
	}
}

// TestEmptyIsThinnedAsHealthy. Reading the rule as "not ok" would keep every
// empty pass for ever, and a watch on a rare query would grow without bound
// precisely because nothing was happening on it.
func TestEmptyIsThinnedAsHealthy(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	old := testNow - 60*day

	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class = "empty"
		insertBarePass(t, s, p)
	}
	if _, err := s.ThinPasses(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes WHERE class = 'empty'`); n != 1 {
		t.Errorf("empty passes = %d, want 1 — a query that legitimately "+
			"returned nothing is as healthy as one that returned fifty", n)
	}
}

// TestPassesInsideTheFreshWindowAreUntouched.
func TestPassesInsideTheFreshWindowAreUntouched(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	for i := 0; i < 10; i++ {
		p := aPass(wid, testNow-3600+int64(i)*60)
		p.Class = "ok"
		insertBarePass(t, s, p)
	}
	if _, err := s.ThinPasses(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes`); n != 10 {
		t.Errorf("passes = %d inside the fresh window, want all 10", n)
	}
}

// TestSweepBodiesIsBoundedByAgeAndBySize. An archive with an age rule and no
// size rule is unbounded on a broken install, which is exactly when it grows
// fastest.
func TestSweepBodiesIsBoundedByAgeAndBySize(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	// The rule is about a ratio, not about half a gigabyte, so the bound is
	// scaled down for the test. Allocating 800 MB of fixture would measure the
	// machine and tax every future run of this package by seven seconds.
	r := DefaultRetention()
	r.BodyMaxBytes = 1 << 20
	s.SetRetention(r)

	insertBody(t, s, "old", testNow-40*day, 1<<10)
	insertBody(t, s, "recent-big-1", testNow-2*day, 800<<10)
	insertBody(t, s, "recent-big-2", testNow-day, 800<<10)
	if _, err := s.SweepBodies(ctx, testNow); err != nil {
		t.Fatalf("SweepBodies: %v", err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM bodies WHERE sha256 = 'old'`); n != 0 {
		t.Error("a body past BodyMaxAge survived")
	}
	var total int64
	if err := s.DB().QueryRow(
		`SELECT coalesce(sum(length(bytes)), 0) FROM bodies`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total > r.BodyMaxBytes {
		t.Errorf("archive is %d bytes, over the %d-byte bound", total, r.BodyMaxBytes)
	}
	// The oldest of the two big ones is what went.
	if n := countRows(t, s, `SELECT count(*) FROM bodies WHERE sha256 = 'recent-big-2'`); n != 1 {
		t.Error("the size sweep took the newer body; it must take the oldest first")
	}
}

// TestABodyDeletionKeepsThePassThatReferredToIt. passes.body_sha is nullable
// with ON DELETE SET NULL so the sweep loses the page and keeps the reading. A
// reference that can never be dropped is a garbage collector that can never
// fire.
func TestABodyDeletionKeepsThePassThatReferredToIt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	insertBody(t, s, "doomed", testNow-40*day, 1<<10)
	p := aPass(wid, testNow-40*day)
	p.Class = "throttled"
	id := insertBarePassWithBody(t, s, p, "doomed")

	if _, err := s.SweepBodies(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	var sha *string
	if err := s.DB().QueryRow(`SELECT body_sha FROM passes WHERE id = ?`, id).Scan(&sha); err != nil {
		t.Fatalf("the pass row went with its body: %v", err)
	}
	if sha != nil {
		t.Errorf("body_sha = %q after the body was deleted, want NULL", *sha)
	}
}

// --- helpers ---
//
// Direct INSERTs rather than calls to SavePass, on purpose: retention must be
// tested against rows a test placed deliberately, so a change to SavePass
// cannot silently change what retention is being tested against.

func insertSnapshot(t *testing.T, s *Store, item, ts int64, anchor bool, fp string) {
	t.Helper()
	// listing_snapshots.item_id references listings, so the parent has to be
	// there. The FK is what stops a snapshot from outliving the thing it
	// describes, and the test has no business turning it off.
	if _, err := s.DB().Exec(`
		INSERT INTO listings
		  (item_id, thread_id, state_at, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO NOTHING`, item, item, ts, ts, ts); err != nil {
		t.Fatalf("insert listing: %v", err)
	}
	_, err := s.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, anchor, fingerprint, source, path, price_kind, currency)
		VALUES (?, 'mobile', ?, ?, ?, 'search', 'dom', 'exact', 'RUB')`,
		item, ts, anchor, fp)
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
}

// insertSnapshots writes n rows for one item, step seconds apart, each with a
// distinct fingerprint so every one of them counts as a real change.
func insertSnapshots(t *testing.T, s *Store, item, start int64, n int, step int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		insertSnapshot(t, s, item, start+int64(i)*step, false,
			"fp-"+strconv.FormatInt(item, 10)+"-"+strconv.Itoa(i))
	}
}

func insertBarePass(t *testing.T, s *Store, p Pass) int64 {
	t.Helper()
	return insertBarePassWithBody(t, s, p, "")
}

func insertBarePassWithBody(t *testing.T, s *Store, p Pass, sha string) int64 {
	t.Helper()
	var body any
	if sha != "" {
		body = sha
	}
	res, err := s.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, http_status,
		   evidential, body_sha)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, ?, ?)`,
		p.WatchID, p.StartedAt, p.FinishedAt, p.Class, p.HTTPStatus,
		p.Evidential, body)
	if err != nil {
		t.Fatalf("insert pass: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// insertBody stores an uncompressed body. algo 'identity' because the size
// bound is about bytes on disk, and a test that compressed its fixture would
// be measuring the compressor rather than the rule.
func insertBody(t *testing.T, s *Store, sha string, ts int64, size int) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO bodies (sha256, algo, size_raw, bytes, first_seen_at)
		VALUES (?, 'identity', ?, ?, ?)`,
		sha, size, make([]byte, size), ts); err != nil {
		t.Fatalf("insert body: %v", err)
	}
}
