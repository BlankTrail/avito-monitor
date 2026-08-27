// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
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
// Two, which is what watches.max_pages is CHECK-constrained to. This screen
// said ten, and the two disagreeing meant somebody typing three got a raw
// constraint failure naming a table instead of a refusal naming the mistake.
//
// The number is two for a measured reason and not a cautious one: walking past
// the first page or two is the loudest escalation trigger on this target — it
// is what turns a 429 into a 403, and a 403 costs the cookie jar and therefore
// the whole clearance the jar was carrying. The schema's own comment says it is
// a constraint rather than a default precisely because the failure mode is
// somebody raising it «just for this one query».
const maxPagesPerPass = 2

func (s *Server) watchesPage(w http.ResponseWriter, r *http.Request) {
	// The default surface follows the transport, because the two surfaces do
	// not answer the same way to the two transports. Measured on live Avito,
	// 25 August 2026: direct mode met a geetest challenge on request one to
	// www.avito.ru and never saw one on m.avito.ru; through BlankTrail the
	// desktop page answered and, once its canonical link is followed, filled
	// 49 of 50 sellers against mobile's none.
	//
	// So: direct gets the only surface that answers, BlankTrail gets the one
	// that carries what the repost ladder reads. Neither is a preference.
	surface := avito.SurfaceMobile
	if mode, err := s.Store.TransportMode(r.Context()); err == nil &&
		mode == transport.ModeBlankTrail {
		surface = avito.SurfaceDesktop
	}
	s.renderWatches(w, r, "", store.Watch{Surface: surface,
		MaxPages: 1, IntervalSec: 300})
}

// collectionState answers «идёт ли сбор» on the screen where the watches are.
//
// Not in Настройки. A monitor that silently stopped collecting is the failure
// this whole product is arranged against, and burying the answer two screens
// away is how somebody spends a week believing they are being watched over.
//
// Answered from the data rather than from a flag, because a flag says what was
// intended and this has to say what happened. The newest pass is the evidence:
// if none has been written in several times the shortest interval, nothing is
// collecting whatever any setting claims.
func (s *Server) collectionState(ctx context.Context, watches []store.Watch) string {
	enabled := 0
	shortest := 0
	for _, w := range watches {
		if !w.Enabled || w.State == "halted" {
			continue
		}
		enabled++
		if shortest == 0 || w.IntervalSec < shortest {
			shortest = w.IntervalSec
		}
	}
	if enabled == 0 {
		return ""
	}

	last, err := s.Store.LastPassAt(ctx)
	if err != nil {
		return ""
	}
	if last == 0 {
		return `<div class="card">` + note("warning",
			"Ни одного прохода ещё не было. Если панель запущена командой "+
				"«avitomon panel», сбор идёт в этом же процессе и первый "+
				"проход будет в течение минуты.") + `</div>`
	}

	age := s.now().Unix() - last
	// Three intervals, the same slack the lifecycle uses to call a pass a
	// re-seed. Two would cry wolf on one slow pass; ten would let a day of
	// silence look normal.
	if age > int64(3*shortest) {
		return `<div class="card">` + note("bad", fmt.Sprintf(
			"Сбор не идёт: последний проход был %s, а самое частое "+
				"наблюдение опрашивается раз в %d с. Проверьте, запущен ли "+
				"«avitomon panel» без -no-collect, и не остановлены ли "+
				"наблюдения.", ago(last, s.now().Unix()), shortest)) + `</div>`
	}
	return `<div class="card">` + note("neutral", fmt.Sprintf(
		"Сбор идёт: последний проход %s.", ago(last, s.now().Unix()))) + `</div>`
}

