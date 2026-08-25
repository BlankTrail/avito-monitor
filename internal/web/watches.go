// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// minIntervalSec is the floor on how often a watch may be polled.
//
// Measured 2026-08-24: within about a minute Avito returns the same page byte
// for byte — three passes one to four seconds apart came back with the same
// list AND the same total_count, as did a pair forty-seven seconds apart. Two
// minutes leaves room either side of that without spending requests on a page
// that has not moved. See
// docs/measurements/2026-08-24-how-fast-the-window-turns-over.md.
const minIntervalSec = 120

// maxPagesPerPass bounds what one watch may ask for in one pass.
//
// Not a technical limit: it is the number above which a single watch can eat a
// whole address's budget in one tick, and the person who typed it would learn
// that as a ban rather than as a refusal.
const maxPagesPerPass = 10

func (s *Server) watchesPage(w http.ResponseWriter, r *http.Request) {
	// Mobile, and the reason is measured rather than preferred: the desktop
	// search page is a shell that carries a canonical URL and no listings, and
	// nothing in this product follows it. A desktop watch collects zero. See
	// the form's own comment below.
	s.renderWatches(w, r, "", store.Watch{Surface: avito.SurfaceMobile,
		MaxPages: 1, IntervalSec: 300})
}

// renderWatches draws the list, the budget and the form.
//
// problem is a refusal to show above the form. draft is what the form is filled
// with — an empty watch normally, and the rejected input when there is a
// problem, because retyping a URL over one wrong field is how a person learns to
// distrust a form.
func (s *Server) renderWatches(w http.ResponseWriter, r *http.Request, problem string, draft store.Watch) {
	ctx := r.Context()
	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, err := s.budget(ctx, watches, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var body strings.Builder

	// The budget first: it is the constraint every row below is measured
	// against, and putting it under the table would make it read as a footnote.
	body.WriteString(`<div class="card">`)
	fmt.Fprintf(&body, `<p class="sum">Потолок <strong>%s</strong> запр./мин — `+
		`%d %s × %s. Занято <strong>%s</strong>, свободно <strong>%s</strong>.</p>`,
		esc(num(b.CapacityPerMin)), b.Addresses,
		esc(ru.Plural(b.Addresses, "адрес", "адреса", "адресов")),
		esc(num(b.PerAddressPerMin)), esc(num(b.UsedPerMin)), esc(num(b.FreePerMin())))
	if b.Addresses == 1 {
		body.WriteString(note("neutral",
			"Один адрес — один потолок, сколько бы соединений ни было открыто. "+
				"Список прокси на вкладке «Прокси» умножает его."))
	}
	body.WriteString(`</div>`)

	if len(watches) > 0 {
		body.WriteString(s.watchTable(ctx, watches))
	}

	if problem != "" {
		body.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}
	body.WriteString(watchForm(draft))

	s.render(w, r, page{Title: "Наблюдения", Body: rawHTML(body.String())})
}

// budget is the arithmetic for the watches as they stand.
func (s *Server) budget(ctx context.Context, watches []store.Watch, exclude int64) (Budget, error) {
	addresses, err := s.addressCount(ctx)
	if err != nil {
		return Budget{}, err
	}
	rpm, err := s.Store.RequestsPerMinute(ctx)
	if err != nil {
		return Budget{}, err
	}
	return BudgetFor(rpm, addresses, watches, exclude), nil
}

func (s *Server) watchTable(ctx context.Context, watches []store.Watch) string {
	// Said once, under the table, when there is nobody configured: a watch with
	// no recipient collects and tells nobody, and that is a state a person
	// would otherwise discover as silence.
	targets, _ := s.Store.EnabledTargets(ctx)

	var b strings.Builder
	b.WriteString(`<div class="card"><table>`)
	b.WriteString(`<tr><th>Название</th><th>Интервал</th><th class="num">Стр.</th>` +
		`<th class="num">Запр./мин</th><th>Состояние</th><th></th></tr>`)
	for _, w := range watches {
		state := `<span class="pill ok">идёт</span>`
		switch {
		case w.State == "halted":
			state = fmt.Sprintf(`<span class="pill bad">остановлено: %s</span>`,
				esc(w.HaltedReason))
		case !w.Enabled:
			state = `<span class="pill warn">выключено</span>`
		case w.SeededAt == nil:
			state = `<span class="pill warn">ещё не засеяно</span>`
		}
		id := strconv.FormatInt(w.ID, 10)
		toggle := "Выключить"
		if !w.Enabled {
			toggle = "Включить"
		}
		fmt.Fprintf(&b, `<tr><td>%s<div class="hint">%s</div></td>`+
			`<td>%d с</td><td class="num">%d</td><td class="num">%s</td><td>%s</td>`,
			esc(w.Name), esc(w.URL), w.IntervalSec, w.MaxPages,
			esc(num(CostPerMinute(w))), state)
		fmt.Fprintf(&b, `<td><form method="post" action="/watches/toggle">`+
			`<input type="hidden" name="id" value="%s">`+
			`<button type="submit">%s</button></form> `, attr(id), esc(toggle))
		fmt.Fprintf(&b, `<form method="post" action="/watches/delete" `+
			`data-confirm="Удалить наблюдение «%s»? Историю это не тронет.">`+
			`<input type="hidden" name="id" value="%s">`+
			`<button class="danger" type="submit">Удалить</button></form></td></tr>`,
			attr(w.Name), attr(id))
	}
	b.WriteString(`</table>`)
	if len(targets) == 0 {
		b.WriteString(note("warning",
			"Получателей нет — эти наблюдения собирают данные, но никому о них "+
				"не сообщают. Добавьте получателя, и правила по умолчанию "+
				"создадутся при следующем добавлении наблюдения."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

func watchForm(d store.Watch) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Добавить наблюдение</h2>`)
	b.WriteString(`<form method="post" action="/watches">`)
	b.WriteString(field("Название", "name", d.Name, "text",
		"Как оно будет называться в сообщениях."))
	b.WriteString(field("Ссылка на поиск", "url", d.URL, "text",
		"Страница выдачи avito.ru — скопируйте её из адресной строки."))
	b.WriteString(field("Интервал, секунды", "interval",
		strconv.Itoa(d.IntervalSec), "number",
		fmt.Sprintf("Не меньше %d. Чаще минуты Авито отдаёт тот же ответ, "+
			"так что запросы тратятся впустую.", minIntervalSec)))
	b.WriteString(field("Страниц за проход", "pages",
		strconv.Itoa(d.MaxPages), "number",
		fmt.Sprintf("Один запрос на страницу. Не больше %d.", maxPagesPerPass)))
	// The two surfaces, and the honest state of both.
	//
	// The label once said mobile carried more cards. It carries thirty against
	// desktop's fifty — that was simply backwards, and it was the only thing on
	// this screen helping somebody choose.
	//
	// Increment 7A then made desktop the default, because the mobile payload
	// carries no seller, no category, no location, no coordinates and no
	// description (0 of 30, measured) and the repost ladder reads all of them.
	// A live run took that back the same day: the desktop search page is a
	// 358 KB shell carrying a canonical URL and no listings, and nothing in
	// this product follows it. A desktop watch collects zero, and it collects
	// zero after clearing a geetest challenge the mobile page never saw.
	//
	// So mobile is the default again, for the plainest possible reason: it is
	// the only surface that collects anything. What it costs is stated below
	// and recovered by «avitomon enrich», which reads a listing's own page —
	// a canonical URL, not a shell — and does work.
	b.WriteString(`<label class="field"><span class="label">Версия сайта</span>` +
		`<select class="input" name="surface">` +
		fmt.Sprintf(`<option value="mobile"%s>мобильная — один запрос на `+
			`страницу, 30 объявлений</option>`,
			selected(d.Surface != avito.SurfaceDesktop)) +
		fmt.Sprintf(`<option value="desktop"%s>десктопная — пока не собирает: `+
			`страница выдачи отдаёт только оболочку</option>`,
			selected(d.Surface == avito.SurfaceDesktop)) +
		`</select>` +
		`<span class="hint">Мобильная выдача не отдаёт ни продавца, ни ` +
		`категорию, ни координаты, ни описание. Перевыкладки по таким ` +
		`объявлениям не связываются, пока продавец неизвестен; дополнить ` +
		`можно командой «avitomon enrich» — по одному запросу на ` +
		`объявление.</span>` +
		`</label>`)
	b.WriteString(`<button class="primary" type="submit">Добавить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func (s *Server) saveWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	draft, err := watchFromForm(r)
	if err != nil {
		s.renderWatches(w, r, err.Error(), draft)
		return
	}

	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, err := s.budget(ctx, watches, draft.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cost := CostPerMinute(draft)
	if !b.Fits(cost) {
		// Refused here rather than discovered as a ban three days later, and
		// with the sum shown rather than a verdict — «слишком часто» tells a
		// person nothing about which number to change.
		s.renderWatches(w, r,
			"Это расписание не помещается в бюджет. "+b.Explain(cost), draft)
		return
	}

	now := s.now().Unix()
	draft.Kind = "search"
	draft.AbsenceConfirmations = 2
	draft.AbsenceMinSpanSec = 1200
	// Due immediately. A watch somebody has just added and that does nothing
	// for five minutes reads as broken.
	draft.NextDueAt = now
	draft.Enabled = true
	draft.CreatedAt, draft.UpdatedAt = now, now

	id, err := s.Store.AddWatch(ctx, draft)
	if err != nil {
		s.renderWatches(w, r, err.Error(), draft)
		return
	}
	// The same seeding the command line does. A watch added from the browser
	// must not be the path that silently produces one nobody hears about.
	if err := s.seedRules(ctx, id); err != nil {
		s.renderWatches(w, r,
			"Наблюдение создано, но правила — нет: "+err.Error(), store.Watch{})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// seedRules creates the four per-watch defaults and, once, the five global
// ones.
//
// Nothing is created when there is nobody to tell: a rule with no target cannot
// pass Validate, deliberately, and the watch is still worth having. The screen
// says so under the table rather than refusing the save — collection is useful
// without notification.
func (s *Server) seedRules(ctx context.Context, watchID int64) error {
	targets, err := s.Store.EnabledTargets(ctx)
	if err != nil || len(targets) == 0 {
		return err
	}
	existing, err := rules.All(ctx, s.Store)
	if err != nil {
		return err
	}
	made := rules.DefaultsForWatch(watchID, targets)
	// The five health rules are about the install rather than about a watch, so
	// they are created once. Keyed on whether a filter-scoped default already
	// exists rather than on the table being empty: somebody who deletes a
	// health rule they did not want must not get it back with their next watch.
	global := true
	for _, r := range existing {
		if r.FromDefaults && r.Scope.Kind == rules.ScopeFilter {
			global = false
			break
		}
	}
	if global {
		made = append(made, rules.GlobalDefaults(targets)...)
	}
	for _, r := range made {
		if _, err := rules.Save(ctx, s.Store, r); err != nil {
			return fmt.Errorf("правило «%s»: %w", r.Name, err)
		}
	}
	return nil
}

func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого наблюдения", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteWatch(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) toggleWatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого наблюдения", http.StatusBadRequest)
		return
	}
	cur, err := s.Store.Watch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	cur.Enabled = !cur.Enabled
	if err := s.Store.UpdateWatch(r.Context(), cur); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// watchFromForm reads and checks one form, reporting every fault it can see.
//
// Every fault at once, because a form that rejects one field at a time is a
// form somebody submits five times.
func watchFromForm(r *http.Request) (store.Watch, error) {
	w := store.Watch{
		Name:    strings.TrimSpace(r.FormValue("name")),
		URL:     strings.TrimSpace(r.FormValue("url")),
		Surface: avito.Surface(r.FormValue("surface")),
	}
	w.IntervalSec, _ = strconv.Atoi(r.FormValue("interval"))
	w.MaxPages, _ = strconv.Atoi(r.FormValue("pages"))
	w.Enabled = true
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		w.ID = id
	}

	var bad []string
	if w.Name == "" {
		bad = append(bad, "не задано название")
	}
	if err := checkSearchURL(w.URL); err != nil {
		bad = append(bad, err.Error())
	}
	if w.IntervalSec < minIntervalSec {
		bad = append(bad, fmt.Sprintf(
			"интервал меньше %d секунд: чаще минуты Авито отдаёт тот же ответ "+
				"с точностью до байта, так что запросы тратятся впустую",
			minIntervalSec))
	}
	if w.MaxPages < 1 || w.MaxPages > maxPagesPerPass {
		bad = append(bad, fmt.Sprintf("страниц за проход должно быть от 1 до %d",
			maxPagesPerPass))
	}
	switch w.Surface {
	case avito.SurfaceMobile, avito.SurfaceDesktop:
	default:
		bad = append(bad, "неизвестная версия сайта")
	}
	if len(bad) > 0 {
		return w, errors.New(strings.Join(bad, "; "))
	}
	return w, nil
}

// checkSearchURL refuses anything this program will not fetch.
//
// Before a request is ever made rather than after: a watch pointed at another
// site would spend a pass and a pacing slot to arrive at a page the extractor
// cannot read, and report it as a collection failure.
func checkSearchURL(raw string) error {
	if raw == "" {
		return errors.New("не задана ссылка")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("ссылку не удалось разобрать")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("ссылка должна начинаться с http:// или https://")
	}
	host := strings.ToLower(u.Hostname())
	// The suffix check carries its dot deliberately: without it
	// «avito.ru.example.com» ends in «avito.ru» and would be accepted.
	if host != "avito.ru" && !strings.HasSuffix(host, ".avito.ru") {
		return errors.New("это не ссылка на avito.ru")
	}
	return nil
}

// addressCount is how many distinct egress addresses are configured.
//
// Never fewer than one: the host's own address is an address, and a budget of
// nothing would refuse every watch on an install that has not configured a
// proxy — which is most of them.
func (s *Server) addressCount(ctx context.Context) (int, error) {
	channels, err := s.Store.Channels(ctx)
	if err != nil {
		return 1, err
	}
	counts, err := s.Store.CountProxies(ctx)
	if err != nil {
		return 1, err
	}
	total := 0
	for _, c := range channels {
		if !c.Enabled {
			continue
		}
		switch c.Kind {
		case "proxy-list":
			total += counts[c.ID]
		case "rotating", "gateway", "direct":
			// One address each. A rotating channel is one address at a time —
			// that is what makes it rotating — and a gateway is one exit
			// however many ports point at it.
			total++
		}
	}
	if total < 1 {
		return 1, nil
	}
	return total, nil
}
