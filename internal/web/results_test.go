// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/export"
)

// aResult seeds one listing, its membership and one snapshot.
func aResult(t *testing.T, s *Server, watchID, itemID int64, title string, minor int64) {
	t.Helper()
	db := s.Store.DB()
	if _, err := db.Exec(`
		INSERT INTO listings
		  (item_id, thread_id, url_path, title, first_seen_at, last_seen_at, state_at)
		VALUES (?, ?, ?, ?, 1000, 2000, 1000)`,
		itemID, itemID, fmt.Sprintf("/m/x_%d", itemID), title); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO watch_members
		  (watch_id, item_id, present, last_position, first_seen_at, last_seen_at)
		VALUES (?, ?, 1, 1, 1000, 2000)`, watchID, itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, fingerprint, source, path, price_kind, price_minor)
		VALUES (?, 'mobile', 2000, 'fp', 'search', 'dom', 'exact', ?)`,
		itemID, minor); err != nil {
		t.Fatal(err)
	}
}

func csvHeader(t *testing.T, body string) []string {
	t.Helper()
	head, err := csv.NewReader(strings.NewReader(body)).Read()
	if err != nil {
		t.Fatalf("reading the header: %v — %q", err, body)
	}
	return head
}

func seeded(t *testing.T) *Server {
	t.Helper()
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aResult(t, s, id, 100, "Айфон 15 Про", 4_000_000)
	aResult(t, s, id, 200, "Самсунг S24", 3_000_000)
	return s
}

// TestTheResultsScreenIsReachableAndTabbed.
func TestTheResultsScreenIsReachableAndTabbed(t *testing.T) {
	s := seeded(t)
	rec := get(t, s, "/results")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /results = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(get(t, s, "/").Body.String(), `href="/results"`) {
		t.Error("Результаты is not in the header navigation")
	}
}

// TestTheDownloadCarriesAFilenameWithTheRightExtension.
//
// A file called «export» with no suffix opens in nothing: on Windows a double
// click does nothing at all, and the person who waited for the download has no
// way to tell that from a failure.
func TestTheDownloadCarriesAFilenameWithTheRightExtension(t *testing.T) {
	s := seeded(t)
	for _, format := range []string{"csv", "json", "jsonl", "xlsx"} {
		rec := get(t, s, "/results/export?format="+format)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d (%s)", format, rec.Code, rec.Body.String())
			continue
		}
		cd := rec.Header().Get("Content-Disposition")
		if !strings.Contains(cd, "attachment") {
			t.Errorf("%s: Content-Disposition = %q; it opens in the browser", format, cd)
		}
		if !strings.Contains(cd, "avito-2023-11-14."+format) {
			t.Errorf("%s: Content-Disposition = %q", format, cd)
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" {
			t.Errorf("%s: no Content-Type", format)
		}
	}
}

// TestTheBodyIsStreamedAndNotBuffered.
//
// Held in memory first, a hundred thousand listings is a monitor that runs out
// of it while a scraper is running in the same process. Asserted through a real
// server rather than a recorder, because it is net/http that decides — a body
// it can hold gets a Content-Length, and a body it cannot is chunked. The
// absence of the header is what «streamed» looks like from outside.
func TestTheBodyIsStreamedAndNotBuffered(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := int64(0); i < 300; i++ {
		aResult(t, s, id, 1000+i, fmt.Sprintf("объявление %d", i), 1_000_000+i)
	}

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/results/export?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q; the whole file was buffered to produce it", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(strings.TrimSpace(string(body)), "\n")); n != 301 {
		t.Errorf("%d lines, want 300 rows and a header", n)
	}
}

