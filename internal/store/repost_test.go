// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/repost"
)

const thirtyDays int64 = 30 * 24 * 60 * 60

// aStoredListing writes one listing directly, with the fields the linker reads.
func aStoredListing(t *testing.T, s *Store, itemID int64, key string, firstSeen, lastSeen int64, state string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title, title_norm,
		                      repost_key, category_root_id, state, state_at,
		                      first_seen_at, last_seen_at)
		VALUES (?, ?, '/m/x', 'iPhone 15, 128 ГБ', 'iphone 15 128 гб',
		        ?, 9, ?, ?, ?, ?)`,
		itemID, itemID, key, state, lastSeen, firstSeen, lastSeen); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, s *Store, predecessor, successor int64, state string) {
	t.Helper()
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.SaveRepostLink(context.Background(), tx, RepostLinkRow{
		SuccessorID: successor, PredecessorID: predecessor,
		CreatedAt: 5000, Basis: "strong", State: state, DecidedBy: "auto",
		Candidates: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func threadOf(t *testing.T, s *Store, itemID int64) int64 {
	t.Helper()
	var thread int64
	if err := s.DB().QueryRow(
		`SELECT thread_id FROM listings WHERE item_id = ?`, itemID).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	return thread
}

// TestAChainOfThreeCollapsesToOneThread, keyed on the oldest listing's own id.
//
// Seeded to item_id at insert, so linking is a repoint rather than a backfill —
// and taking the oldest means a chain has the same thread whichever end it is
// rebuilt from.
func TestAChainOfThreeCollapsesToOneThread(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "vanished")
	aStoredListing(t, s, 300, "k", 5000, 6000, "active")
	link(t, s, 100, 200, "linked")
	link(t, s, 200, 300, "linked")

	if err := s.RecomputeThread(ctx, 300); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{100, 200, 300} {
		if got := threadOf(t, s, id); got != 100 {
			t.Errorf("listing %d is in thread %d, want 100", id, got)
		}
	}

	// And from the other end, the same answer.
	if err := s.RecomputeThread(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if got := threadOf(t, s, 300); got != 100 {
		t.Errorf("rebuilt from the other end: thread %d", got)
	}
}

// TestSplittingALinkRebuildsBothHalves.
//
// The property that makes a wrong link repairable rather than permanent, which
// is the whole reason thread_id is allowed to be a written derived value.
func TestSplittingALinkRebuildsBothHalves(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "vanished")
	aStoredListing(t, s, 300, "k", 5000, 6000, "active")
	link(t, s, 100, 200, "linked")
	link(t, s, 200, 300, "linked")
	if err := s.RecomputeThread(ctx, 300); err != nil {
		t.Fatal(err)
	}

	if err := s.PinPair(ctx, 200, 300, 7000); err != nil {
		t.Fatal(err)
	}
	if got := threadOf(t, s, 100); got != 100 {
		t.Errorf("the older half moved to %d", got)
	}
	if got := threadOf(t, s, 200); got != 100 {
		t.Errorf("the older half's second member moved to %d", got)
	}
	if got := threadOf(t, s, 300); got != 300 {
		t.Errorf("the split-off listing is in thread %d, want its own 300", got)
	}
}

// TestAPinIsSymmetric.
//
// The pair is, so the pin must be. Otherwise the same correction has to be made
// twice, and the second time is after the wrong link has come back.
func TestAPinIsSymmetric(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "active")
	if err := s.PinPair(ctx, 100, 200, 5000); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, c := range [][2]int64{{100, 200}, {200, 100}} {
		pinned, err := s.IsPinned(ctx, tx, c[0], c[1])
		if err != nil {
			t.Fatal(err)
		}
		if !pinned {
			t.Errorf("(%d, %d) is not pinned", c[0], c[1])
		}
	}
}

// TestACycleTerminates.
//
// A cycle cannot arise from the linker — the co-existence gate makes edges
// strictly forward in time — but it can arise from a future bug, and a query
// that hangs inside a pass transaction is a collection that stops. UNION rather
// than UNION ALL is what ends it; the depth cap is the second guard.
func TestACycleTerminates(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "vanished")
	link(t, s, 100, 200, "linked")
	link(t, s, 200, 100, "linked") // the impossible edge, written by hand

	done := make(chan error, 1)
	go func() { done <- s.RecomputeThread(ctx, 100) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RecomputeThread: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RecomputeThread did not return on a cycle")
	}
	if got := threadOf(t, s, 200); got != 100 {
		t.Errorf("thread = %d", got)
	}
}

// TestOnlyLinkedEdgesJoinAThread.
//
// A proposal withholds nothing and must join nothing: a person looking at a
// price chart of a proposed thread would be shown two listings' prices as one
// series on the strength of a guess.
func TestOnlyLinkedEdgesJoinAThread(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "active")
	link(t, s, 100, 200, "proposed")

	if err := s.RecomputeThread(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if got := threadOf(t, s, 200); got != 200 {
		t.Errorf("a proposal joined a thread: %d", got)
	}
}

// TestCandidatesComeFromTheKeyAndExcludeTheListingItself.
func TestCandidatesComeFromTheKeyAndExcludeTheListingItself(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "other", 1000, 2000, "vanished")
	aStoredListing(t, s, 300, "k", 5000, 6000, "active")

	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	arrival, err := s.RepostListing(ctx, tx, 300)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.RepostCandidates(ctx, tx, arrival, thirtyDays)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ItemID != 100 {
		t.Errorf("candidates = %+v, want only 100", got)
	}
}

// TestAListingWithNoKeyFindsNothing.
//
// The whole mobile-surface population, and the right answer for it: a key over
// a bare title puts fifty listings in seven buckets, measured, and the seek
// would return them all for the gates to throw away one at a time.
func TestAListingWithNoKeyFindsNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "", 1000, 2000, "vanished")
	aStoredListing(t, s, 300, "", 5000, 6000, "active")

	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	arrival, err := s.RepostListing(ctx, tx, 300)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.RepostCandidates(ctx, tx, arrival, thirtyDays)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a keyless listing found %d candidates", len(got))
	}
}

// TestACandidateOutsideTheWindowIsNotEvenRead.
//
// The gates would refuse it anyway; reading it would mean pulling a seller's
// whole two-year history out of the database on every arrival to throw it away.
func TestACandidateOutsideTheWindowIsNotEvenRead(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 300, "k", 2000+thirtyDays+10, 2000+thirtyDays+10, "active")

	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	arrival, err := s.RepostListing(ctx, tx, 300)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.RepostCandidates(ctx, tx, arrival, thirtyDays)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a candidate past the window was read: %+v", got)
	}
}

// TestADecisionIsKeptWhateverItWas.
//
// Including the refusals. «Почему мне не сказали про это объявление» has to be
// answerable for the pairs that were considered and rejected too.
func TestADecisionIsKeptWhateverItWas(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	aStoredListing(t, s, 100, "k", 1000, 2000, "vanished")
	aStoredListing(t, s, 200, "k", 3000, 4000, "active")

	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRepostLink(ctx, tx, RepostLinkRow{
		SuccessorID: 200, PredecessorID: 100, CreatedAt: 5000,
		State: "rejected", DecidedBy: "auto", Candidates: 3, GapSec: 1000,
		Evidence: []repost.Term{{Name: "Ворота", Detail: "разные продавцы"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	rows, err := s.RepostLinks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}
	got := rows[0]
	if got.State != "rejected" || got.Candidates != 3 {
		t.Errorf("row = %+v", got)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Detail != "разные продавцы" {
		t.Errorf("the evidence was lost: %+v", got.Evidence)
	}
}
