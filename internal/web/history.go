// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/chart"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// История: the price chart, per thread.
//
// Per thread and not per listing because a listing has incarnations. A seller
// who reposts the same phone produces a new number, and a history drawn per
// listing restarts from nothing every time — which is exactly what a person
// opened this screen to avoid.
//
// Two routes, because a chart is an image: the page renders the frame and the
// <img> points at the second, which answers a PNG. The alternative — a data URI
// inline — puts a hundred kilobytes of base64 into every page load and into the
// browser's history.

// rubles renders kopecks the way the notifier does.
//
// The same shape as internal/telegram's, including the non-breaking separator:
// «40 000» wrapped after the 40 is a listing that appears to cost forty roubles.
// Not shared with it, because that package renders whole messages and this
// needs one number — an import for six lines would tie a screen to a notifier.
func rubles(minor int64) string {
	whole, frac := minor/100, minor%100
	if frac < 0 {
		frac = -frac
	}
	out := groupThousands(whole)
	if frac != 0 {
		out += fmt.Sprintf(",%02d", frac)
	}
	return out + " ₽"
}

func groupThousands(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// chartMaxGap is how long a silence has to be before the line breaks, and it
// is derived from the write cadence rather than picked.
//
// It used to be six hours, with a comment saying six «is comfortably longer
// than any watch's interval». The watch interval is not what governs this: a
// snapshot is written when the fingerprint MOVES, plus one anchor every
// Retention.AnchorEvery — a day by default — for a listing that has not
// changed. So a phone whose price held for a week has seven readings a day
// apart, every pair of them further apart than six hours, every pair refused,
// and the chart came out with correct axes and no line at all. That is what
// looking at the rendered PNG showed.
//
// The same comment named the fact that sank it — «far shorter than the
// retention that thins old snapshots to one a day» — and drew the wrong
// conclusion from it.
//
// So: the anchor interval plus a quarter of it. Below that a missing reading
// means we would have written a row and did not, which is a real silence.
// Above it there is nothing to distinguish a silence from the cadence itself.
//
// The remaining hole is named rather than papered over: past thirty days
// retention thins to one row a day and past a year to one a week, so a stretch
// old enough to have been thinned breaks the line even though somebody did
// look. The rows that would prove otherwise were deleted on purpose. The
// screen says so where a chart reaches that far back.
func chartMaxGap(r store.Retention) int64 {
	gap := r.AnchorEvery + r.AnchorEvery/4
	if gap <= 0 {
		gap = 30 * time.Hour
	}
	return int64(gap / time.Second)
}

func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var b strings.Builder

	threadID, _ := strconv.ParseInt(r.URL.Query().Get("thread"), 10, 64)
	if threadID > 0 {
		series, err := s.Store.ThreadSeries(ctx, threadID, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b.WriteString(chartCard(series, s.Store.Retention(), s.now().Unix()))
	}

	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	offered := 0
	for _, watch := range watches {
		threads, err := s.Store.ThreadsWithHistory(ctx, watch.ID, 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(threads) == 0 {
			continue
		}
		offered += len(threads)
		b.WriteString(threadList(watch, threads, s.now().Unix()))
	}
	if offered == 0 && threadID == 0 {
		b.WriteString(`<div class="card">` + note("neutral",
			"Пока нечего показать: история появится, когда наблюдение "+
				"сделает хотя бы один проход и запишет цены.") + `</div>`)
	}

	s.render(w, r, page{Title: "История", Body: rawHTML(b.String())})
}

func chartCard(series store.ThreadSeries, ret store.Retention, now int64) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	title := series.Title
	if title == "" {
		title = "объявление " + strconv.FormatInt(series.ThreadID, 10)
	}
	fmt.Fprintf(&b, `<h2>%s</h2>`, esc(title))

	if n := len(series.ItemIDs); n > 1 {
		// Said out loud, because it is the reason this chart is one line and
		// not several: the same advertisement has worn several numbers.
		fmt.Fprintf(&b, `<p class="hint">Одно объявление под %d %s — `+
			`график сшит по всем.</p>`,
			n, esc(ru.Plural(n, "номером", "номерами", "номерами")))
	}
	if series.URLPath != "" {
		fmt.Fprintf(&b, `<p><a href="https://www.avito.ru%s" rel="noreferrer">`+
			`Открыть на Авито</a></p>`, attr(series.URLPath))
	}

	// A chart needs two points to be a line, and the page knows how many it
	// has. Hanging an <img> on fewer and letting it 404 puts a broken image
	// under a real heading — which is what a first pass produces for every
	// listing, because an unchanged fingerprint writes no second snapshot.
	priced := make([]store.PricePoint, 0, len(series.Points))
	for _, p := range series.Points {
		if p.Present {
			priced = append(priced, p)
		}
	}
	switch {
	case len(priced) == 0:
		b.WriteString(note("neutral",
			"За всё время наблюдения цена ни разу не была числом — "+
				"либо продавец указал «договорная», либо её не удалось прочитать."))
	case len(priced) == 1:
		fmt.Fprintf(&b, `<p class="sum">Пока одна цена: <strong>%s</strong>. `+
			`График появится, когда будет вторая.</p>`,
			esc(rubles(priced[0].Minor)))
	default:
		fmt.Fprintf(&b, `<p><img src="/history/chart?thread=%d" alt="%s" `+
			`style="max-width:100%%"></p>`,
			series.ThreadID, attr("График цены: "+title))
		// The one hole the gap rule cannot close, named where it shows rather
		// than left for somebody to puzzle over.
		//
		// The line breaks across a stretch nobody read, and past
		// SnapshotWeeklyAfter retention has thinned the readings to one a week
		// — further apart than the gap, so the line breaks there too, over a
		// stretch somebody did read. The rows that would prove it were deleted
		// on purpose, so the picture genuinely cannot tell the two apart and
		// says so instead of guessing either way.
		if oldest := priced[0].TS; now-oldest > int64(ret.SnapshotWeeklyAfter/time.Second) {
			b.WriteString(note("neutral",
				"График уходит дальше года назад. Там снимки прорежены до "+
					"одного в неделю, поэтому линия рвётся — не потому, что "+
					"никто не смотрел, а потому, что подробности удалены "+
					"хранением."))
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

func threadList(watch store.Watch, threads []store.ThreadSeries, now int64) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h2>%s</h2><table>`, esc(watch.Name))
	b.WriteString(`<thead><tr><th>Объявление</th><th class="num">Цена</th>` +
		`<th class="num">Видели</th></tr></thead>`)
	for _, t := range threads {
		title := t.Title
		if title == "" {
			title = "объявление " + strconv.FormatInt(t.ThreadID, 10)
		}
		// Price and date, because the title alone does not identify anything.
		// A shop's page one is a dozen of the same model at four prices, and a
		// column of identical strings is a column a person cannot use.
		price := "—"
		if t.LastPriceMinor != nil {
			price = rubles(*t.LastPriceMinor)
		}
		fmt.Fprintf(&b, `<tr><td><a href="/history?thread=%d">%s</a></td>`+
			`<td class="num">%s</td><td class="num">%s</td></tr>`,
			t.ThreadID, esc(title), esc(price), esc(ago(t.LastSeenAt, now)))
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// historyChart answers the PNG the page's <img> points at.
func (s *Server) historyChart(w http.ResponseWriter, r *http.Request) {
	threadID, err := strconv.ParseInt(r.URL.Query().Get("thread"), 10, 64)
	if err != nil || threadID <= 0 {
		// Not a chart of thread zero: a request this malformed is a link
		// somebody built by hand, and drawing an empty grid for it would look
		// like an answer.
		http.Error(w, "нет такого объявления", http.StatusBadRequest)
		return
	}
	series, err := s.Store.ThreadSeries(r.Context(), threadID, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	points := make([]chart.Point, 0, len(series.Points))
	for _, p := range series.Points {
		// NaN for a reading whose price was not a number, which is what breaks
		// the line. A zero would be a price crash that never happened.
		pt := chart.Point{TS: p.TS, Value: math.NaN()}
		if p.Present {
			// Roubles rather than kopecks on the axis: a Russian reader does
			// not price a phone in kopecks, and four extra digits on every tick
			// label buy nothing.
			pt.Value = float64(p.Minor) / 100
		}
		points = append(points, pt)
	}

	line := chart.Line{
		Series: []chart.Series{{Points: points}},
		MaxGap: chartMaxGap(s.Store.Retention()),
		Loc:    time.UTC,
		Y:      chart.Axis{Format: num},
	}

	// Encoded into a buffer first, so a failure can still choose its status
	// code. A chart is a few tens of kilobytes; buffering it costs nothing and
	// buys a 404 instead of a PNG header over an error message.
	var buf bytes.Buffer
	if err := line.Encode(&buf); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "нет данных для графика")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(buf.Bytes())
}
