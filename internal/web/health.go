// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Здоровье сбора: the screen that makes the transport honest.
//
// Its whole reason is one sentence of the spec — a monitor being flagged, a
// monitor being throttled and a quiet market look identical from everywhere
// else in the product — and every figure on it exists to tell those three
// apart.
//
// Nothing here invents a number where it has no data. That is the spec's rule
// about saturation, and it governs the rest: an unarmed card-yield alarm says
// «ещё не набралось десяти проходов», a watch never read says «ни разу», and a
// query with no arrivals says «нечего измерять» rather than showing a ratio.

// healthWindow is how far back the shares and the block picture look.
//
// A day, which is the spec's word. Long enough that an hour of throttling does
// not dominate it, short enough that a switch from state to dom this morning is
// not buried under a month of health.
const healthWindow = 24 * time.Hour

func (s *Server) healthPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := s.now().Add(-healthWindow).Unix()

	shares, err := s.Store.PathShares(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hours, err := s.Store.ClassByHour(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	health, err := s.Store.WatchHealth(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder

	total := 0
	for _, sh := range shares {
		total += sh.Count
	}
	if total == 0 {
		b.WriteString(`<div class="card">` + note("warning",
			"За последние сутки ещё не было ни одного прохода. "+
				"Это не поломка, если наблюдения только что созданы.") + `</div>`)
	}

	b.WriteString(pathCard(shares, total))
	b.WriteString(s.watchCards(ctx, health))
	b.WriteString(blockCard(hours))
	b.WriteString(s.volumeCard(ctx))

	s.render(w, r, page{Title: "Здоровье сбора", Body: rawHTML(b.String())})
}

// pathCard is the extraction-path share, and what a shift in it means.
func pathCard(shares []store.PathShare, total int) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Чем разобраны страницы</h2>`)
	if total == 0 {
		b.WriteString(note("neutral", "Нет проходов за сутки."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(`<table><tr><th>Путь</th><th class="num">Проходов</th>` +
		`<th class="num">Доля</th></tr>`)
	dom := 0
	for _, sh := range shares {
		label := sh.Path
		switch sh.Path {
		case "state":
			label = "состояние страницы (state)"
		case "dom":
			label = "разметка (dom)"
			dom = sh.Count
		case "":
			label = "не дошло до разбора"
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td>`+
			`<td class="num">%s%%</td></tr>`,
			esc(label), sh.Count, esc(num(100*float64(sh.Count)/float64(total))))
	}
	b.WriteString(`</table>`)
	// The alarm is about the desktop surface only, and the screen says so
	// rather than alarming on mobile where dom is what a healthy pass has
	// always answered.
	if dom > 0 {
		b.WriteString(note("neutral",
			"На десктопной версии переход с «состояния» на «разметку» — самый "+
				"ранний признак, что установку пометили: раньше, чем 429, и "+
				"намного раньше капчи. На мобильной версии «разметка» — это "+
				"норма, и там тот же вопрос отвечает съём карточек ниже."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// watchCards is the per-watch half: the two ages, the evidential fraction, the
// saturation figure and the card yield.
func (s *Server) watchCards(ctx context.Context, health []store.WatchHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Наблюдения</h2>`)
	if len(health) == 0 {
		b.WriteString(note("neutral", "Наблюдений пока нет."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(`<table><tr><th>Наблюдение</th><th>Последний проход</th>` +
		`<th>Последний доказательный</th><th class="num">Доказательных</th>` +
		`<th>Насыщенность</th><th>Съём карточек</th></tr>`)

	now := s.now().Unix()
	for _, h := range health {
		fmt.Fprintf(&b, `<tr><td>%s<div class="hint">%s</div></td>`,
			esc(h.Name), esc(h.Surface))
		fmt.Fprintf(&b, `<td>%s</td>`, esc(ago(h.LastPassAt, now)))
		// The two ages side by side, because only the second says anything is
		// wrong when a watch answers soft-block every five minutes.
		fmt.Fprintf(&b, `<td>%s</td>`, esc(ago(h.LastEvidentialAt, now)))
		if h.Passes == 0 {
			b.WriteString(`<td class="num">—</td>`)
		} else {
			fmt.Fprintf(&b, `<td class="num">%d из %d</td>`, h.Evidential, h.Passes)
		}
		fmt.Fprintf(&b, `<td>%s</td>`, s.saturationCell(ctx, h.WatchID))
		fmt.Fprintf(&b, `<td>%s</td></tr>`, cardYieldCell(h))
	}
	b.WriteString(`</table>`)
	b.WriteString(note("neutral",
		"«Последний доказательный» — это единственная строка, которая отличает "+
			"тихий рынок от мёртвого сбора: наблюдение может опрашиваться "+
			"каждые пять минут и всё это время отвечать мягкой блокировкой."))
	b.WriteString(`</div>`)
	return b.String()
}

// saturationCell is items_on_page / arrivals over the last ten evidential
// passes.
//
// Zero arrivals is a quiet query and not an alarm: the spec is explicit that it
// is neither a division by zero nor a problem, and is reported as having nothing
// to measure rather than as a ratio.
func (s *Server) saturationCell(ctx context.Context, watchID int64) string {
	v, ok, err := s.Store.Saturation(ctx, watchID, 10)
	if err != nil || !ok {
		return `<span class="pill">нечего измерять</span>`
	}
	if v < 2 {
		return fmt.Sprintf(`<span class="pill bad">%s</span>`+
			`<div class="hint">теряет объявления между опросами</div>`, esc(num(v)))
	}
	return fmt.Sprintf(`<span class="pill ok">%s</span>`, esc(num(v)))
}

// cardYieldCell is the mobile half of the flagged-install question.
func cardYieldCell(h store.WatchHealth) string {
	if !h.CardYieldOK {
		return `<span class="pill">ещё не набралось десяти проходов</span>`
	}
	if h.CardYield < 0.5 {
		return fmt.Sprintf(`<span class="pill bad">%s</span>`+
			`<div class="hint">страница отдаёт вдвое меньше карточек, чем обычно</div>`,
			esc(num(h.CardYield)))
	}
	return fmt.Sprintf(`<span class="pill ok">%s</span>`, esc(num(h.CardYield)))
}

// blockCard is the picture of what the site answered, by class and hour.
func blockCard(hours []store.ClassHour) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Что отвечала площадка</h2>`)
	if len(hours) == 0 {
		b.WriteString(note("neutral", "Нет проходов за сутки."))
		b.WriteString(`</div>`)
		return b.String()
	}

	byHour := map[int64]map[string]int{}
	classes := map[string]bool{}
	var order []int64
	for _, h := range hours {
		if byHour[h.Hour] == nil {
			byHour[h.Hour] = map[string]int{}
			order = append(order, h.Hour)
		}
		byHour[h.Hour][h.Class] += h.Count
		classes[h.Class] = true
	}
	names := make([]string, 0, len(classes))
	for c := range classes {
		names = append(names, c)
	}
	sort.Strings(names)

	b.WriteString(`<table><tr><th>Час</th>`)
	for _, c := range names {
		fmt.Fprintf(&b, `<th class="num">%s</th>`, esc(classLabel(c)))
	}
	b.WriteString(`</tr>`)
	for _, hour := range order {
		fmt.Fprintf(&b, `<tr><td>%s</td>`,
			esc(time.Unix(hour, 0).UTC().Format("02.01 15:04")))
		for _, c := range names {
			n := byHour[hour][c]
			if n == 0 {
				b.WriteString(`<td class="num">—</td>`)
				continue
			}
			fmt.Fprintf(&b, `<td class="num">%d</td>`, n)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)
	if classes["shape"] {
		b.WriteString(note("warning",
			"«Разметка не разобрана» — это наш разбор, а не отказ площадки: "+
				"страница пришла, а наш код перестал её понимать. Чинить надо "+
				"здесь, а не ждать снятия блокировки."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// classLabel is what a class is called on screen.
//
// «shape» reads as «наш разбор» because it is ours: the spec asks for it to be
// labelled as ours rather than theirs, and filed with the site's refusals it
// would send somebody to wait out a block that is not happening.
func classLabel(c string) string {
	switch c {
	case "ok":
		return "в порядке"
	case "empty":
		return "пусто"
	case "shape":
		return "разметка не разобрана (наш разбор)"
	case "soft-block":
		return "мягкая блокировка"
	case "cold":
		return "холодная личность"
	case "throttled":
		return "притормозили"
	case "burned":
		return "личность сожжена"
	case "challenge":
		return "капча"
	case "mangled":
		return "битые байты"
	case "other":
		return "прочее"
	}
	return c
}

func (s *Server) volumeCard(ctx context.Context) string {
	v, err := s.Store.Volume(ctx)
	if err != nil {
		return `<div class="card">` + note("bad", err.Error()) + `</div>`
	}
	names := make([]string, 0, len(v.Rows))
	for n := range v.Rows {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>База</h2>`)
	fmt.Fprintf(&b, `<p class="sum">Файл занимает <strong>%s МБ</strong>.</p>`,
		esc(num(float64(v.FileBytes)/(1<<20))))
	b.WriteString(`<table><tr><th>Таблица</th><th class="num">Строк</th></tr>`)
	for _, n := range names {
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td></tr>`,
			esc(n), v.Rows[n])
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// ago renders a moment as how long ago it was.
//
// Zero is «ни разу», not the epoch. A watch created five minutes ago and a watch
// soft-blocked since March are both «no evidential pass», and 1 January 1970 is
// a date neither has anything to do with.
func ago(at, now int64) string {
	if at <= 0 {
		return "ни разу"
	}
	d := now - at
	switch {
	case d < 0:
		return "только что"
	case d < 60:
		return fmt.Sprintf("%d %s назад", d,
			ru.Plural(int(d), "секунду", "секунды", "секунд"))
	case d < 3600:
		m := d / 60
		return fmt.Sprintf("%d %s назад", m,
			ru.Plural(int(m), "минуту", "минуты", "минут"))
	case d < 86400:
		h := d / 3600
		return fmt.Sprintf("%d %s назад", h,
			ru.Plural(int(h), "час", "часа", "часов"))
	}
	days := d / 86400
	return fmt.Sprintf("%d %s назад", days,
		ru.Plural(int(days), "день", "дня", "дней"))
}
