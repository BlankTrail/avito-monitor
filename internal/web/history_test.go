// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	_ "image/png"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/chart"
)

func aListing(t *testing.T, s *Server, itemID, threadID int64, title string, firstSeen int64) {
	t.Helper()
	if _, err := s.Store.DB().Exec(`
		INSERT INTO listings
		  (item_id, thread_id, url_path, title, first_seen_at, last_seen_at, state_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		itemID, threadID, "/m/x_"+strconv.FormatInt(itemID, 10), title,
		firstSeen, firstSeen, firstSeen); err != nil {
		t.Fatal(err)
	}
}

func aSnapshot(t *testing.T, s *Server, itemID, ts int64, kind string, minor int64) {
	t.Helper()
	var price any
	if kind == "exact" {
		price = minor
	}
	if _, err := s.Store.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, fingerprint, source, path, price_kind, price_minor)
		VALUES (?, 'mobile', ?, 'fp', 'search', 'dom', ?, ?)`,
		itemID, ts, kind, price); err != nil {
		t.Fatal(err)
	}
}

func aMember(t *testing.T, s *Server, watchID, itemID int64) {
	t.Helper()
	if _, err := s.Store.DB().Exec(`
		INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
		VALUES (?, ?, 1, 0, 0)`, watchID, itemID); err != nil {
		t.Fatal(err)
	}
}

// TestTheChartIsAPng, so the <img> on the page is not a promise.
func TestTheChartIsAPng(t *testing.T) {
	s := newServer(t)
	aListing(t, s, 100, 100, "iPhone 15", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 100, 1_700_003_600, "exact", 3_500_000)

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
	img, format, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || img.Bounds().Dx() == 0 {
		t.Errorf("format=%q bounds=%v", format, img.Bounds())
	}
}

// TestAThreadWithNoPricesSaysSoRatherThanDrawingAnEmptyGrid.
//
// A picture of an empty grid looks like a product whose price was nothing, and
// prompts a second look at a working program.
func TestAThreadWithNoPricesSaysSoRatherThanDrawingAnEmptyGrid(t *testing.T) {
	s := newServer(t)
	aListing(t, s, 100, 100, "договорная", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "negotiable", 0)

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Header().Get("Content-Type") == "image/png" {
		t.Error("a thread with no prices drew a chart")
	}
	if rec.Code == http.StatusOK {
		t.Errorf("code = %d; nothing to draw is not a success", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "нет данных") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestThePageLinksTheChartAndNamesTheThread.
func TestThePageLinksTheChartAndNamesTheThread(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15, 128 ГБ", 1_700_000_000)
	// Two, because one price is shown as a price rather than as a chart.
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 100, 1_700_003_600, "exact", 3_900_000)
	aMember(t, s, id, 100)

	body := get(t, s, "/history?thread=100").Body.String()
	if !strings.Contains(body, "/history/chart?thread=100") {
		t.Error("the page does not point an img at the chart")
	}
	if !strings.Contains(body, "iPhone 15, 128 ГБ") {
		t.Error("the page does not name the thread")
	}
}

// TestARepostIsOneHistoryAndNotTwo.
//
// The whole reason the chart is per thread. Two incarnations of one
// advertisement are one line, and the page says how many numbers it has worn.
func TestARepostIsOneHistoryAndNotTwo(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "старое объявление", 1_700_000_000)
	aListing(t, s, 200, 100, "то же, заново", 1_700_100_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 200, 1_700_100_000, "exact", 3_800_000)
	aMember(t, s, id, 100)
	aMember(t, s, id, 200)

	body := get(t, s, "/history?thread=100").Body.String()
	if !strings.Contains(body, "2 номерами") && !strings.Contains(body, "2 номера") {
		t.Errorf("the page does not say the thread has worn two numbers: %s", body)
	}
	if strings.Contains(body, "старое объявление") {
		t.Error("the page is headed with a title the listing no longer has")
	}
}

// TestTheListIsOfferedWhenNothingIsPicked.
func TestTheListIsOfferedWhenNothingIsPicked(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aMember(t, s, id, 100)

	body := get(t, s, "/history").Body.String()
	if !strings.Contains(body, "iPhone 15") {
		t.Error("the list does not offer the thread")
	}
}

// TestAnEmptyInstallSaysThereIsNothingToShowYet.
func TestAnEmptyInstallSaysThereIsNothingToShowYet(t *testing.T) {
	body := get(t, newServer(t), "/history").Body.String()
	if !strings.Contains(body, "нечего показать") {
		t.Errorf("body = %q", body)
	}
}

