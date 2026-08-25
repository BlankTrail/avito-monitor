// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/export"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Результаты: what was collected, filtered, and the file it comes out as.
//
// Two forms and no JavaScript in the path that matters. The filter is a GET
// form to this page, so the address bar carries what is on screen and a filtered
// view can be bookmarked and sent to somebody. The download is a second GET
// form to /results/export carrying the same filter as hidden fields, which is
// what makes the file agree with the table — a download that ignored the filter
// is the failure this screen would be blamed for, and building the link in
// JavaScript from the live form would put that guarantee in the one place the
// suite cannot reach.
//
// Three routes: the page, the table as a fragment for the refresh button, and
// the file.

// tableLimit is how many rows the page draws.
//
// The browser is not the file. Twenty thousand rows of table is a page that
// takes seconds to lay out and megabytes to send, for a question — «what does
// this look like» — that the first two hundred answer. The count above the table
// is the real number and the screen says which is which, because a silent cap
// reads as «that is all there is».
const tableLimit = 200

func (s *Server) resultsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter, err := s.filterFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	b.WriteString(s.filterCard(r, filter, watches))
	b.WriteString(s.exportCard(r, filter))

	b.WriteString(`<div class="card" id="results-table">`)
	b.WriteString(s.resultsTable(ctx, filter))
	b.WriteString(`</div>`)

	s.render(w, r, page{Title: "Результаты", Body: rawHTML(b.String())})
}

// resultsFragment answers the refresh button.
func (s *Server) resultsFragment(w http.ResponseWriter, r *http.Request) {
	filter, err := s.filterFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeFragment(w, s.resultsTable(r.Context(), filter))
}

// resultsTable is the table and the two numbers above it.
func (s *Server) resultsTable(ctx context.Context, f store.ExportFilter) string {
	n, err := s.Store.CountExportRows(ctx, f)
	if err != nil {
		return note("bad", "Не удалось посчитать: "+err.Error())
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<p class="sum">Под фильтр попадает <strong>%d</strong> %s.</p>`,
		n, esc(ru.Plural(n, "строка", "строки", "строк")))
	if n == 0 {
		b.WriteString(note("neutral",
			"Ничего не найдено. Файл всё равно выгрузится — с заголовком и без "+
				"строк: это тоже ответ."))
		return b.String()
	}
	if n > tableLimit {
		fmt.Fprintf(&b, `<p class="hint">Здесь показаны первые %d — `+
			`в файл попадут все %d.</p>`, tableLimit, n)
	}

	b.WriteString(`<table><tr>` +
		`<th>Заголовок</th><th>Цена</th><th>Наблюдение</th>` +
		`<th>Позиция</th><th>В выдаче</th></tr>`)
	shown := 0
	for row, err := range s.Store.ExportRows(ctx, f) {
		if err != nil {
			b.WriteString(note("bad", err.Error()))
			break
		}
		if shown >= tableLimit {
			break
		}
		shown++
		b.WriteString(resultRow(row))
	}
	b.WriteString(`</table>`)
	return b.String()
}

func resultRow(row export.Row) string {
	var b strings.Builder
	b.WriteString(`<tr><td>`)
	title := row.Title
	if title == "" {
		title = "объявление " + strconv.FormatInt(row.ItemID, 10)
	}
	if row.URLPath != "" {
		fmt.Fprintf(&b, `<a href="https://www.avito.ru%s" rel="noreferrer">%s</a>`,
			attr(row.URLPath), esc(title))
	} else {
		b.WriteString(esc(title))
	}
	b.WriteString(`</td><td>`)
	// The three price kinds stay three statements here too, for the same reason
	// the export keeps them apart: «договорная» is the seller saying something
	// and «не прочитана» is our defect, and a person scanning this column is
	// entitled to tell them apart at a glance.
	switch {
	case row.PriceMinor != nil:
		b.WriteString(esc(rubles(*row.PriceMinor)))
	case row.PriceKind == "negotiable":
		b.WriteString(`<span class="hint">договорная</span>`)
	default:
		b.WriteString(`<span class="hint">не прочитана</span>`)
	}
	fmt.Fprintf(&b, `</td><td>%s</td><td>%d</td><td>%s</td></tr>`,
		esc(row.WatchName), row.Position, boolWord(row.Present))
	return b.String()
}

func boolWord(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

// filterCard is the filter, as a GET form to this same page.
func (s *Server) filterCard(r *http.Request, f store.ExportFilter, watches []store.Watch) string {
	q := r.URL.Query()

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Что показывать</h2>`)
	b.WriteString(`<form method="get" action="/results">`)

	b.WriteString(`<label class="field"><span class="label">Наблюдение</span>` +
		`<select class="input" name="watch">`)
	fmt.Fprintf(&b, `<option value="0"%s>все</option>`, selected(f.WatchID == 0))
	for _, watch := range watches {
		fmt.Fprintf(&b, `<option value="%d"%s>%s</option>`,
			watch.ID, selected(f.WatchID == watch.ID), esc(watch.Name))
	}
	b.WriteString(`</select></label>`)

	b.WriteString(field("Поиск по заголовку", "q", f.Query, "text",
		"Регистр не важен."))
	b.WriteString(field("Цена от, ₽", "min", roublesField(f.PriceMin), "number", ""))
	b.WriteString(field("Цена до, ₽", "max", roublesField(f.PriceMax), "number", ""))

	days := q.Get("days")
	b.WriteString(`<label class="field"><span class="label">За какой срок</span>` +
		`<select class="input" name="days">`)
	for _, d := range []struct {
		value string
		label string
	}{
		{"0", "всё время"},
		{"1", "сутки"},
		{"7", "неделя"},
		{"30", "месяц"},
	} {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(d.value), selected(days == d.value), esc(d.label))
	}
	b.WriteString(`</select><span class="hint">По времени последнего снимка.</span></label>`)

	b.WriteString(checkbox("Только те, что сейчас в выдаче", "present", f.PresentOnly,
		"Снимите, чтобы увидеть и то, что пропало — обычно ради этого сюда и приходят."))

	b.WriteString(`<button class="btn" type="submit">Показать</button>`)
	b.WriteString(`</form>`)

	// The refresh button is the one place app.js earns its keep here: a
	// collection running in the background changes this table under the
	// person, and re-submitting the filter to see it would lose their place
	// on the page.
	fmt.Fprintf(&b, `<p><button class="btn" data-load="%s" `+
		`data-into="#results-table">Обновить таблицу</button></p>`,
		attr("/results/table?"+filterQuery(f).Encode()))
	b.WriteString(`</div>`)
	return b.String()
}