// TestEveryFormatProducesItsOwnBytes.
//
// A CSV that answers to ?format=xlsx is worse than a refusal: it lands in a
// downloads folder with the right name and the wrong insides, and Excel's
// complaint names the file rather than this program.
func TestEveryFormatProducesItsOwnBytes(t *testing.T) {
	s := seeded(t)

	csv := get(t, s, "/results/export?format=csv&column=title").Body.String()
	if !strings.HasPrefix(strings.TrimPrefix(csv, "\ufeff"), "Заголовок") {
		t.Errorf("csv = %q", csv)
	}

	var list []map[string]any
	arr := get(t, s, "/results/export?format=json&column=title").Body.Bytes()
	if err := json.Unmarshal(arr, &list); err != nil {
		t.Errorf("json is not an array: %v — %q", err, arr)
	} else if len(list) != 2 {
		t.Errorf("json has %d entries", len(list))
	}

	lines := get(t, s, "/results/export?format=jsonl&column=title").Body.String()
	if strings.HasPrefix(strings.TrimSpace(lines), "[") {
		t.Errorf("jsonl carries an envelope: %q", lines)
	}

	xlsx := get(t, s, "/results/export?format=xlsx&column=title").Body.Bytes()
	if !strings.HasPrefix(string(xlsx), "PK") {
		t.Errorf("xlsx is not a zip: %q", xlsx[:min(16, len(xlsx))])
	}
}

// TestAFormatThisBuildDoesNotKnowIsRefusedByName, rather than quietly answering
// with a CSV that the browser then saves under the name that was asked for.
func TestAFormatThisBuildDoesNotKnowIsRefusedByName(t *testing.T) {
	rec := get(t, seeded(t), "/results/export?format=parquet")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "parquet") {
		t.Errorf("the refusal does not name the format: %q", rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("a refusal was given a filename (%q); the browser would save it", cd)
	}
}

// TestTheColumnSelectionSurvivesIntoTheFile, and nothing ticked is everything.
func TestTheColumnSelectionSurvivesIntoTheFile(t *testing.T) {
	s := seeded(t)

	head := csvHeader(t, get(t, s, "/results/export?column=title").Body.String())
	if len(head) != 1 || head[0] != "Заголовок" {
		t.Errorf("one column asked for, header = %q", head)
	}

	// Parsed and not split on commas, because «Скидка, %» carries one — which
	// is also the proof that the header is quoted the way the rows are.
	head = csvHeader(t, get(t, s, "/results/export").Body.String())
	if len(head) != len(export.Fields()) {
		t.Errorf("nothing ticked gave %d columns, want the whole catalogue (%d)",
			len(head), len(export.Fields()))
	}
}

// TestAColumnThisBuildDoesNotHaveIsRefused, so a saved selection from another
// release loses a column loudly rather than months later in a spreadsheet.
func TestAColumnThisBuildDoesNotHaveIsRefused(t *testing.T) {
	rec := get(t, seeded(t), "/results/export?column=колонка-из-будущего")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "колонка-из-будущего") {
		t.Errorf("the refusal does not name the column: %q", rec.Body.String())
	}
}

// TestTheFilterIsOnThePageAndInTheExportForm.
//
// A download that ignored what was on screen is the failure this screen would
// be blamed for: the person filtered to nine listings, pressed «Выгрузить» and
// got four hundred.
func TestTheFilterIsOnThePageAndInTheExportForm(t *testing.T) {
	s := seeded(t)
	body := get(t, s, "/results?q=айфон").Body.String()

	if !strings.Contains(body, `action="/results/export"`) {
		t.Fatal("the page has no export form")
	}
	if !strings.Contains(body, `name="q" value="айфон"`) {
		t.Error("the filter does not travel into the export form")
	}
	if !strings.Contains(body, "Айфон 15 Про") || strings.Contains(body, "Самсунг") {
		t.Error("the table ignored the filter")
	}

	// And the export honours it — the same query, the same rows.
	file := get(t, s, "/results/export?q=айфон&column=title").Body.String()
	if strings.Contains(file, "Самсунг") {
		t.Errorf("the file ignored the filter: %q", file)
	}
	if !strings.Contains(file, "Айфон 15 Про") {
		t.Errorf("the file lost the row the filter kept: %q", file)
	}
}

// TestThePriceFilterIsTypedInRoublesAndNotKopecks.
//
// A person typing 35000 into «Цена до» means thirty-five thousand roubles. Read
// as kopecks it would be three hundred and fifty, and they would be shown an
// empty table for a filter they set correctly.
func TestThePriceFilterIsTypedInRoublesAndNotKopecks(t *testing.T) {
	s := seeded(t)
	body := get(t, s, "/results?max=35000").Body.String()
	if !strings.Contains(body, "Самсунг") {
		t.Error("a 30 000 ₽ listing was excluded by «до 35000»")
	}
	if strings.Contains(body, "Айфон 15 Про") {
		t.Error("a 40 000 ₽ listing survived «до 35000»")
	}
}