// paceWarnings names the watches whose passes take longer than their interval.
//
// Measured on 25 August 2026: a mobile pass in direct mode takes two or three
// seconds against a 120-second interval, and a desktop pass through BlankTrail
// took seven minutes and forty-one seconds against 300. The second is a watch
// that physically cannot keep its own schedule — the next pass falls due before
// the previous one ends.
//
// The scheduler does not know this and the budget line cannot tell: that
// arithmetic counts requests and not seconds. What such a watch actually does
// is get read less often than its own row claims, and nothing anywhere said so.
//
// This says so and does nothing else. What a watch SHOULD do when it cannot
// keep up — stretch its interval, skip ticks quietly, or carry on and be
// honest about it — has at least three defensible answers, and choosing one
// inside a scheduler is choosing it by accident. Naming the fact is the part
// that needs no decision.
func (s *Server) paceWarnings(ctx context.Context, watches []store.Watch) string {
	var b strings.Builder
	for _, w := range watches {
		if !w.Enabled || w.State == "halted" || w.IntervalSec <= 0 {
			continue
		}
		pace, err := s.Store.PaceOf(ctx, w.ID)
		if err != nil || !pace.Enough {
			continue
		}
		if pace.MedianSec <= int64(w.IntervalSec) {
			continue
		}
		fmt.Fprintf(&b, `<div class="card">%s</div>`, note("warning", fmt.Sprintf(
			"Наблюдение «%s» не успевает за своим интервалом: проход занимает "+
				"около %d с, а опрашивать его положено раз в %d с. На деле оно "+
				"опрашивается реже — примерно раз в %d с. Поставьте интервал "+
				"побольше, уменьшите число страниц или переведите его на "+
				"мобильную выдачу: она стоит один запрос вместо двух.",
			w.Name, pace.MedianSec, w.IntervalSec, pace.MedianSec)))
	}
	return b.String()
}

// tooBroadWarnings names the watches whose query is wider than this product can
// see the shape of.
//
// Avito pages through at most five thousand results, so above that a
// date-sorted page one is a window onto something whose size is unknowable from
// here — «new listing» stops being a set difference and becomes a guess about
// the top of a firehose.
//
// It names pmin and pmax rather than saying «narrow it». Those are the two
// parameters that actually narrow an Avito search without changing what is
// being watched, and a person who has to go and find that out is a person who
// narrows it by deleting the watch.
//
// Nothing slices automatically. A monitor that multiplies its own request
// budget without being asked is a monitor that earns a 403.
func tooBroadWarnings(watches []store.Watch) string {
	var b strings.Builder
	for _, w := range watches {
		if !w.TooBroad || !w.Enabled {
			continue
		}
		count := "больше пяти тысяч"
		if w.LastTotalCount != nil {
			count = fmt.Sprintf("%d", *w.LastTotalCount)
		}
		fmt.Fprintf(&b, `<div class="card">%s</div>`, note("warning", fmt.Sprintf(
			"Наблюдение «%s» слишком широкое: под запрос попадает %s "+
				"объявлений, а Авито отдаёт не больше пяти тысяч. Первая "+
				"страница такого запроса — это верхушка потока, и «новое "+
				"объявление» перестаёт быть надёжным. Сузьте запрос ценой: "+
				"добавьте в ссылку pmin и pmax — например pmin=20000&pmax=60000. "+
				"Сам продукт запрос не сузит: монитор, который без спроса "+
				"умножает себе бюджет запросов, зарабатывает блокировку.",
			w.Name, count)))
	}
	return b.String()
}