// exportCard is the download: the columns, the format, and the filter again as
// hidden fields so the file cannot disagree with the table above it.
func (s *Server) exportCard(r *http.Request, f store.ExportFilter) string {
	chosen := map[string]bool{}
	for _, key := range r.URL.Query()["column"] {
		chosen[key] = true
	}

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Выгрузить</h2>`)
	b.WriteString(`<form method="get" action="/results/export">`)

	for key, values := range filterQuery(f) {
		for _, v := range values {
			fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`,
				attr(key), attr(v))
		}
	}

	b.WriteString(note("neutral",
		"Ничего не отмечено — выгрузятся все колонки."))
	for _, group := range export.Groups() {
		fmt.Fprintf(&b, `<h3>%s</h3>`, esc(group))
		for _, fld := range export.Fields() {
			if fld.Group != group {
				continue
			}
			b.WriteString(columnBox(fld, chosen[fld.Key]))
		}
	}

	b.WriteString(`<label class="field"><span class="label">Формат</span>` +
		`<select class="input" name="format">`)
	for _, fm := range []struct {
		value string
		label string
	}{
		{"csv", "CSV — таблица для чего угодно"},
		{"xlsx", "XLSX — открывается в Excel двойным щелчком"},
		{"json", "JSON — один массив, для программы"},
		{"jsonl", "JSONL — по объекту на строку, для конвейера"},
	} {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(fm.value), selected(r.URL.Query().Get("format") == fm.value),
			esc(fm.label))
	}
	b.WriteString(`</select></label>`)

	// One knob and not three. Russian Excel wants a semicolon, a comma for the
	// decimal and a byte-order mark, and getting one of the three wrong is a
	// spreadsheet of mojibake or a column of text that will not sum. Three
	// separate controls would be three chances to get it wrong for a person who
	// only ever wanted «так, чтобы открылось в Экселе».
	b.WriteString(checkbox("Настроить CSV под русский Excel", "excel", false,
		"Точка с запятой, запятая в дробях и метка кодировки. "+
			"На другие форматы не влияет."))

	b.WriteString(`<button class="btn" type="submit">Выгрузить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func columnBox(f export.Field, on bool) string {
	checked := ""
	if on {
		checked = " checked"
	}
	// No hidden companion here, deliberately: this is a set and not a switch.
	// An unticked column posts nothing, which is exactly right — the selection
	// is every key that arrived, and an empty selection is every column.
	return fmt.Sprintf(
		`<label class="check"><input type="checkbox" name="column" value="%s"%s>`+
			`<span>%s</span></label>`,
		attr(f.Key), checked, esc(f.Name))
}

func selected(on bool) string {
	if on {
		return " selected"
	}
	return ""
}

func roublesField(minor int64) string {
	if minor == 0 {
		return ""
	}
	return strconv.FormatInt(minor/100, 10)
}

// filterQuery renders a filter back into parameters.
//
// From the parsed filter and not from the incoming query, so that whatever
// travels into the export link is exactly what the table was built from. Copying
// the raw query would carry a malformed «min=сорок» through to the file, where
// it would be dropped a second time and silently produce a different answer.
func filterQuery(f store.ExportFilter) url.Values {
	v := url.Values{}
	if f.WatchID > 0 {
		v.Set("watch", strconv.FormatInt(f.WatchID, 10))
	}
	if f.PresentOnly {
		v.Set("present", "1")
	}
	if f.Query != "" {
		v.Set("q", f.Query)
	}
	if f.PriceMin > 0 {
		v.Set("min", strconv.FormatInt(f.PriceMin/100, 10))
	}
	if f.PriceMax > 0 {
		v.Set("max", strconv.FormatInt(f.PriceMax/100, 10))
	}
	if f.Since > 0 {
		v.Set("since", strconv.FormatInt(f.Since, 10))
	}
	return v
}

// filterFrom reads the filter out of a request.
//
// Prices are typed in roubles and stored in kopecks, and the multiplication
// happens here, once. A person typing 40000 into «Цена до» means forty thousand
// roubles, and a screen that took it as kopecks would show them four hundred
// roubles' worth of nothing.
//
// «since» is accepted as an absolute second as well as «days», because the
// export form carries the resolved value: a download begun at 23:59 and a page
// rendered at 00:01 must not silently cover different weeks.
func (s *Server) filterFrom(r *http.Request) (store.ExportFilter, error) {
	q := r.URL.Query()
	f := store.ExportFilter{
		Query:       strings.TrimSpace(q.Get("q")),
		PresentOnly: ticked(r, "present"),
	}
	f.WatchID, _ = strconv.ParseInt(q.Get("watch"), 10, 64)

	for _, p := range []struct {
		name string
		dst  *int64
	}{{"min", &f.PriceMin}, {"max", &f.PriceMax}} {
		raw := strings.TrimSpace(q.Get(p.name))
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return f, fmt.Errorf("цена «%s» — не число", raw)
		}
		*p.dst = v * 100
	}

	if raw := q.Get("since"); raw != "" {
		f.Since, _ = strconv.ParseInt(raw, 10, 64)
	} else if days, _ := strconv.Atoi(q.Get("days")); days > 0 {
		f.Since = s.now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	}
	return f, nil
}

// optionsFrom is the CSV knob, as one tick.
func optionsFrom(r *http.Request) export.Options {
	if !ticked(r, "excel") {
		return export.Options{}
	}
	return export.Options{Separator: ';', Decimal: ',', Encoding: "utf-8-bom"}
}

// exportResults streams the file.
func (s *Server) exportResults(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	ext, err := export.Extension(format)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter, err := s.filterFrom(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cols := r.URL.Query()["column"]
	if _, unknown := export.Columns(cols); len(unknown) > 0 {
		http.Error(w, "неизвестные колонки: "+strings.Join(unknown, ", "),
			http.StatusBadRequest)
		return
	}

	// The writer is built before a single header is set, and the order is the
	// point. NewWriter is where an option is refused — JSON in windows-1251, an
	// encoding this build does not have — and building it afterwards would
	// answer 400 with a Content-Disposition already attached, which is a
	// browser saving the error message as avito-2026-08-25.csv.
	//
	// It is given w directly rather than a buffer: the whole reason ExportRows
	// is a sequence is that a hundred thousand listings must not be held in
	// memory, and buffering the file here would put them back.
	writer, err := export.NewWriter(format, w, optionsFrom(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", export.ContentType(format))
	// No Content-Length: it is not known until the last row, and declaring one
	// would mean buffering. A browser handles a chunked download fine; what it
	// cannot handle is a length that turns out to be wrong.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="avito-%s.%s"`,
			s.now().UTC().Format("2006-01-02"), ext))

	if _, err := export.Export(r.Context(),
		s.Store.ExportRows(r.Context(), filter), cols, writer); err != nil {
		// Nothing useful can be sent now — the status went out with the first
		// byte. Said where the operator will see it, and the truncated file is
		// its own signal: Export closed the writer, so an XLSX without its
		// central directory does not open at all rather than opening short.
		fmt.Fprintln(os.Stderr, "export:", err)
	}
}