// TestATitleIsEscapedInTheTable. Titles on this site are seller-written.
func TestATitleIsEscapedInTheTable(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aResult(t, s, id, 100, `<script>alert(1)</script>`, 100)

	body := get(t, s, "/results").Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("a seller-written title reached the page as markup")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the title is not on the page at all: %q", body)
	}
}

// TestAnExportOfNothingIsAHeaderAndNoRows.
//
// Not a 404. A person who filtered to nothing asked a question and got an
// answer, and a file with a header and no rows is that answer — it also opens
// in a spreadsheet, which an error page does not.
func TestAnExportOfNothingIsAHeaderAndNoRows(t *testing.T) {
	s := seeded(t)
	rec := get(t, s, "/results/export?q=ничего-такого-нет&column=title")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	got := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(got) != 1 || strings.TrimRight(got[0], "\r") != "Заголовок" {
		t.Errorf("body = %q, want just the header", rec.Body.String())
	}
	// And the screen says so rather than looking broken.
	if !strings.Contains(get(t, s, "/results?q=ничего-такого-нет").Body.String(),
		"Ничего не найдено") {
		t.Error("an empty table says nothing")
	}
}

// TestTheExcelTickChangesTheCsvAndNotTheOthers.
//
// One knob, because getting one of the three settings wrong is a spreadsheet of
// mojibake or a price column that will not sum, and the person only ever wanted
// «так, чтобы открылось в Экселе».
func TestTheExcelTickChangesTheCsvAndNotTheOthers(t *testing.T) {
	s := seeded(t)
	body := get(t, s, "/results/export?column=price&excel=0&excel=1").Body.Bytes()
	if !strings.HasPrefix(string(body), "\ufeff") {
		t.Error("no byte-order mark; Excel would read the Russian as windows-1251")
	}
	if !strings.Contains(string(body), "40000,00") {
		t.Errorf("the decimal comma was not applied: %q", body)
	}
	// The same tick on a format it cannot touch is ignored rather than refused.
	if rec := get(t, s, "/results/export?format=xlsx&excel=0&excel=1"); rec.Code != http.StatusOK {
		t.Errorf("xlsx with the Excel tick = %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestTheCountAboveTheTableIsTheWholeNumberAndTheCapIsSaid.
//
// A silent cap reads as «that is all there is», which is how somebody concludes
// their watch found two hundred listings when it found nine thousand.
func TestTheCountAboveTheTableIsTheWholeNumberAndTheCapIsSaid(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := int64(0); i < tableLimit+5; i++ {
		aResult(t, s, id, 1000+i, fmt.Sprintf("объявление %d", i), 100)
	}
	body := get(t, s, "/results").Body.String()
	if !strings.Contains(body, fmt.Sprintf("<strong>%d</strong>", tableLimit+5)) {
		t.Error("the count above the table is not the whole number")
	}
	if !strings.Contains(body, "показаны первые") {
		t.Error("the cap is not said out loud")
	}
	if n := strings.Count(body, "<tr>"); n != tableLimit+1 {
		t.Errorf("%d rows drawn (with the header), want %d", n, tableLimit+1)
	}
}

// TestTheTableFragmentAnswersOnItsOwn, which is what the refresh button loads.
func TestTheTableFragmentAnswersOnItsOwn(t *testing.T) {
	s := seeded(t)
	rec := get(t, s, "/results/table?q=айфон")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<nav") {
		t.Error("the fragment is a whole page")
	}
	if !strings.Contains(body, "Айфон 15 Про") || strings.Contains(body, "Самсунг") {
		t.Errorf("the fragment ignored the filter: %q", body)
	}
}

// TestAPriceThatIsNotANumberIsRefusedRatherThanIgnored.
//
// Ignored, the screen would answer a question nobody asked and look like it had
// filtered — and the export form would then carry the same silence into a file.
func TestAPriceThatIsNotANumberIsRefusedRatherThanIgnored(t *testing.T) {
	rec := get(t, seeded(t), "/results?max=сорок")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("= %d (%s)", rec.Code, rec.Body.String())
	}
}
