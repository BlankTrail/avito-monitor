// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

// aDesktopPass is what a pass from the surface that carries everything looks
// like, so a test can say what it is about rather than repeat nine fields.
func aDesktopPass(watchID, at int64) Pass {
	return Pass{
		WatchID: watchID, StartedAt: at, FinishedAt: at + 1, Page: 1,
		Surface: avito.SurfaceDesktop, Class: "ok", Path: "state", Mode: "direct",
	}
}

// aFullListing is one listing as the desktop surface actually delivers it.
func aFullListing(itemID int64) avito.Listing {
	l := aListing()
	l.ItemID = itemID
	l.Title = "iPhone 15, 128 ГБ"
	l.SellerKey, l.SellerKeyQuality = "brand:bioniqo", "slug"
	l.SellerName, l.SellerProfilePath = "Re:Market", "/brands/bioniqo"
	l.MicroCategoryID, l.CategoryID, l.CategoryRootID = 1104365, 24, 9
	l.LocationID, l.LocationName = 637640, "Москва"
	l.GeoAddress = "Москва, ул. Ленина, 28"
	lat, lng := 55.75, 37.61
	l.Lat, l.Lng, l.CoordsPrecise = &lat, &lng, true
	l.Description = "Продаю телефон, состояние отличное."
	l.ImageKey = "https://90.img.avito.st/image/1/1.ureJN7aAFl"
	return l
}

// TestAPassFillsTheColumnsTheLadderReads.
//
// upsertListing wrote nine columns of thirty, and the export's «Продавец» was
// empty on all sixty rows of a live run because of it. Every column below is
// one the repost ladder's gates read.
func TestAPassFillsTheColumnsTheLadderReads(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aFullListing(500)
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}

	var (
		sellerID                     sql.NullInt64
		micro, root, location        int64
		lat, lng                     sql.NullFloat64
		precise                      bool
		geo, locName, titleNorm      string
		repostKey, descSHA, imageKey string
	)
	if err := s.DB().QueryRow(`
		SELECT seller_id, micro_category_id, category_root_id, location_id,
		       lat, lng, coords_precise, geo_address, location_name, title_norm,
		       repost_key, description_sha, image_key
		FROM listings WHERE item_id = 500`).Scan(
		&sellerID, &micro, &root, &location, &lat, &lng, &precise,
		&geo, &locName, &titleNorm, &repostKey, &descSHA, &imageKey); err != nil {
		t.Fatal(err)
	}
	if !sellerID.Valid || sellerID.Int64 == 0 {
		t.Error("seller_id is NULL; the ladder's one hard gate has no input")
	}
	if micro != 1104365 || root != 9 || location != 637640 {
		t.Errorf("category/location = %d/%d/%d", micro, root, location)
	}
	if !lat.Valid || !lng.Valid || !precise {
		t.Errorf("coords = %v/%v precise=%v", lat, lng, precise)
	}
	if geo == "" || locName == "" {
		t.Errorf("address = %q / %q", geo, locName)
	}
	// title_norm has been declared and never written since migration 0001.
	if titleNorm != avito.FoldTitle(l.Title) {
		t.Errorf("title_norm = %q, want %q", titleNorm, avito.FoldTitle(l.Title))
	}
	if repostKey != avito.RepostKey(l) {
		t.Errorf("repost_key = %q, want %q", repostKey, avito.RepostKey(l))
	}
	if descSHA != avito.DescriptionSHA(l.Description) {
		t.Errorf("description_sha = %q", descSHA)
	}
	if imageKey != l.ImageKey {
		t.Errorf("image_key = %q", imageKey)
	}
}