// TestABadThreadParameterIsRefusedRatherThanRenderingThreadZero.
func TestABadThreadParameterIsRefusedRatherThanRenderingThreadZero(t *testing.T) {
	s := newServer(t)
	for _, bad := range []string{"?thread=слово", "?thread=-1", "?thread=", "?thread=0"} {
		rec := get(t, s, "/history/chart"+bad)
		if rec.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%q drew a chart", bad)
		}
	}
}

// TestTheHistoryTabIsActiveOnAChart.
//
// A person who followed a link into a chart and sees «Наблюдения» highlighted
// has been told they are somewhere they are not.
func TestTheHistoryTabIsActiveOnAChart(t *testing.T) {
	s := newServer(t)
	active := ""
	for _, tab := range s.tabs("/history?thread=100") {
		if tab.Active {
			active = tab.Href
		}
	}
	if active != "/history" {
		t.Errorf("the active tab on a chart is %q", active)
	}
}

// TestAThreadTitleIsEscaped, because it is a seller's text rendered back on the
// machine that holds the reader's proxy key.
func TestAThreadTitleIsEscaped(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, `<script>alert(1)</script>`, 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aMember(t, s, id, 100)

	if strings.Contains(get(t, s, "/history?thread=100").Body.String(),
		"<script>alert(1)</script>") {
		t.Error("a seller's title was rendered as markup")
	}
}

// TestOnePriceIsShownAsAPriceAndNotAsABrokenImage.
//
// Found by running it: after a single pass every thread has exactly one
// snapshot, chart.Encode answers ErrNoData for a series it cannot span, and the
// page hung a broken <img> under a real heading. There IS data — one reading —
// and a broken image is never the right way to say «one».
func TestOnePriceIsShownAsAPriceAndNotAsABrokenImage(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aMember(t, s, id, 100)

	body := get(t, s, "/history?thread=100").Body.String()
	if strings.Contains(body, "<img") {
		t.Error("the page hangs an img on a thread with one point")
	}
	if !strings.Contains(body, "40\u00a0000 ₽") {
		t.Errorf("the one price is not shown: %s", body)
	}
	if !strings.Contains(body, "одна цена") {
		t.Errorf("the page does not say why there is no chart: %s", body)
	}
}

// TestTwoPricesDoGetAChart, so the guard above is about «one» and not about
// «any».
func TestTwoPricesDoGetAChart(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 100, 1_700_003_600, "exact", 3_500_000)
	aMember(t, s, id, 100)

	if !strings.Contains(get(t, s, "/history?thread=100").Body.String(), "<img") {
		t.Error("a thread with two points got no chart")
	}
}

// TestAThreadWithNoPriceAtAllSaysThatInstead, which is a different sentence
// from «one price»: nothing was ever a number here.
func TestAThreadWithNoPriceAtAllSaysThatInstead(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "договорная", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "negotiable", 0)
	aMember(t, s, id, 100)

	body := get(t, s, "/history?thread=100").Body.String()
	if strings.Contains(body, "<img") {
		t.Error("a thread whose price was never a number got a chart")
	}
	if !strings.Contains(body, "цена ни разу не была числом") {
		t.Errorf("body = %q", body)
	}
}

// TestTheHistoryIndexTellsItsRowsApart.
//
// Found by opening the page: a live shop's twelve listings rendered as a column
// of bare titles, three of them reading «iPhone 14 Pro Max, 256 ГБ, SIM + eSIM»
// with nothing to distinguish them. A list a person cannot read is a list that
// is not there.
func TestTheHistoryIndexTellsItsRowsApart(t *testing.T) {
	s := newServer(t)
	wid := aWatch(t, s, "наблюдение")
	// Two listings whose titles are the same string, which is what a shop's
	// page one actually looks like, at two prices.
	aListing(t, s, 100, 100, "iPhone 14 Pro Max, 256 ГБ", 1_700_000_000)
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 5_099_000)
	aMember(t, s, wid, 100)
	aListing(t, s, 101, 101, "iPhone 14 Pro Max, 256 ГБ", 1_700_000_000)
	aSnapshot(t, s, 101, 1_700_000_000, "exact", 4_799_000)
	aMember(t, s, wid, 101)

	body := get(t, s, "/history").Body.String()
	for _, want := range []string{"Цена", "Видели"} {
		if !strings.Contains(body, want) {
			t.Errorf("в списке нет колонки %q", want)
		}
	}
	if !strings.Contains(body, "₽") {
		t.Error("в списке нет ни одной цены")
	}
}