// surfaceAdvice is which surface this installation can actually use.
//
// Measured on live Avito, 25 August 2026, from one machine within minutes:
// direct mode met a geetest challenge on its first request to www.avito.ru and
// never met one on m.avito.ru. Through BlankTrail the desktop page answered.
// So the advice is not a preference and it is not the same for both modes.
func surfaceAdvice(mode transport.Mode) string {
	if mode == transport.ModeBlankTrail {
		return "Через BlankTrail работают обе. Десктопная несёт продавца, " +
			"категорию, координаты и фотографии — без них перевыкладки " +
			"не связываются, — и стоит вдвое дороже в запросах."
	}
	return "В прямом режиме десктопная версия встаёт на капче с первого " +
		"запроса: этот режим её проходить не умеет. Мобильная работает, " +
		"но не отдаёт ни продавца, ни категорию, ни координаты, ни " +
		"описание — дополнить можно командой «avitomon enrich», по одному " +
		"запросу на объявление, и она тоже требует BlankTrail."
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
	// The mode decides which surface this installation can use at all, so the
	// form is drawn knowing it. An unreadable or unchosen setting falls back to
	// direct, which is the more restrictive advice — better to under-promise a
	// surface than to point somebody at one that halts on request one.
	mode, _ := s.Store.TransportMode(ctx)

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
	// The ceiling is the site's wall, not this mode's throughput, and in the
	// free mode the two are far apart. The soak run of 23–24 августа spent
	// sixty-nine of ninety-three minutes waiting out ten refusals — a 74% time
	// tax — and landed at about 250–260 clean requests an hour against the
	// ceiling's 360.
	//
	// Said here rather than left to the pace warning below, which is honest but
	// late: it needs ten passes before it can say anything, and by then the
	// schedule is set and the watches are already running behind. A person
	// filling the budget to its ceiling in direct mode is making a plan the
	// free route cannot keep.
	//
	// The sample size and the single-address caveat travel with the number,
	// because the spec forbids quoting one without them.
	if mode == transport.ModeDirect {
		body.WriteString(note("warning",
			"Это потолок площадки, а не то, что выдаёт прямой режим. "+
				"В замере 23–24 августа он потратил 69 минут из 93 на "+
				"пережидание отказов и выдал около 250–260 чистых запросов "+
				"в час против потолка в 360 — один адрес, один прогон. "+
				"Планируя расписание под самый потолок, вы планируете то, "+
				"чего свободный путь не выдержит."))
	}
	body.WriteString(`</div>`)

	if len(watches) > 0 {
		body.WriteString(s.watchTable(ctx, watches))
	}
	body.WriteString(s.collectionState(ctx, watches))
	body.WriteString(s.paceWarnings(ctx, watches))
	body.WriteString(tooBroadWarnings(watches))

	if problem != "" {
		body.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}
	body.WriteString(watchForm(draft, mode))

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
			// The reason, and the way out. A halt is the only state this
			// program will not leave on its own — «halted_reason is never
			// cleared by the program» — so the row that shows it has to carry
			// the button, or the exit is a command line somebody has to find.
			state = fmt.Sprintf(`<span class="pill bad">остановлено: %s</span>`+
				`<form method="post" action="/watches/resume" `+
				`data-confirm="Возобновить? Ничего не проверяется: если стена `+
				`на месте, следующий проход снова остановится — это один `+
				`запрос и честный ответ.">`+
				`<input type="hidden" name="id" value="%d">`+
				`<button class="btn" type="submit">Возобновить</button></form>`,
				esc(w.HaltedReason), w.ID)
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

func watchForm(d store.Watch, mode transport.Mode) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Добавить наблюдение</h2>`)
	b.WriteString(`<form method="post" action="/watches">`)
	b.WriteString(field("Название", "name", d.Name, "text",
		"Как оно будет называться в сообщениях."))
	// Two kinds behind one field, because a person pasting a link does not
	// think of themselves as choosing a kind. The link says which it is, and
	// what is worth saying here is the one case that will not work: a private
	// seller's profile looks exactly like a shop to whoever is looking at it.
	b.WriteString(field("Ссылка на поиск или магазин", "url", d.URL, "text",
		"Страница выдачи avito.ru или магазин (avito.ru/brands/…) — "+
			"скопируйте из адресной строки. За магазином следим целиком: "+
			"версия сайта и число страниц выбираются сами. За частным "+
			"продавцом (avito.ru/user/…) эта версия следить не умеет."))
	b.WriteString(field("Интервал, секунды", "interval",
		strconv.Itoa(d.IntervalSec), "number",
		fmt.Sprintf("Не меньше %d. Чаще минуты Авито отдаёт тот же ответ, "+
			"так что запросы тратятся впустую.", minIntervalSec)))
	b.WriteString(field("Страниц за проход", "pages",
		strconv.Itoa(d.MaxPages), "number",
		fmt.Sprintf("Один запрос на страницу. Не больше %d: заход дальше "+
			"второй страницы — самый громкий сигнал для площадки, "+
			"он превращает 429 в 403, а 403 стоит уже всего пропуска.",
			maxPagesPerPass)))
	// Both of these are ignored for a shop, and deliberately still shown: a
	// disabled pair of fields that reappear when the link changes is more
	// screen machinery than this earns, and the hint on the link field already
	// says the choice is made for you.
	//
	// The two surfaces, and the honest state of both.
	//
	// The label once said mobile carried more cards. It carries thirty against
	// desktop's fifty — that was simply backwards, and it was the only thing
	// on this screen helping somebody choose.
	//
	// What replaced it is measured. Desktop page one is a shell carrying a
	// canonical URL; the transport now follows it, so one desktop page costs
	// two requests and yields fifty listings carrying the seller, the
	// category, the coordinates and the images. Mobile costs one request,
	// yields thirty, and carries none of that.
	//
	// The catch is the transport. In direct mode www.avito.ru met a geetest
	// challenge on request one and m.avito.ru never did, so on the free route
	// the desktop option is not a choice at all — which is why the default
	// follows the mode rather than a preference, and why the hint says which
	// mode the reader is on.
	b.WriteString(`<label class="field"><span class="label">Версия сайта</span>` +
		`<select class="input" name="surface">` +
		fmt.Sprintf(`<option value="desktop"%s>десктопная — два запроса на `+
			`страницу, 50 объявлений, с продавцом и фотографиями</option>`,
			selected(d.Surface == avito.SurfaceDesktop)) +
		fmt.Sprintf(`<option value="mobile"%s>мобильная — один запрос, `+
			`30 объявлений, без продавца</option>`,
			selected(d.Surface != avito.SurfaceDesktop)) +
		`</select>` +
		`<span class="hint">` + surfaceAdvice(mode) + `</span>` +
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
	// Kind comes from the link and was decided in watchFromForm. It used to be
	// stamped "search" here, which was true while search was the only kind and
	// silently wrong the moment it was not.
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
	// The URL decides the kind, and a shop decides its own surface and page
	// count. A seller page is already canonical: one request, no shell hop, no
	// second page — and the mobile spelling of one was never measured, so
	// offering it would be an unmeasured claim in the one place this product
	// promises to watch a competitor.
	parsed, err := checkSearchURL(w.URL)
	if err != nil {
		bad = append(bad, err.Error())
	} else if parsed.Kind == avito.KindSeller {
		w.Kind = string(avito.KindSeller)
		w.Surface = avito.SurfaceDesktop
		w.MaxPages = 1
	} else {
		w.Kind = string(avito.KindSearch)
	}
	if w.IntervalSec < minIntervalSec {
		bad = append(bad, fmt.Sprintf(
			"интервал меньше %d секунд: чаще минуты Авито отдаёт тот же ответ "+
				"с точностью до байта, так что запросы тратятся впустую",
			minIntervalSec))
	}
	if w.Kind != string(avito.KindSeller) &&
		(w.MaxPages < 1 || w.MaxPages > maxPagesPerPass) {
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

// resumeWatch clears a halt from the panel.
//
// The same act as «avitomon watch resume» and deliberately the same store call:
// two spellings of one correction is how they drift apart.
func (s *Server) resumeWatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "нет такого наблюдения", http.StatusBadRequest)
		return
	}
	if _, err := s.Store.ResumeWatch(r.Context(), id, s.now().Unix()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// checkSearchURL refuses anything this program will not fetch, and names what
// it accepted when it accepts.
//
// Checked by the same function that will build the URL to fetch, and that is
// the point. This screen used to accept any *.avito.ru host while
// avito.ParseSearch accepts three — so a watch could save cleanly here and fail
// on every pass afterwards, which is the worst shape a refusal can take: no
// refusal at all, then silence.
//
// The private-seller case is passed through word for word rather than folded
// into the generic message. «Скопируйте ссылку из адресной строки» is useless
// advice to somebody who did exactly that and landed on /user/… — the honest
// answer is that this version watches shops and not people.
func checkSearchURL(raw string) (avito.Search, error) {
	if strings.TrimSpace(raw) == "" {
		return avito.Search{}, errors.New("не задана ссылка")
	}
	parsed, err := avito.ParseSearch(raw)
	switch {
	case errors.Is(err, avito.ErrPrivateSeller):
		return avito.Search{}, err
	case err != nil:
		return avito.Search{}, errors.New(
			"это не похоже на ссылку поиска или магазина Авито: " +
				"скопируйте её из адресной строки со страницы выдачи")
	}
	return parsed, nil
}
