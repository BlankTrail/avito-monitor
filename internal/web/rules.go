// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// Правила: what gets said, to whom, and why something was not said.
//
// Three cards on one screen, and the third is the reason the other two are
// trustworthy. rule_events is the only answer this product has to «почему мне
// не сказали» — every decision, including every refusal, with the reason it was
// refused. Without it a suppressed notification is indistinguishable from a
// broken one, and a person who cannot tell those apart stops believing the
// silence means nothing happened.

// logLimit is how far back the decision log goes on screen.
//
// A hundred, because the question it answers is «почему мне только что не
// сказали» and that answer is always near the top. The rows are kept for as
// long as retention says; this is the window, not the retention.
const logLimit = 100

// targetKind is the only kind of recipient this build has.
//
// A constant and not a dropdown: a select with one option is a question with
// one answer, and the day a second transport exists this becomes a select in
// one place.
const targetKind = "telegram"

func (s *Server) rulesPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	all, err := rules.All(ctx, s.Store)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targets, err := s.Store.Targets(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events, err := s.Store.RuleEvents(ctx, logLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	b.WriteString(ruleList(all, targets))
	b.WriteString(targetList(targets))
	b.WriteString(decisionLog(events, all))

	s.render(w, r, page{Title: "Правила", Body: rawHTML(b.String())})
}

func ruleList(all []rules.Rule, targets []store.TargetRow) string {
	names := map[int64]string{}
	for _, t := range targets {
		names[t.ID] = t.Name
	}

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Правила</h2>`)
	if len(all) == 0 {
		b.WriteString(note("neutral",
			"Ни одного правила. Добавьте наблюдение — вместе с ним создадутся "+
				"обычные правила, — или напишите своё."))
	}
	b.WriteString(`<table><tr><th>Правило</th><th>На что</th><th>Кому</th>` +
		`<th>Состояние</th><th></th></tr>`)
	for _, rule := range all {
		b.WriteString(`<tr><td>`)
		name := rule.Name
		if name == "" {
			name = "правило " + strconv.FormatInt(rule.ID, 10)
		}
		fmt.Fprintf(&b, `<a href="/rules/edit?id=%d">%s</a>`, rule.ID, esc(name))
		if rule.FromDefaults {
			// Said out loud so that deleting one is an informed act. Nothing
			// had read FromDefaults until this line: it exists precisely so a
			// person can tell what they wrote from what the program wrote for
			// them, and can decide to be rid of it.
			b.WriteString(`<br><span class="hint">создано программой</span>`)
		}
		fmt.Fprintf(&b, `</td><td>%s<br><span class="hint">%s</span></td><td>%s</td>`,
			esc(track.Label(rule.Kind)), esc(scopeLabel(rule.Scope)),
			esc(targetNames(rule.Targets, names)))

		b.WriteString(`<td>`)
		if rule.Enabled {
			b.WriteString("включено")
		} else {
			b.WriteString(`<span class="hint">выключено</span>`)
		}
		if err := rule.Validate(); err != nil {
			// A rule that cannot fire looks healthy in a list, and its owner
			// concludes the thing they are watching never changes. Said here
			// as well as in the editor, because the list is what people scan.
			fmt.Fprintf(&b, `<br><span class="bad">не сработает: %s</span>`,
				esc(strings.TrimPrefix(err.Error(), "rules: ")))
		}
		b.WriteString(`</td><td>`)
		fmt.Fprintf(&b, `<form method="post" action="/rules/delete" `+
			`data-confirm="Удалить правило «%s»? Вместе с ним пропадёт весь `+
			`журнал его решений — то есть ответ на вопрос, почему оно о чём-то `+
			`не сообщило.">`+
			`<input type="hidden" name="id" value="%d">`+
			`<button class="btn" type="submit">Удалить</button></form>`,
			attr(name), rule.ID)
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</table>`)
	b.WriteString(`<p><a class="btn" href="/rules/edit">Новое правило</a></p>`)
	b.WriteString(`</div>`)
	return b.String()
}

func targetNames(ids []int64, names map[int64]string) string {
	if len(ids) == 0 {
		return "никому"
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := names[id]; ok {
			out = append(out, name)
			continue
		}
		// A rule pointing at a deleted recipient is not broken — it just has
		// one fewer. Shown as the bare number so it is obviously not a person.
		out = append(out, "№"+strconv.FormatInt(id, 10)+" (нет такого)")
	}
	return strings.Join(out, ", ")
}

func scopeLabel(sc rules.Scope) string {
	switch sc.Kind {
	case rules.ScopeListing:
		return fmt.Sprintf("объявление %d", sc.ID)
	case rules.ScopeThread:
		return fmt.Sprintf("объявление %d и его перевыкладки", sc.ID)
	case rules.ScopeSeller:
		return fmt.Sprintf("продавец %d", sc.ID)
	case rules.ScopeWatch:
		return fmt.Sprintf("наблюдение %d", sc.ID)
	case rules.ScopeFilter:
		f := sc.Filter
		switch {
		case f.PriceMinMinor != 0 && f.PriceMaxMinor != 0:
			return fmt.Sprintf("всё в пределах %s — %s",
				rubles(f.PriceMinMinor), rubles(f.PriceMaxMinor))
		case f.PriceMinMinor != 0:
			return "всё дороже " + rubles(f.PriceMinMinor)
		case f.PriceMaxMinor != 0:
			return "всё дешевле " + rubles(f.PriceMaxMinor)
		}
		return "все наблюдения"
	}
	return string(sc.Kind)
}

// targetList is the recipients: add, silence, delete.
func targetList(targets []store.TargetRow) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Получатели</h2>`)
	if len(targets) == 0 {
		b.WriteString(note("warning",
			"Ни одного получателя — сообщать некому, и ни одно правило "+
				"не сработает."))
	}
	for _, t := range targets {
		b.WriteString(`<form method="post" action="/rules/targets" class="row">`)
		fmt.Fprintf(&b, `<input type="hidden" name="id" value="%d">`, t.ID)
		b.WriteString(field("Имя", "name", t.Name, "text", ""))
		b.WriteString(field("Чат", "address", t.Address, "text", ""))
		// Disabling silences this recipient everywhere at once: the engine
		// intersects a rule's targets with the enabled ones, so one tick stops
		// every rule from writing here without editing any of them.
		b.WriteString(checkbox("Присылать сюда", "enabled", t.Enabled,
			"Снятая галочка молчит во всех правилах сразу."))
		b.WriteString(`<button class="btn" type="submit">Сохранить</button>`)
		b.WriteString(`</form>`)
		fmt.Fprintf(&b, `<form method="post" action="/rules/targets/delete" `+
			`data-confirm="Удалить получателя «%s»? Всё, что для него уже `+
			`поставлено в очередь и ещё не ушло, пропадёт вместе с ним.">`+
			`<input type="hidden" name="id" value="%d">`+
			`<button class="btn" type="submit">Удалить</button></form>`,
			attr(t.Name), t.ID)
	}

	b.WriteString(`<h3>Добавить</h3>`)
	b.WriteString(`<form method="post" action="/rules/targets">`)
	b.WriteString(field("Имя", "name", "", "text", "Как называть его в списках."))
	b.WriteString(field("Чат", "address", "", "text",
		"Числовой id чата в Telegram. Бот должен быть в нём."))
	b.WriteString(`<button class="btn" type="submit">Добавить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

// decisionLog is «что решалось» — including everything that was decided against.
func decisionLog(events []store.RuleEventRow, all []rules.Rule) string {
	names := map[int64]string{}
	for _, r := range all {
		names[r.ID] = r.Name
	}

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Что решалось</h2>`)
	b.WriteString(note("neutral",
		"Здесь видно и то, о чём сообщили, и то, о чём промолчали — "+
			"с причиной. Молчание без причины неотличимо от поломки."))
	if len(events) == 0 {
		b.WriteString(note("neutral", "Пока ничего не решалось."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(`<table><tr><th>Когда</th><th>Правило</th><th>Что</th>` +
		`<th>Решение</th></tr>`)
	for _, e := range events {
		name := names[e.RuleID]
		if name == "" {
			name = "правило " + strconv.FormatInt(e.RuleID, 10)
		}
		subject := fmt.Sprintf("%s %d", subjectWord(e.SubjectKind), e.SubjectKey)
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s<br>`+
			`<span class="hint">%s</span></td><td>%s</td></tr>`,
			esc(when(e.FiredAt)), esc(name),
			esc(track.Label(track.Kind(e.Kind))), esc(subject),
			decisionWords(e.SuppressedBy))
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

func subjectWord(kind string) string {
	switch kind {
	case "listing":
		return "объявление"
	case "thread":
		return "объявление (сквозной номер)"
	case "seller":
		return "продавец"
	case "watch":
		return "наблюдение"
	}
	return kind
}

func when(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04")
}

// decisionWords is the six refusals and the one success, in Russian.
//
// Written out rather than printed as the stored key, because «rate-limit» in a
// «почему мне не сказали» column is an answer only to whoever wrote this code.
// A reason this build does not know is shown as itself rather than swallowed:
// a row from a newer release must not read as «отправлено».
func decisionWords(reason string) string {
	switch rules.Reason(reason) {
	case rules.ReasonNone:
		return `<strong>отправлено</strong>`
	case rules.ReasonSeed:
		return "промолчали: первое наполнение — объявление было в выдаче ещё " +
			"до того, как за ней начали следить"
	case rules.ReasonRepost:
		return "промолчали: то же объявление под новым номером"
	case rules.ReasonThreshold:
		return "промолчали: изменение меньше порога"
	case rules.ReasonDuplicate:
		return "промолчали: об этом же уже сообщали"
	case rules.ReasonRateLimit:
		return "промолчали: слишком часто про один и тот же предмет"
	case rules.ReasonQuietHours:
		return "промолчали: тихие часы"
	}
	return "промолчали: " + esc(reason)
}

// ruleEditor draws one rule.
func (s *Server) ruleEditor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)

	rule := rules.Rule{
		Kind:    track.KindPriceChanged,
		Scope:   rules.Scope{Kind: rules.ScopeFilter},
		Enabled: true,
	}
	if id > 0 {
		all, err := rules.All(ctx, s.Store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		found := false
		for _, candidate := range all {
			if candidate.ID == id {
				rule, found = candidate, true
			}
		}
		if !found {
			http.Error(w, "нет такого правила", http.StatusNotFound)
			return
		}
	}
	s.renderEditor(w, r, rule, nil)
}

func (s *Server) renderEditor(w http.ResponseWriter, r *http.Request,
	rule rules.Rule, problems []string) {

	ctx := r.Context()
	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	targets, err := s.Store.Targets(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, page{
		Title: "Правило",
		Body:  rawHTML(ruleForm(rule, watches, targets, problems)),
	})
}

func ruleForm(rule rules.Rule, watches []store.Watch,
	targets []store.TargetRow, problems []string) string {

	var b strings.Builder
	b.WriteString(`<div class="card">`)
	if rule.ID == 0 {
		b.WriteString(`<h2>Новое правило</h2>`)
	} else {
		b.WriteString(`<h2>Правило</h2>`)
	}
	// Every reason at once. Validate produces them all for exactly this
	// reason: a form that rejects one field at a time is a form somebody
	// submits five times.
	for _, p := range problems {
		b.WriteString(note("bad", p))
	}
	if rule.FromDefaults {
		b.WriteString(note("neutral",
			"Это правило создала программа вместе с наблюдением. "+
				"Его можно менять и удалять — оно не вернётся."))
	}

	b.WriteString(`<form method="post" action="/rules">`)
	fmt.Fprintf(&b, `<input type="hidden" name="id" value="%d">`, rule.ID)
	fmt.Fprintf(&b, `<input type="hidden" name="from_defaults" value="%s">`,
		attr(boolField(rule.FromDefaults)))

	b.WriteString(field("Название", "name", rule.Name, "text",
		"Для себя — оно попадёт в журнал решений."))

	b.WriteString(`<label class="field"><span class="label">О чём сообщать</span>` +
		`<select class="input" name="kind">`)
	for _, k := range track.Kinds() {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(string(k)), selected(rule.Kind == k), esc(track.Label(k)))
	}
	b.WriteString(`</select><span class="hint">Одно правило — один вид ` +
		`изменения: порог считается в единицах этого вида.</span></label>`)

	b.WriteString(scopeSelect(rule.Scope, watches))
	b.WriteString(conditionEditor(rule.Condition))

	b.WriteString(`<h3>Когда молчать</h3>`)
	b.WriteString(field("Порог, %", "threshold_pct",
		intField(int64(rule.ThresholdPct)), "number",
		"Меньшее изменение не стоит сообщения. 0 — сообщать о любом."))
	b.WriteString(field("Порог, ₽", "threshold_rub",
		intField(rule.ThresholdMinor/100), "number",
		"То же, но в рублях. Хватит любого из двух."))
	b.WriteString(field("Не чаще чем раз в, минут", "interval_min",
		intField(int64(rule.MinInterval/time.Minute)), "number",
		"Про один и тот же предмет. 0 — без ограничения."))

	b.WriteString(`<h3>Как сообщать</h3>`)
	b.WriteString(checkbox("Срочно", "urgent", rule.Urgent,
		"Единственное, что это меняет — правило пробивает тихие часы."))
	b.WriteString(checkbox("Собирать в одно сообщение", "aggregate", rule.Aggregate,
		"Вместо сорока сообщений подряд — одно с сорока строками."))
	b.WriteString(checkbox("Включено", "enabled", rule.Enabled, ""))

	b.WriteString(`<h3>Кому</h3>`)
	if len(targets) == 0 {
		b.WriteString(note("warning",
			"Получателей нет. Правило без получателя не сохранится — "+
				"добавьте его на странице «Правила»."))
	}
	chosen := map[int64]bool{}
	for _, id := range rule.Targets {
		chosen[id] = true
	}
	for _, t := range targets {
		label := t.Name
		if !t.Enabled {
			label += " (выключен — молчит во всех правилах)"
		}
		fmt.Fprintf(&b,
			`<label class="check"><input type="checkbox" name="target" `+
				`value="%d"%s><span>%s</span></label>`,
			t.ID, checkedAttr(chosen[t.ID]), esc(label))
	}

	b.WriteString(`<button class="btn" type="submit">Сохранить</button>`)
	b.WriteString(` <a class="btn" href="/rules">Назад</a>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

// scopeSelect collapses the common scopes into one control.
//
// A watch is picked by name because that is how a person thinks of it; the
// other three are numbers because that is what they are, and the number goes in
// its own field. The alternative — one select for the kind and a second control
// that changes shape with it — needs JavaScript to be usable, and this screen
// works without any.
func scopeSelect(sc rules.Scope, watches []store.Watch) string {
	value := string(sc.Kind)
	if sc.Kind == rules.ScopeWatch {
		value = "watch:" + strconv.FormatInt(sc.ID, 10)
	}

	var b strings.Builder
	b.WriteString(`<label class="field"><span class="label">За чем следить</span>` +
		`<select class="input" name="scope">`)
	fmt.Fprintf(&b, `<option value="filter"%s>всё, что попадает под фильтр ниже</option>`,
		selected(value == "filter"))
	for _, watch := range watches {
		v := "watch:" + strconv.FormatInt(watch.ID, 10)
		fmt.Fprintf(&b, `<option value="%s"%s>наблюдение «%s»</option>`,
			attr(v), selected(value == v), esc(watch.Name))
	}
	for _, o := range []struct{ key, label string }{
		{"listing", "одно объявление (номер ниже)"},
		{"thread", "объявление и его перевыкладки (сквозной номер ниже)"},
		{"seller", "один продавец (номер ниже)"},
	} {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(o.key), selected(value == o.key), esc(o.label))
	}
	b.WriteString(`</select></label>`)

	id := int64(0)
	if sc.Kind != rules.ScopeWatch {
		id = sc.ID
	}
	b.WriteString(field("Номер", "scope_id", intField(id), "number",
		"Нужен только для трёх последних вариантов."))
	b.WriteString(field("Цена от, ₽", "filter_min",
		intField(sc.Filter.PriceMinMinor/100), "number", "Только для фильтра."))
	b.WriteString(field("Цена до, ₽", "filter_max",
		intField(sc.Filter.PriceMaxMinor/100), "number", "Только для фильтра."))
	b.WriteString(checkbox("Считать и те, у кого цены нет", "filter_unpriced",
		sc.Filter.IncludeUnpriced,
		"«Договорная» и непрочитанная цена — не ноль, и по умолчанию "+
			"ценовой фильтр их не пропускает."))
	return b.String()
}

// conditionEditor is one And of comparisons, and says so.
//
// Deliberately small. The tree supports nested groups and this offers one
// group; deeper conditions are storable, work, and are edited by hand. A rule
// whose stored condition is deeper than this can draw is shown read-only rather
// than flattened, because flattening would change what it fires on without
// telling anybody — which is the one thing a rule editor must never do.
func conditionEditor(n rules.Node) string {
	var b strings.Builder
	b.WriteString(`<h3>При каком условии</h3>`)

	if !editableCondition(n) {
		b.WriteString(note("warning",
			"Условие этого правила сложнее, чем умеет показать эта страница: "+
				"в нём есть вложенные группы. Оно работает и сохранится как "+
				"есть — здесь его не показать и не изменить, чтобы не "+
				"превратить его молча во что-то другое. Менять такое пока "+
				"можно только в файле базы."))
		fmt.Fprintf(&b, `<input type="hidden" name="condition_untouched" value="1">`)
		return b.String()
	}

	b.WriteString(note("neutral",
		"Условие собирается из простых сравнений, соединённых «и». "+
			"Пустая строка не учитывается; ни одного сравнения — "+
			"правило срабатывает на каждое изменение своего вида."))

	leaves := append([]rules.Node(nil), n.Nodes...)
	// Three spare rows, so adding a second comparison does not need a save
	// first. Empty ones are dropped on the way in.
	for i := 0; i < 3; i++ {
		leaves = append(leaves, rules.Node{Op: rules.OpCompare})
	}
	for _, leaf := range leaves {
		b.WriteString(conditionRow(leaf))
	}
	return b.String()
}

func conditionRow(leaf rules.Node) string {
	var b strings.Builder
	b.WriteString(`<div class="row">`)
	b.WriteString(`<select class="input" name="leaf_field">`)
	fmt.Fprintf(&b, `<option value=""%s>—</option>`, selected(leaf.Field == ""))
	for _, f := range rules.Fields() {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(string(f)), selected(leaf.Field == f), esc(rules.FieldLabel(f)))
	}
	b.WriteString(`</select>`)

	b.WriteString(`<select class="input" name="leaf_cmp">`)
	for _, c := range []rules.Cmp{
		rules.CmpLess, rules.CmpLessOrEq, rules.CmpGreater,
		rules.CmpGreaterOrEq, rules.CmpEqual, rules.CmpNotEqual,
	} {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			attr(string(c)), selected(leaf.Cmp == c), esc(string(c)))
	}
	b.WriteString(`</select>`)

	value := ""
	if leaf.Field != "" {
		value = strconv.FormatFloat(leaf.Value, 'f', -1, 64)
	}
	fmt.Fprintf(&b,
		`<input class="input" type="text" name="leaf_value" value="%s">`,
		attr(value))
	b.WriteString(`</div>`)
	return b.String()
}

// editableCondition reports whether this screen can draw the tree honestly.
func editableCondition(n rules.Node) bool {
	if n.Op != rules.OpAnd && n.Op != "" {
		return false
	}
	for _, kid := range n.Nodes {
		if kid.Op != rules.OpCompare {
			return false
		}
	}
	return true
}

// saveRule reads the form and writes the rule.
func (s *Server) saveRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rule, problems := ruleFromForm(r)

	// A condition this screen refused to draw is carried across from the
	// stored rule rather than rebuilt from a form that never showed it.
	// Without this, saving a name change would silently replace a nested
	// condition with an empty one — which fires on everything.
	if r.FormValue("condition_untouched") == "1" && rule.ID > 0 {
		all, err := rules.All(r.Context(), s.Store)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, stored := range all {
			if stored.ID == rule.ID {
				rule.Condition = stored.Condition
			}
		}
	}

	if len(problems) == 0 {
		if _, err := rules.Save(r.Context(), s.Store, rule); err != nil {
			problems = strings.Split(
				strings.TrimPrefix(err.Error(), "rules: "), "; ")
		}
	}
	if len(problems) > 0 {
		// Re-drawn from what was submitted rather than from the store, so
		// nothing typed is lost to a refusal.
		s.renderEditor(w, r, rule, problems)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func ruleFromForm(r *http.Request) (rules.Rule, []string) {
	var problems []string
	intOf := func(name string) int64 {
		v, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(name)), 10, 64)
		return v
	}

	rule := rules.Rule{
		ID:           intOf("id"),
		Name:         strings.TrimSpace(r.FormValue("name")),
		Kind:         track.Kind(r.FormValue("kind")),
		Urgent:       ticked(r, "urgent"),
		Aggregate:    ticked(r, "aggregate"),
		Enabled:      ticked(r, "enabled"),
		FromDefaults: r.FormValue("from_defaults") == "1",

		ThresholdPct:   int(intOf("threshold_pct")),
		ThresholdMinor: intOf("threshold_rub") * 100,
		MinInterval:    time.Duration(intOf("interval_min")) * time.Minute,
	}

	scope := r.FormValue("scope")
	switch {
	case strings.HasPrefix(scope, "watch:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(scope, "watch:"), 10, 64)
		rule.Scope = rules.Scope{Kind: rules.ScopeWatch, ID: id}
	case scope == "listing" || scope == "thread" || scope == "seller":
		rule.Scope = rules.Scope{Kind: rules.ScopeKind(scope), ID: intOf("scope_id")}
	default:
		rule.Scope = rules.Scope{Kind: rules.ScopeFilter, Filter: rules.Filter{
			PriceMinMinor:   intOf("filter_min") * 100,
			PriceMaxMinor:   intOf("filter_max") * 100,
			IncludeUnpriced: ticked(r, "filter_unpriced"),
		}}
	}

	for _, raw := range r.Form["target"] {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			rule.Targets = append(rule.Targets, id)
		}
	}

	// The condition is left alone when the editor refused to draw it; saveRule
	// copies the stored one back over this zero value.
	if r.FormValue("condition_untouched") == "1" {
		return rule, problems
	}

	condition := rules.Node{Op: rules.OpAnd}
	fields := r.Form["leaf_field"]
	cmps := r.Form["leaf_cmp"]
	values := r.Form["leaf_value"]
	for i, field := range fields {
		if strings.TrimSpace(field) == "" {
			continue
		}
		leaf := rules.Node{Op: rules.OpCompare, Field: rules.Field(field)}
		if i < len(cmps) {
			leaf.Cmp = rules.Cmp(cmps[i])
		}
		raw := ""
		if i < len(values) {
			raw = strings.TrimSpace(values[i])
		}
		v, err := strconv.ParseFloat(strings.Replace(raw, ",", ".", 1), 64)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"«%s» — не число (условие: %s)", raw, rules.FieldLabel(leaf.Field)))
			continue
		}
		leaf.Value = v
		condition.Nodes = append(condition.Nodes, leaf)
	}
	rule.Condition = condition
	return rule, problems
}

// deleteRule removes one, with its log.
func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "нет такого правила", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteRule(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) saveTarget(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	address := strings.TrimSpace(r.FormValue("address"))
	if address == "" {
		http.Error(w, "не указан чат", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = address
	}
	// A new recipient is enabled: somebody who just typed an address wants
	// messages there. An existing one keeps what the tick says, which is how
	// one can be silenced everywhere at once.
	enabled := true
	if id > 0 {
		enabled = ticked(r, "enabled")
	}
	if _, err := s.Store.SaveTarget(r.Context(), store.TargetRow{
		ID: id, Name: name, Kind: targetKind, Address: address, Enabled: enabled,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func (s *Server) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "нет такого получателя", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteTarget(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rules", http.StatusSeeOther)
}

func intField(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func boolField(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func checkedAttr(on bool) string {
	if on {
		return " checked"
	}
	return ""
}
