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
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
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
