// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/export"
)

func member(t *testing.T, s *Store, watchID, itemID int64, present bool, position int) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO watch_members
		  (watch_id, item_id, present, last_position, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, 1000, 2000)`,
		watchID, itemID, present, position); err != nil {
		t.Fatal(err)
	}
}

func collect(t *testing.T, s *Store, f ExportFilter) []export.Row {
	t.Helper()
	var out []export.Row
	for r, err := range s.ExportRows(context.Background(), f) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// TestOneRowPerListingCarryingItsNewestSnapshot.
//
// «Результаты» is what is there now; the history is История's job. A
// spreadsheet with four rows per phone is one nobody can pivot.
func TestOneRowPerListingCarryingItsNewestSnapshot(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "iPhone", 1_000)
	member(t, s, id, 100, true, 3)
	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 2_000, "exact", 3_500_000)
	snapshot(t, s, 100, 3_000, "exact", 3_100_000)

	got := collect(t, s, ExportFilter{})
	if len(got) != 1 {
		t.Fatalf("%d rows for one listing with three snapshots", len(got))
	}
	if got[0].PriceMinor == nil || *got[0].PriceMinor != 3_100_000 {
		t.Errorf("price = %v, want the newest", got[0].PriceMinor)
	}
	if got[0].SnapshotAt != 3_000 {
		t.Errorf("SnapshotAt = %d, want the newest", got[0].SnapshotAt)
	}
	if got[0].Position != 3 || got[0].WatchName != "наблюдение" {
		t.Errorf("row = %+v", got[0])
	}
}

// TestAListingInTwoWatchesAppearsOncePerWatch, because the position and the
// watch name differ and a person exporting one watch wants its own numbers.
func TestAListingInTwoWatchesAppearsOncePerWatch(t *testing.T) {
	s := openTemp(t)
	first := addTestWatch(t, s, "первое", 300)
	second := addTestWatch(t, s, "второе", 300)
	listing(t, s, 100, 100, "iPhone", 1_000)
	member(t, s, first, 100, true, 1)
	member(t, s, second, 100, true, 9)
	snapshot(t, s, 100, 1_000, "exact", 4_000_000)

	if got := collect(t, s, ExportFilter{}); len(got) != 2 {
		t.Fatalf("%d rows, want one per watch", len(got))
	}
	got := collect(t, s, ExportFilter{WatchID: second})
	if len(got) != 1 || got[0].Position != 9 {
		t.Errorf("filtered = %+v", got)
	}
}

// TestWhatHasLeftTheWindowIsIncludedUnlessAskedOtherwise.
//
// Somebody exporting to find out what disappeared needs the rows that are
// gone — which is most of the reason to export at all.
func TestWhatHasLeftTheWindowIsIncludedUnlessAskedOtherwise(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "здесь", 1_000)
	listing(t, s, 200, 200, "ушло", 1_000)
	member(t, s, id, 100, true, 1)
	member(t, s, id, 200, false, 0)
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 200, 1_000, "exact", 100)

	if got := collect(t, s, ExportFilter{}); len(got) != 2 {
		t.Errorf("%d rows by default, want both", len(got))
	}
	got := collect(t, s, ExportFilter{PresentOnly: true})
	if len(got) != 1 || got[0].Title != "здесь" {
		t.Errorf("PresentOnly = %+v", got)
	}
}

// TestThePriceBandIsInclusiveOfBothEnds, the same as rules.Filter, because a
// person typing 40000 as the top means «and forty thousand».
func TestThePriceBandIsInclusiveOfBothEnds(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i, price := range []int64{3_000_000, 4_000_000, 5_000_000} {
		item := int64(100 + i)
		listing(t, s, item, item, "объявление", 1_000)
		member(t, s, id, item, true, i+1)
		snapshot(t, s, item, 1_000, "exact", price)
	}
	got := collect(t, s, ExportFilter{PriceMin: 3_000_000, PriceMax: 4_000_000})
	if len(got) != 2 {
		t.Errorf("%d rows in a band of both ends, want 2", len(got))
	}
}

// TestAPriceBandExcludesAListingWhosePriceWasNotRead.
//
// A band cannot include something whose price is unknown, and «Цена
// договорная» is not «free» — the same reasoning rules.Filter keeps.
func TestAPriceBandExcludesAListingWhosePriceWasNotRead(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "договорная", 1_000)
	member(t, s, id, 100, true, 1)
	snapshot(t, s, 100, 1_000, "negotiable", 0)

	if got := collect(t, s, ExportFilter{PriceMax: 9_000_000}); len(got) != 0 {
		t.Errorf("a band included a listing with no price: %+v", got)
	}
	// And with no band it is there, with the kind saying which absence it is.
	got := collect(t, s, ExportFilter{})
	if len(got) != 1 || got[0].PriceKind != "negotiable" || got[0].PriceMinor != nil {
		t.Errorf("row = %+v", got)
	}
}

// TestTheQueryMatchesCyrillicCaseInsensitively.
//
// SQLite's LIKE folds ASCII only, so «айфон» would not match «Айфон» without
// lowering both sides — and every title on this site is Cyrillic.
func TestTheQueryMatchesCyrillicCaseInsensitively(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "Айфон 15 Про", 1_000)
	listing(t, s, 200, 200, "Самсунг", 1_000)
	member(t, s, id, 100, true, 1)
	member(t, s, id, 200, true, 2)
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 200, 1_000, "exact", 100)

	got := collect(t, s, ExportFilter{Query: "айфон"})
	if len(got) != 1 || got[0].ItemID != 100 {
		t.Errorf("query «айфон» matched %+v", got)
	}
	if got := collect(t, s, ExportFilter{Query: "ПРО"}); len(got) != 1 {
		t.Errorf("query «ПРО» matched %d rows", len(got))
	}
}

// TestTheCountAgreesWithWhatIsStreamed. The screen shows one and downloads the
// other, and two numbers that disagree is a person who thinks the file is short.
func TestTheCountAgreesWithWhatIsStreamed(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 25; i++ {
		item := int64(100 + i)
		listing(t, s, item, item, "объявление", 1_000)
		member(t, s, id, item, i%2 == 0, i+1)
		snapshot(t, s, item, 1_000, "exact", int64(1_000_000*(i+1)))
	}
	for _, f := range []ExportFilter{
		{},
		{PresentOnly: true},
		{PriceMin: 5_000_000},
		{Query: "объяв"},
	} {
		n, err := s.CountExportRows(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(collect(t, s, f)); got != n {
			t.Errorf("%+v: count says %d, stream gave %d", f, n, got)
		}
	}
}

// TestAConsumerThatStopsEarlyDoesNotLeakTheRowsHandle.
//
// Which is what a cancelled download looks like. Asserted through the store
// still answering afterwards: a leaked read holds a connection, and the next
// query would block on it.
func TestAConsumerThatStopsEarlyDoesNotLeakTheRowsHandle(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		item := int64(100 + i)
		listing(t, s, item, item, "объявление", 1_000)
		member(t, s, id, item, true, i+1)
		snapshot(t, s, item, 1_000, "exact", 100)
	}
	for range s.ExportRows(ctx, ExportFilter{}) {
		break
	}
	if _, err := s.CountExportRows(ctx, ExportFilter{}); err != nil {
		t.Fatalf("the store did not answer after an abandoned stream: %v", err)
	}
}

// TestAListingWithNoSnapshotAtAllStillExports.
//
// It happens: a pass saw the listing and retention later thinned its snapshots
// away. Dropping the row would make the export quietly disagree with the window
// the panel shows.
func TestAListingWithNoSnapshotAtAllStillExports(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "без снимков", 1_000)
	member(t, s, id, 100, true, 1)

	got := collect(t, s, ExportFilter{})
	if len(got) != 1 {
		t.Fatalf("%d rows", len(got))
	}
	if got[0].PriceKind != "absent" || got[0].PriceMinor != nil {
		t.Errorf("row = %+v; no snapshot is «не прочитана», not a price", got[0])
	}
	if got[0].SnapshotAt != 0 {
		t.Errorf("SnapshotAt = %d, want zero so the export writes an empty cell",
			got[0].SnapshotAt)
	}
}

// TestResultsComeOutInTheOrderTheSiteShowedThem.
//
// Found by looking at the rendered screen rather than by reading code: the
// table carries a «Позиция» column and the rows came out 1, 9, 12, 8, 7, 2 down
// a twelve-row list, because the order was item id and item id is neither
// position nor age.
func TestResultsComeOutInTheOrderTheSiteShowedThem(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately descending ids against ascending positions, so an order by
	// id and an order by position cannot agree by accident.
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(900, 800, 700)); err != nil {
		t.Fatal(err)
	}

	var positions []int
	for row, err := range s.ExportRows(ctx, ExportFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		positions = append(positions, row.Position)
	}
	if len(positions) != 3 {
		t.Fatalf("строк = %d", len(positions))
	}
	for i, got := range positions {
		if got != i+1 {
			t.Fatalf("позиции = %v, ожидалось 1 2 3", positions)
		}
	}
}

// TestWhatHasGoneSinksToTheBottom. A listing still in the result set has a
// position that means something; one that has gone does not, and mixing the two
// by position would sort the departed among the living by a stale number.
func TestWhatHasGoneSinksToTheBottom(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(900, 800, 700)); err != nil {
		t.Fatal(err)
	}
	// 900 held position one and is now gone.
	if _, err := s.SavePass(ctx, aPass(wid, 2000), listingsFor(800, 700)); err != nil {
		t.Fatal(err)
	}

	var ids []int64
	for row, err := range s.ExportRows(ctx, ExportFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ItemID)
	}
	if len(ids) != 3 || ids[len(ids)-1] != 900 {
		t.Fatalf("порядок = %v, ушедшее должно быть последним", ids)
	}
}

// TestTheExportedSellerIsOneAPersonCanRead.
//
// The «Продавец» column used to carry this database's own row number: a file
// opened in Excel said «Продавец: 1», which means nothing to whoever opened it
// and something different on every install. Found by downloading the file.
func TestTheExportedSellerIsOneAPersonCanRead(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	l := aListing()
	l.ItemID = 900
	l.SellerKey = "brand:abc123"
	l.SellerName = "Телефоны и точка"
	if _, err := s.SavePass(ctx, aPass(wid, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}

	var seen bool
	for row, err := range s.ExportRows(ctx, ExportFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		seen = true
		if row.SellerKey != "brand:abc123" {
			t.Errorf("ключ продавца = %q", row.SellerKey)
		}
		if row.SellerName != "Телефоны и точка" {
			t.Errorf("имя продавца = %q", row.SellerName)
		}
		if row.SellerID == 0 {
			t.Error("внутренний номер тоже нужен: по нему группируют внутри файла")
		}
	}
	if !seen {
		t.Fatal("ни одной строки")
	}
}

// TestAListingWithNoSellerExportsEmptyAndNotAZero. Most of the mobile surface
// identifies nobody, and «0» in a column headed «Продавец» is a claim.
func TestAListingWithNoSellerExportsEmptyAndNotAZero(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	l := aListing()
	l.ItemID = 901
	l.SellerKey, l.SellerName = "", ""
	if _, err := s.SavePass(ctx, aPass(wid, 1000), []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	for row, err := range s.ExportRows(ctx, ExportFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		if row.SellerKey != "" || row.SellerName != "" {
			t.Errorf("продавца нет, а выгрузилось %q / %q", row.SellerKey, row.SellerName)
		}
	}
}