// TestTheSellerIsWrittenOnceWithItsKeyQuality.
//
// A heuristic key is never accepted as repost evidence, and the schema can only
// enforce that if the quality was written beside the key.
func TestTheSellerIsWrittenOnceWithItsKeyQuality(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	one, two := aFullListing(500), aFullListing(501)
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000),
		[]avito.Listing{one, two}); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM sellers`); n != 1 {
		t.Errorf("%d sellers for two listings by one seller", n)
	}
	var quality, name, path string
	if err := s.DB().QueryRow(
		`SELECT key_quality, display_name, profile_path FROM sellers`).Scan(
		&quality, &name, &path); err != nil {
		t.Fatal(err)
	}
	if quality != "slug" || name != "Re:Market" || path != "/brands/bioniqo" {
		t.Errorf("seller = %q / %q / %q", quality, name, path)
	}
	if n := countRows(t, s,
		`SELECT count(*) FROM listings WHERE seller_id IS NOT NULL`); n != 2 {
		t.Errorf("%d listings point at a seller", n)
	}
}

// TestAKeyWithNoStatedQualityIsFiledAsHeuristic.
//
// Which is the treatment that makes it useless as repost evidence — exactly
// right for a key whose provenance was lost somewhere upstream.
func TestAKeyWithNoStatedQualityIsFiledAsHeuristic(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aFullListing(500)
	l.SellerKeyQuality = ""
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var quality string
	if err := s.DB().QueryRow(`SELECT key_quality FROM sellers`).Scan(&quality); err != nil {
		t.Fatal(err)
	}
	if quality != "heuristic" {
		t.Errorf("key_quality = %q, want heuristic", quality)
	}
}

// TestAListingWithNoSellerStillSaves, which is every listing from the mobile
// surface — thirty of thirty.
func TestAListingWithNoSellerStillSaves(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aListing()
	l.ItemID = 500
	p := aDesktopPass(id, 1000)
	p.Surface, p.Path = avito.SurfaceMobile, "dom"
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var sellerID sql.NullInt64
	var repostKey string
	if err := s.DB().QueryRow(
		`SELECT seller_id, repost_key FROM listings WHERE item_id = 500`).Scan(
		&sellerID, &repostKey); err != nil {
		t.Fatal(err)
	}
	if sellerID.Valid {
		t.Error("a seller was invented for a listing that identified none")
	}
	if repostKey != "" {
		t.Errorf("repost_key = %q; a key with no context buckets everything together",
			repostKey)
	}
	if n := countRows(t, s, `SELECT count(*) FROM sellers`); n != 0 {
		t.Errorf("%d sellers were written for a listing that named none", n)
	}
}

// TestATitleEditedLaterDoesNotMoveTheKeyItWasLinkedOn.
//
// The whole reason repost_key is written once. A link made against one key must
// not be silently re-pointed at another.
func TestATitleEditedLaterDoesNotMoveTheKeyItWasLinkedOn(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aFullListing(500)
	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := s.DB().QueryRow(
		`SELECT repost_key FROM listings WHERE item_id = 500`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("no key was written to begin with")
	}

	l.Title = "iPhone 15 Pro Max, 1 ТБ, срочно"
	if _, err := s.SavePass(ctx, aDesktopPass(id, 2000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var second, title, titleNorm string
	if err := s.DB().QueryRow(
		`SELECT repost_key, title, title_norm FROM listings WHERE item_id = 500`).Scan(
		&second, &title, &titleNorm); err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("the key moved with the title: %q → %q", first, second)
	}
	// The title itself does update, and so does its folded form: those are what
	// a person reads and searches. Only the key is frozen.
	if title != l.Title || titleNorm != avito.FoldTitle(l.Title) {
		t.Errorf("the title did not update: %q / %q", title, titleNorm)
	}
}

// TestALaterPassThatReadLessDoesNotEraseWhatAnEarlierOneRead.
//
// A listing in two watches is read from both, and one of them may be on the
// mobile surface, which carries none of this. A blanket update would let the
// cheaper page wipe the richer one's work every other pass.
func TestALaterPassThatReadLessDoesNotEraseWhatAnEarlierOneRead(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	rich := addTestWatch(t, s, "десктоп", 300)
	poor := addTestWatch(t, s, "мобильная", 300)

	full := aFullListing(500)
	if _, err := s.SavePass(ctx, aDesktopPass(rich, 1000),
		[]avito.Listing{full}); err != nil {
		t.Fatal(err)
	}

	bare := aListing()
	bare.ItemID, bare.Title = 500, full.Title
	p := aDesktopPass(poor, 2000)
	p.Surface, p.Path = avito.SurfaceMobile, "dom"
	if _, err := s.SavePass(ctx, p, []avito.Listing{bare}); err != nil {
		t.Fatal(err)
	}

	var sellerID sql.NullInt64
	var micro int64
	var lat sql.NullFloat64
	var geo, imageKey, descSHA string
	if err := s.DB().QueryRow(`
		SELECT seller_id, micro_category_id, lat, geo_address, image_key, description_sha
		FROM listings WHERE item_id = 500`).Scan(
		&sellerID, &micro, &lat, &geo, &imageKey, &descSHA); err != nil {
		t.Fatal(err)
	}
	if !sellerID.Valid {
		t.Error("the mobile pass erased the seller")
	}
	if micro != full.MicroCategoryID {
		t.Errorf("micro_category_id = %d; the mobile pass wrote its zero over it", micro)
	}
	if !lat.Valid {
		t.Error("the mobile pass erased the coordinates")
	}
	if geo == "" || imageKey == "" || descSHA == "" {
		t.Errorf("erased: geo=%q image=%q desc=%q", geo, imageKey, descSHA)
	}
}

// TestTheSellerSnapshotFollowsTheSameFingerprintRuleAsAListing.
//
// Write when something moved. A row per pass would be a reputation table
// growing at the rate of collection and saying nothing new.
func TestTheSellerSnapshotFollowsTheSameFingerprintRuleAsAListing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	rating := int64(490)
	l := aFullListing(500)
	l.SellerRatingHundredths = &rating
	l.SellerReviewSummary = "1 898 отзывов"

	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aDesktopPass(id, 2000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM seller_snapshots`); n != 1 {
		t.Errorf("%d snapshots for an unchanged rating", n)
	}

	moved := int64(470)
	l.SellerRatingHundredths = &moved
	if _, err := s.SavePass(ctx, aDesktopPass(id, 3000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM seller_snapshots`); n != 2 {
		t.Errorf("%d snapshots after the rating moved", n)
	}
	var got int64
	if err := s.DB().QueryRow(
		`SELECT rating_hundredths FROM seller_snapshots ORDER BY ts DESC LIMIT 1`).
		Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 470 {
		t.Errorf("rating_hundredths = %d", got)
	}
}

// TestTwoListingsByOneSellerInOnePassWriteOneSnapshot.
//
// The unique index is on (seller_id, ts) and both listings carry the same
// reading at the same moment. The second must not fail the whole transaction —
// which would lose the pass over a duplicate that says nothing new.
func TestTwoListingsByOneSellerInOnePassWriteOneSnapshot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	rating := int64(490)
	one, two := aFullListing(500), aFullListing(501)
	one.SellerRatingHundredths, two.SellerRatingHundredths = &rating, &rating

	if _, err := s.SavePass(ctx, aDesktopPass(id, 1000),
		[]avito.Listing{one, two}); err != nil {
		t.Fatalf("a duplicate reading failed the pass: %v", err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM seller_snapshots`); n != 1 {
		t.Errorf("%d snapshots for one reading", n)
	}
}
