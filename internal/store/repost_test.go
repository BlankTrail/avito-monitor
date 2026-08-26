// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/repost"
	"github.com/BlankTrail/avito-monitor/internal/track"
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

// aSeller writes one seller and returns its id.
func aSeller(t *testing.T, s *Store, key string) int64 {
	t.Helper()
	res, err := s.DB().Exec(`
		INSERT INTO sellers (seller_key, key_quality, first_seen_at, last_seen_at)
		VALUES (?, 'slug', 1000, 1000)`, key)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// aLinkable is a listing carrying everything the strong rung needs.
func aLinkable(itemID int64, title string) avito.Listing {
	l := aListing()
	l.ItemID = itemID
	l.Title = title
	l.SellerKey, l.SellerKeyQuality = "brand:one", "slug"
	l.MicroCategoryID, l.CategoryRootID, l.LocationID = 1104365, 9, 637640
	l.Description = "Продаю телефон, состояние отличное."
	l.ImageKey = "https://90.img.avito.st/image/1/1.ureJN7aAFl"
	return l
}

func kindsOf(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT kind FROM changes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

// TestASellerWhoRepublishesProducesARepostAndNotANewListing.
//
// The increment's own demonstrable, as a test. A repost announced as a new
// listing is the noise that gets a monitor switched off in its first week.
func TestASellerWhoRepublishesProducesARepostAndNotANewListing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	// The first incarnation, seen twice so the watch has a comparable pass
	// behind it — the passes below must be readings and not re-seeds, because
	// the linker is deliberately off on a re-seed.
	first := aLinkable(100, "iPhone 15, 128 ГБ")
	for _, at := range []int64{1000, 1200} {
		if _, err := s.SavePass(ctx, aDesktopPass(id, at),
			[]avito.Listing{first}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`
		UPDATE listings SET state = 'vanished', state_at = 1300, last_seen_at = 1300
		WHERE item_id = 100`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DELETE FROM changes`); err != nil {
		t.Fatal(err)
	}

	// The same advertisement under a new number, shortly after.
	second := aLinkable(200, "iPhone 15, 128 ГБ")
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1400),
		[]avito.Listing{second}); err != nil {
		t.Fatal(err)
	}

	kinds := kindsOf(t, s)
	for _, k := range kinds {
		if k == string(track.KindListingAppeared) {
			t.Errorf("a repost was announced as a new listing: %v", kinds)
		}
	}
	found := false
	for _, k := range kinds {
		if k == string(track.KindListingReposted) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no listing-reposted change: %v", kinds)
	}

	// And the two are one thread, so a price chart is one line.
	if got := threadOf(t, s, 200); got != 100 {
		t.Errorf("the successor is in thread %d, want the predecessor's 100", got)
	}
	// And the notifier can find the predecessor, which is what makes
	// suppress.Decide answer ReasonRepost.
	predecessor, err := s.RepostOf(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}
	if predecessor != 100 {
		t.Errorf("RepostOf = %d, want 100", predecessor)
	}
}

// TestADealerWithIdenticalLivePhonesProducesNoLinks.
//
// The measured common case: one ordinary page carried four pairs sharing a full
// repost key with both members alive at once. Every one of them must produce an
// ordinary new-listing alert.
func TestADealerWithIdenticalLivePhonesProducesNoLinks(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	var stock []avito.Listing
	for i := 0; i < 8; i++ {
		stock = append(stock, aLinkable(int64(100+i), "iPhone 15, 128 ГБ"))
	}
	for _, at := range []int64{1000, 1200} {
		if _, err := s.SavePass(ctx, aDesktopPass(id, at), stock); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`DELETE FROM changes`); err != nil {
		t.Fatal(err)
	}

	// A ninth, while all eight are still alive.
	ninth := aLinkable(200, "iPhone 15, 128 ГБ")
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1400),
		append(stock, ninth)); err != nil {
		t.Fatal(err)
	}

	links, err := s.RepostLinks(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.State == "linked" {
			t.Errorf("a live duplicate was linked: %+v", l)
		}
	}
	kinds := kindsOf(t, s)
	if len(kinds) != 1 || kinds[0] != string(track.KindListingAppeared) {
		t.Errorf("changes = %v, want one ordinary new-listing alert", kinds)
	}
}

// TestARefusalIsWrittenDownAllTheSame.
//
// «Почему мне не сказали про это объявление» has to be answerable for the pairs
// that were considered and refused, not only for the ones that were linked.
//
// The refusal exercised here is a predecessor that only left the window, which
// is a judgement the gates make and the candidate query does not.
//
// Most refusals never reach the gates at all, and that is deliberate. A
// different seller or a different category produces a different repost_key, so
// the two are never in the same bucket. A pair that co-existed is cut by the
// candidate query, which requires the predecessor to have been last seen before
// the arrival — otherwise a dealer with eight identical live phones would write
// eight rejection rows on every arrival, which is noise for something refused
// by arithmetic rather than by judgement.
func TestARefusalIsWrittenDownAllTheSame(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	mine := aLinkable(100, "iPhone 15, 128 ГБ")
	for _, at := range []int64{1000, 1200} {
		if _, err := s.SavePass(ctx, aDesktopPass(id, at),
			[]avito.Listing{mine}); err != nil {
			t.Fatal(err)
		}
	}
	// It left page one, which is not the same as having ended: that happens to
	// every listing on a busy query within the hour, and a merge built on it
	// would be built on nothing.
	if _, err := s.DB().Exec(`
		UPDATE listings SET state = 'left-window', last_seen_at = 1300
		WHERE item_id = 100`); err != nil {
		t.Fatal(err)
	}

	second := aLinkable(200, "iPhone 15, 128 ГБ")
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1400),
		[]avito.Listing{second}); err != nil {
		t.Fatal(err)
	}

	links, err := s.RepostLinks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) == 0 {
		t.Fatal("a refused pair left no record at all")
	}
	if links[0].State != "rejected" {
		t.Errorf("state = %q", links[0].State)
	}
	if len(links[0].Evidence) == 0 || links[0].Evidence[0].Detail == "" {
		t.Errorf("the refusal carries no reason: %+v", links[0].Evidence)
	}
}

// TestASeedingPassLinksNothing.
//
// A re-seed's arrivals are a whole page appearing at once. Running the linker
// over them would turn a gap in collection into a page of reposts.
func TestASeedingPassLinksNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	// Two passes, because a seed is not an admissible pass and the re-seed
	// rule is «too long since the last ADMISSIBLE one». Without the second
	// there is nothing for the gap to be measured from.
	first := aLinkable(100, "iPhone 15, 128 ГБ")
	for _, at := range []int64{1000, 1200} {
		if _, err := s.SavePass(ctx, aDesktopPass(id, at),
			[]avito.Listing{first}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`
		UPDATE listings SET state = 'vanished', last_seen_at = 1300
		WHERE item_id = 100`); err != nil {
		t.Fatal(err)
	}

	// Far enough ahead that the pass is a re-seed rather than a reading.
	p := aDesktopPass(id, 1200+int64(300*10))
	second := aLinkable(200, "iPhone 15, 128 ГБ")
	if _, err := s.SavePass(ctx, p, []avito.Listing{second}); err != nil {
		t.Fatal(err)
	}
	links, err := s.RepostLinks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.State == "linked" {
			t.Errorf("a re-seed linked something: %+v", l)
		}
	}
}
