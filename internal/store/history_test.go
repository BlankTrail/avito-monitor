// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

func listing(t *testing.T, s *Store, itemID, threadID int64, title string, firstSeen int64) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO listings
		  (item_id, thread_id, url_path, title, first_seen_at, last_seen_at, state_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		itemID, threadID, "/m/x_"+title, title, firstSeen, firstSeen, firstSeen); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, s *Store, itemID, ts int64, kind string, minor int64) {
	t.Helper()
	var price any
	if kind == "exact" {
		price = minor
	}
	if _, err := s.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, fingerprint, source, path, price_kind, price_minor)
		VALUES (?, 'mobile', ?, 'fp', 'search', 'dom', ?, ?)`,
		itemID, ts, kind, price); err != nil {
		t.Fatal(err)
	}
}

// TestTheSeriesIsPerThreadAndNotPerListing.
//
// A seller who reposts the same phone produces a new item id. A history drawn
// per listing restarts from nothing every time, which is exactly the history a
// person opened this screen to avoid.
func TestTheSeriesIsPerThreadAndNotPerListing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	listing(t, s, 100, 100, "первое", 1_000)
	listing(t, s, 200, 100, "второе", 3_000) // a repost: same thread

	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 2_000, "exact", 3_900_000)
	snapshot(t, s, 200, 3_000, "exact", 3_800_000)

	got, err := s.ThreadSeries(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 {
		t.Fatalf("%d points, want the whole thread's three", len(got.Points))
	}
	if got.Points[0].TS != 1_000 || got.Points[2].TS != 3_000 {
		t.Errorf("points = %+v, want oldest first", got.Points)
	}
	if len(got.ItemIDs) != 2 {
		t.Errorf("ItemIDs = %v, want both incarnations", got.ItemIDs)
	}
}

// TestAPriceThatWasReadAndWasNotANumberIsNotZero.
//
// «Цена договорная» is the seller saying something. Drawn as a zero it is a
// price crash that never happened; dropped, it is a line joined across a stretch
// where the price was known not to exist.
func TestAPriceThatWasReadAndWasNotANumberIsNotZero(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "объявление", 1_000)
	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 2_000, "negotiable", 0)
	snapshot(t, s, 100, 3_000, "exact", 3_800_000)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 {
		t.Fatalf("%d points, want three", len(got.Points))
	}
	if got.Points[1].Present {
		t.Error("a negotiable price came back as present")
	}
	if got.Points[1].Minor != 0 {
		t.Errorf("Minor = %d on an absent price; the chart reads Present, not this",
			got.Points[1].Minor)
	}
	if !got.Points[0].Present || !got.Points[2].Present {
		t.Error("a real price came back as absent")
	}
}

// TestAPriceWeCouldNotReadIsAlsoNotZero, and is the same kind of absence: our
// blindness is not the seller withdrawing a price, but neither is a number.
func TestAPriceWeCouldNotReadIsAlsoNotZero(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "объявление", 1_000)
	snapshot(t, s, 100, 1_000, "absent", 0)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 1 || got.Points[0].Present {
		t.Errorf("points = %+v", got.Points)
	}
}

// TestTheTitleIsTheNewestIncarnations, because that is what the listing is
// called now, and a chart headed with a title from March is a chart of
// something else.
func TestTheTitleIsTheNewestIncarnations(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "старое имя", 1_000)
	listing(t, s, 200, 100, "новое имя", 2_000)
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 200, 2_000, "exact", 100)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "новое имя" {
		t.Errorf("Title = %q", got.Title)
	}
}

// TestAThreadWithNothingInItIsNotAnError. A person following a link to a thread
// whose snapshots retention has thinned away gets an empty chart and a sentence,
// not a five hundred.
func TestAThreadWithNothingInItIsNotAnError(t *testing.T) {
	got, err := openTemp(t).ThreadSeries(context.Background(), 999, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 || got.ThreadID != 999 {
		t.Errorf("got %+v", got)
	}
}

// TestTheListOffersTheThreadsThisWatchActuallySaw.
func TestTheListOffersTheThreadsThisWatchActuallySaw(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "моё", 1_000)
	listing(t, s, 300, 300, "чужое", 1_000)
	if _, err := s.DB().Exec(
		`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
		 VALUES (?, 100, 1, 0, 0)`, id); err != nil {
		t.Fatal(err)
	}
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 300, 1_000, "exact", 100)

	got, err := s.ThreadsWithHistory(ctx, id, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ThreadID != 100 {
		t.Errorf("threads = %+v, want only this watch's", got)
	}
	if len(got[0].Points) != 0 {
		t.Error("the list carries points; it is a list of heads")
	}
}

// TestARepostAppearsOnceInTheList.
//
// Two incarnations, one thread, one row. A list with the same advertisement
// twice is the repost linking having bought nothing.
func TestARepostAppearsOnceInTheList(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "старое", 1_000)
	listing(t, s, 200, 100, "новое", 2_000)
	for _, item := range []int64{100, 200} {
		if _, err := s.DB().Exec(
			`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
			 VALUES (?, ?, 1, 0, 0)`, id, item); err != nil {
			t.Fatal(err)
		}
		snapshot(t, s, item, 1_000+item, "exact", 100)
	}
	got, err := s.ThreadsWithHistory(ctx, id, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("threads = %+v, want one row for the thread", got)
	}
	if got[0].Title != "новое" {
		t.Errorf("Title = %q, want the newest incarnation's", got[0].Title)
	}
}

// TestASinceCutTrimsTheSeriesAndNotTheHead.
//
// The chart may be asked for the last month; the title still has to be there.
func TestASinceCutTrimsTheSeriesAndNotTheHead(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "объявление", 1_000)
	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 90_000, "exact", 3_800_000)

	got, err := s.ThreadSeries(context.Background(), 100, 50_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 1 || got.Points[0].TS != 90_000 {
		t.Errorf("points = %+v, want only the recent one", got.Points)
	}
	if got.Title != "объявление" {
		t.Errorf("Title = %q; the cut took the head with it", got.Title)
	}
}