// countAccent is how many pixels of the series colour the chart actually
// carries. Nothing else on the picture uses it: the frame, the grid and the
// labels are greys.
func countAccent(t *testing.T, png []byte) int {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	want := chart.Accent
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(bl>>8) == want.B {
				n++
			}
		}
	}
	return n
}

// TestTheChartActuallyDrawsTheLineAtTheCadenceTheStoreWritesAt.
//
// The defect this exists for: chartMaxGap was six hours, and a snapshot is
// written when the fingerprint moves plus one anchor every
// Retention.AnchorEvery — a day. So a listing whose price held had readings a
// day apart, every pair further apart than the gap, every pair refused, and the
// chart came out with correct axes and no line at all.
//
// TestTheChartIsAPng did not catch it because its two readings are an hour
// apart: a cadence the store never produces. This one uses the real one.
func TestTheChartActuallyDrawsTheLineAtTheCadenceTheStoreWritesAt(t *testing.T) {
	s := newServer(t)
	const day = 24 * 60 * 60
	base := int64(1_700_000_000)
	aListing(t, s, 100, 100, "iPhone 15", base)
	for i, price := range []int64{4_000_000, 4_000_000, 3_900_000, 3_900_000, 3_700_000} {
		aSnapshot(t, s, 100, base+int64(i)*day, "exact", price)
	}

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
	if n := countAccent(t, rec.Body.Bytes()); n < 100 {
		t.Fatalf("на графике %d пикселей линии: оси нарисованы, а данных нет", n)
	}
}

// TestASilenceLongerThanTheCadenceStillBreaksTheLine, because the fix must not
// have been «join everything». A stretch where a reading was due and none was
// written is a stretch nobody read, and drawing through it is a guess.
func TestASilenceLongerThanTheCadenceStillBreaksTheLine(t *testing.T) {
	s := newServer(t)
	const day = 24 * 60 * 60
	base := int64(1_700_000_000)
	aListing(t, s, 100, 100, "iPhone 15", base)
	// Two readings a fortnight apart: eleven anchors that should have been
	// written and were not.
	aSnapshot(t, s, 100, base, "exact", 4_000_000)
	aSnapshot(t, s, 100, base+14*day, "exact", 3_000_000)

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if n := countAccent(t, rec.Body.Bytes()); n > 20 {
		t.Errorf("линия проведена через двухнедельное молчание: %d пикселей", n)
	}
}

// TestAChartReachingPastAYearSaysWhyItIsBroken.
//
// Past SnapshotWeeklyAfter the readings are thinned to one a week — further
// apart than the gap — so the line breaks over a stretch somebody DID read. The
// rows that would prove it were deleted on purpose, so the picture cannot tell
// that from a real silence and the page says so instead of guessing.
func TestAChartReachingPastAYearSaysWhyItIsBroken(t *testing.T) {
	s := newServer(t)
	const day = 24 * 60 * 60
	now := s.now().Unix()
	aListing(t, s, 100, 100, "iPhone 15", now-400*day)
	aSnapshot(t, s, 100, now-400*day, "exact", 4_000_000)
	aSnapshot(t, s, 100, now-2*day, "exact", 3_000_000)
	aMember(t, s, aWatch(t, s, "наблюдение"), 100)

	if !strings.Contains(get(t, s, "/history?thread=100").Body.String(),
		"прорежены до одного в неделю") {
		t.Error("страница не объяснила разрыв")
	}
}

// TestARecentChartIsNotToldAboutThinning. The note is true only where it
// applies; on a fortnight of history it would explain a break that has a
// different cause entirely.
func TestARecentChartIsNotToldAboutThinning(t *testing.T) {
	s := newServer(t)
	const day = 24 * 60 * 60
	now := s.now().Unix()
	aListing(t, s, 100, 100, "iPhone 15", now-10*day)
	aSnapshot(t, s, 100, now-10*day, "exact", 4_000_000)
	aSnapshot(t, s, 100, now-9*day, "exact", 3_900_000)
	aMember(t, s, aWatch(t, s, "наблюдение"), 100)

	if strings.Contains(get(t, s, "/history?thread=100").Body.String(),
		"прорежены до одного в неделю") {
		t.Error("недельной истории рассказали про годовое прореживание")
	}
}
