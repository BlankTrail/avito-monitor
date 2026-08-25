// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

// aMobilePass is a pass from the surface that carries none of the ladder's
// inputs, which is the reason enrichment exists.
func aMobilePass(watchID, at int64) Pass {
	p := aDesktopPass(watchID, at)
	p.Surface, p.Path = avito.SurfaceMobile, "dom"
	return p
}

// TestOnlyListingsWithNoSellerAreOffered, so the queue empties rather than
// re-fetching the same rows for ever.
func TestOnlyListingsWithNoSellerAreOffered(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	withSeller := aFullListing(500)
	without := aListing()
	without.ItemID = 501

	if _, err := s.SavePass(ctx, aMobilePass(id, 1000),
		[]avito.Listing{withSeller, without}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ItemID != 501 {
		t.Fatalf("offered %+v, want only 501", got)
	}
	if got[0].URLPath == "" {
		t.Error("a candidate with no path cannot be fetched")
	}
}

// TestAListingWithNoPathIsNotOffered, because there is nothing to open.
func TestAListingWithNoPathIsNotOffered(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title,
		                      first_seen_at, last_seen_at, state_at)
		VALUES (700, 700, '', 'нечего открывать', 1000, 1000, 1000)`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.ItemID == 700 {
			t.Error("a listing with no url_path was offered for fetching")
		}
	}
}

// TestTheQueueIsNewestFirst.
//
// A listing collected an hour ago may still be in somebody's window; one from
// three weeks ago is past the repost window anyway. Drained oldest-first, the
// budget would go entirely to rows that can no longer produce a link.
func TestTheQueueIsNewestFirst(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for i, at := range []int64{1000, 3000, 2000} {
		l := aListing()
		l.ItemID = int64(600 + i)
		if _, err := s.SavePass(ctx, aMobilePass(id, at), []avito.Listing{l}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("%d candidates", len(got))
	}
	if got[0].ItemID != 601 {
		t.Errorf("head of the queue is %d, want the newest (601)", got[0].ItemID)
	}
}

// TestEnrichmentFillsTheSellerAndSealsTheKeyExactlyOnce.
//
// A listing from the mobile surface has no seller at insert, so its key is
// empty. Enrichment is the row becoming complete rather than an edit, so the
// key is written then — and the guard is the UPDATE's own WHERE clause: once
// seller_id is set, no second rewrite is possible.
func TestEnrichmentFillsTheSellerAndSealsTheKeyExactlyOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	bare := aListing()
	bare.ItemID, bare.Title = 501, "iPhone 15, 128 ГБ"
	if _, err := s.SavePass(ctx, aMobilePass(id, 1000),
		[]avito.Listing{bare}); err != nil {
		t.Fatal(err)
	}

	full := aFullListing(501)
	if err := s.ApplyEnrichment(ctx, 501, full, 2000); err != nil {
		t.Fatal(err)
	}

	var sellerID sql.NullInt64
	var key, descSHA, imageKey string
	var micro int64
	if err := s.DB().QueryRow(`
		SELECT seller_id, repost_key, description_sha, image_key, micro_category_id
		FROM listings WHERE item_id = 501`).Scan(
		&sellerID, &key, &descSHA, &imageKey, &micro); err != nil {
		t.Fatal(err)
	}
	if !sellerID.Valid {
		t.Fatal("enrichment did not fill the seller")
	}
	if key != avito.RepostKey(full) {
		t.Errorf("repost_key = %q, want the key of the completed row", key)
	}
	if descSHA == "" || imageKey == "" || micro == 0 {
		t.Errorf("the other signals were not filled: %q / %q / %d",
			descSHA, imageKey, micro)
	}

	// A second enrichment — a re-fetch, a retry, a second watch — must not move
	// the key. Same guarantee the title edit has.
	moved := full
	moved.Title = "совсем другой заголовок"
	moved.SellerKey = "brand:someone-else"
	if err := s.ApplyEnrichment(ctx, 501, moved, 3000); err != nil {
		t.Fatal(err)
	}
	var second string
	var after sql.NullInt64
	if err := s.DB().QueryRow(
		`SELECT repost_key, seller_id FROM listings WHERE item_id = 501`).Scan(
		&second, &after); err != nil {
		t.Fatal(err)
	}
	if second != key {
		t.Errorf("a second enrichment moved the key: %q → %q", key, second)
	}
	if after.Int64 != sellerID.Int64 {
		t.Errorf("a second enrichment repointed the seller: %d → %d",
			sellerID.Int64, after.Int64)
	}
}

// TestAPageThatIdentifiesNoSellerEitherLeavesTheRowInTheQueue.
//
// Marking it done would spend the request and lose the row; a later attempt may
// read a page that does carry one.
func TestAPageThatIdentifiesNoSellerEitherLeavesTheRowInTheQueue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	bare := aListing()
	bare.ItemID = 501
	if _, err := s.SavePass(ctx, aMobilePass(id, 1000),
		[]avito.Listing{bare}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyEnrichment(ctx, 501, bare, 2000); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ItemID != 501 {
		t.Errorf("the row left the queue without gaining a seller: %+v", got)
	}
}

// TestTheQueueCountsWhatIsWaitingAndWhatIsDone, which is what the panel shows.
func TestTheQueueCountsWhatIsWaitingAndWhatIsDone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	var list []avito.Listing
	for i := 0; i < 5; i++ {
		if i < 2 {
			list = append(list, aFullListing(int64(600+i)))
			continue
		}
		l := aListing()
		l.ItemID = int64(600 + i)
		list = append(list, l)
	}
	if _, err := s.SavePass(ctx, aMobilePass(id, 1000), list); err != nil {
		t.Fatal(err)
	}
	waiting, keyed, total, err := s.EnrichmentQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || keyed != 2 || waiting != 3 {
		t.Errorf("queue = waiting %d, keyed %d, total %d; want 3/2/5",
			waiting, keyed, total)
	}
}

// TestAnEmptyDatabaseCountsZeroRatherThanFailing.
//
// sum() over no rows is NULL, and a Scan into an int would fail — on a fresh
// install, which is the first thing anybody sees.
func TestAnEmptyDatabaseCountsZeroRatherThanFailing(t *testing.T) {
	waiting, keyed, total, err := openTemp(t).EnrichmentQueue(context.Background())
	if err != nil {
		t.Fatalf("an empty database failed the count: %v", err)
	}
	if waiting != 0 || keyed != 0 || total != 0 {
		t.Errorf("queue = %d/%d/%d on an empty database", waiting, keyed, total)
	}
}
