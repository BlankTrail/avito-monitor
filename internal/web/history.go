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

// chartMaxGap is how long a silence has to be before the line breaks.
//
// Six hours. The number is the caller's knowledge and not the chart's: only
// this side knows how often a listing was supposed to be read, and the
// difference between «the price held for a week» and «nobody looked for a week»
// is exactly this. Six is comfortably longer than any watch's interval and far
// shorter than the retention that thins old snapshots to one a day.
const chartMaxGap = 6 * 60 * 60

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
		b.WriteString(chartCard(series))
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
		b.WriteString(threadList(watch, threads))
	}
	if offered == 0 && threadID == 0 {
		b.WriteString(`<div class="card">` + note("neutral",
			"Пока нечего показать: история появится, когда наблюдение "+
				"сделает хотя бы один проход и запишет цены.") + `</div>`)
	}

	s.render(w, r, page{Title: "История", Body: rawHTML(b.String())})
}

func chartCard(series store.ThreadSeries) string {
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
	fmt.Fprintf(&b, `<p><img src="/history/chart?thread=%d" alt="%s" `+
		`style="max-width:100%%"></p>`,
		series.ThreadID, attr("График цены: "+title))
	b.WriteString(`</div>`)
	return b.String()
}

func threadList(watch store.Watch, threads []store.ThreadSeries) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h2>%s</h2><table>`, esc(watch.Name))
	for _, t := range threads {
		title := t.Title
		if title == "" {
			title = "объявление " + strconv.FormatInt(t.ThreadID, 10)
		}
		fmt.Fprintf(&b, `<tr><td><a href="/history?thread=%d">%s</a></td></tr>`,
			t.ThreadID, esc(title))
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
		MaxGap: chartMaxGap,
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
