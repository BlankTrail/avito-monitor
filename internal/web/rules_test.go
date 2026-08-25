// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func aTarget(t *testing.T, s *Server, name, address string, enabled bool) int64 {
	t.Helper()
	id, err := s.Store.SaveTarget(context.Background(), store.TargetRow{
		Name: name, Kind: "telegram", Address: address, Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ruleForm is a complete, valid submission, for a test to spoil one field of.
func ruleFormValues(targetID int64) url.Values {
	return url.Values{
		"id":         {"0"},
		"name":       {"дешевеет"},
		"kind":       {string(track.KindPriceChanged)},
		"scope":      {"filter"},
		"enabled":    {"0", "1"},
		"target":     {strconv.FormatInt(targetID, 10)},
		"leaf_field": {string(rules.FieldPricePct), ""},
		"leaf_cmp":   {string(rules.CmpLess), string(rules.CmpLess)},
		"leaf_value": {"-5", ""},
	}
}

func rulesOf(t *testing.T, s *Server) []rules.Rule {
	t.Helper()
	all, err := rules.All(context.Background(), s.Store)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// TestTheRulesScreenIsReachableAndTabbed.
func TestTheRulesScreenIsReachableAndTabbed(t *testing.T) {
	s := newServer(t)
	if rec := get(t, s, "/rules"); rec.Code != http.StatusOK {
		t.Fatalf("GET /rules = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(get(t, s, "/").Body.String(), `href="/rules"`) {
		t.Error("Правила is not in the header navigation")
	}
}

// TestARuleIsWrittenFromTheBrowser, which is the point of the screen.
func TestARuleIsWrittenFromTheBrowser(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)

	rec := post(t, s, "/rules", ruleFormValues(id))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /rules = %d (%s)", rec.Code, rec.Body.String())
	}
	all := rulesOf(t, s)
	if len(all) != 1 {
		t.Fatalf("%d rules saved", len(all))
	}
	got := all[0]
	if got.Name != "дешевеет" || got.Kind != track.KindPriceChanged {
		t.Errorf("rule = %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0] != id {
		t.Errorf("targets = %v", got.Targets)
	}
	if !got.Enabled {
		t.Error("the rule was saved disabled")
	}
	if err := got.Validate(); err != nil {
		t.Errorf("the saved rule cannot fire: %v", err)
	}
}

// TestARuleThatCannotMatchIsRefusedWithEveryReasonAtOnce.
//
// Validate produces them all for exactly this reason. A form that rejects one
// field at a time is a form somebody submits five times, and a rule that cannot
// fire is worse than no rule: it sits in the list looking healthy while its
// owner concludes nothing ever changes.
func TestARuleThatCannotMatchIsRefusedWithEveryReasonAtOnce(t *testing.T) {
	s := newServer(t)

	form := ruleFormValues(0)
	form.Del("target")                  // no recipient
	form.Set("kind", "нет-такого-вида") // a kind nothing emits
	form.Set("scope", "listing")        // a subject scope...
	form.Set("scope_id", "0")           // ...with no subject
	form.Set("threshold_pct", "-5")     // a negative floor

	rec := post(t, s, "/rules", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("a rule that cannot fire was accepted: %d", rec.Code)
	}
	body := rec.Body.String()
	// Phrases only Validate produces. «Порог» and «получатель» also appear as
	// labels on the re-drawn form, so matching those would pass on a screen
	// that reported nothing at all.
	for _, want := range []string{
		"(kind)", "(target)", "не указан предмет", "не может быть отрицательным",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not mention %q: %s", want, body)
		}
	}
	if n := strings.Count(body, `class="note bad"`); n < 4 {
		t.Errorf("%d reasons shown at once, want every one", n)
	}
	if n := len(rulesOf(t, s)); n != 0 {
		t.Errorf("%d rules stored despite the refusal", n)
	}
}

// TestARefusalKeepsWhatWasTyped, so a person does not retype a form to find out
// the second reason it was refused.
func TestARefusalKeepsWhatWasTyped(t *testing.T) {
	s := newServer(t)
	form := ruleFormValues(0)
	form.Set("name", "моё правило")
	form.Del("target")

	body := post(t, s, "/rules", form).Body.String()
	if !strings.Contains(body, "моё правило") {
		t.Error("the name was lost to the refusal")
	}
	if !strings.Contains(body, `value="-5"`) {
		t.Error("the condition was lost to the refusal")
	}
}

// TestARuleWithNoTargetIsRefusedAndSaysSo.
func TestARuleWithNoTargetIsRefusedAndSaysSo(t *testing.T) {
	s := newServer(t)
	form := ruleFormValues(0)
	form.Del("target")

	rec := post(t, s, "/rules", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "не выбран получатель") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
}

// TestTheConditionEditorRoundTripsATwoLeafAnd.
//
// That is the shape the price defaults use. A tree that came back different
// would fire on something other than what its owner wrote — and they would find
// out from a notification that did not arrive.
func TestTheConditionEditorRoundTripsATwoLeafAnd(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)

	form := ruleFormValues(id)
	form["leaf_field"] = []string{
		string(rules.FieldPricePct), string(rules.FieldNow), ""}
	form["leaf_cmp"] = []string{
		string(rules.CmpLess), string(rules.CmpGreaterOrEq), string(rules.CmpLess)}
	form["leaf_value"] = []string{"-5", "1000000", ""}

	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	saved := rulesOf(t, s)[0]
	if saved.Condition.Op != rules.OpAnd || len(saved.Condition.Nodes) != 2 {
		t.Fatalf("condition = %+v", saved.Condition)
	}
	first, second := saved.Condition.Nodes[0], saved.Condition.Nodes[1]
	if first.Field != rules.FieldPricePct || first.Cmp != rules.CmpLess || first.Value != -5 {
		t.Errorf("first leaf = %+v", first)
	}
	if second.Field != rules.FieldNow || second.Value != 1_000_000 {
		t.Errorf("second leaf = %+v", second)
	}

	// And the editor draws it back with both leaves and their values.
	body := get(t, s, "/rules/edit?id="+strconv.FormatInt(saved.ID, 10)).Body.String()
	if !strings.Contains(body, `value="-5"`) || !strings.Contains(body, `value="1000000"`) {
		t.Errorf("the editor lost a leaf: %s", body)
	}
}

// TestAConditionDeeperThanTheEditorIsShownReadOnlyAndSurvivesASave.
//
// Flattening it would change what the rule fires on without telling anybody,
// which is the one thing a rule editor must never do.
func TestAConditionDeeperThanTheEditorIsShownReadOnlyAndSurvivesASave(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aTarget(t, s, "я", "100500", true)

	deep := rules.Node{Op: rules.OpAnd, Nodes: []rules.Node{
		{Op: rules.OpOr, Nodes: []rules.Node{
			{Op: rules.OpCompare, Field: rules.FieldPricePct, Cmp: rules.CmpLess, Value: -5},
			{Op: rules.OpCompare, Field: rules.FieldPricePct, Cmp: rules.CmpGreater, Value: 20},
		}},
	}}
	ruleID, err := rules.Save(ctx, s.Store, rules.Rule{
		Name: "сложное", Kind: track.KindPriceChanged, Condition: deep,
		Scope: rules.Scope{Kind: rules.ScopeFilter}, Targets: []int64{id}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/rules/edit?id="+strconv.FormatInt(ruleID, 10)).Body.String()
	if !strings.Contains(body, "сложнее") {
		t.Errorf("the editor did not say it cannot draw this condition: %s", body)
	}
	if !strings.Contains(body, `name="condition_untouched"`) {
		t.Error("nothing marks the condition as carried through untouched")
	}
	if strings.Contains(body, `name="leaf_field"`) {
		t.Error("the editor drew leaves for a tree it cannot represent")
	}

	// Saving a name change must not replace the condition with an empty one,
	// which would fire on every price change there is.
	form := ruleFormValues(id)
	form.Set("id", strconv.FormatInt(ruleID, 10))
	form.Set("name", "переименовано")
	form.Set("condition_untouched", "1")
	form.Del("leaf_field")
	form.Del("leaf_cmp")
	form.Del("leaf_value")
	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	saved := rulesOf(t, s)[0]
	if saved.Name != "переименовано" {
		t.Errorf("name = %q", saved.Name)
	}
	if len(saved.Condition.Nodes) != 1 || saved.Condition.Nodes[0].Op != rules.OpOr {
		t.Errorf("the nested condition was flattened: %+v", saved.Condition)
	}
}

// TestTheNineDefaultsAreMarkedAsSuchOnScreen.
//
// FromDefaults exists so a person can tell what they wrote from what the
// program wrote for them. Nothing had read it until this screen, and without it
// deleting one is a surprise rather than a decision.
func TestTheNineDefaultsAreMarkedAsSuchOnScreen(t *testing.T) {
	s := newServer(t)
	aTarget(t, s, "я", "100500", true)
	aWatch(t, s, "наблюдение") // seeds the defaults

	all := rulesOf(t, s)
	if len(all) == 0 {
		t.Fatal("adding a watch seeded no rules")
	}
	marked := 0
	for _, r := range all {
		if r.FromDefaults {
			marked++
		}
	}
	if marked == 0 {
		t.Fatal("no seeded rule carries FromDefaults")
	}
	body := get(t, s, "/rules").Body.String()
	if !strings.Contains(body, "создано программой") {
		t.Errorf("the screen does not mark the seeded rules: %s", body)
	}
	if n := strings.Count(body, "создано программой"); n != marked {
		t.Errorf("%d rules marked on screen, %d carry the flag", n, marked)
	}
}

// TestDeletingARuleAsksAndSaysWhatGoesWithIt.
//
// The schema cascades rule_events, which is the only answer this product has to
// «почему мне не сказали». That is the right behaviour and not something to let
// somebody discover afterwards.
func TestDeletingARuleAsksAndSaysWhatGoesWithIt(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aTarget(t, s, "я", "100500", true)
	ruleID, err := rules.Save(ctx, s.Store, rules.Rule{
		Name: "правило", Kind: track.KindPriceChanged,
		Scope: rules.Scope{Kind: rules.ScopeFilter}, Targets: []int64{id}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: ruleID, FiredAt: 1_700_000_000, Kind: string(track.KindPriceChanged),
		SubjectKind: "listing", SubjectKey: 100, DedupKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/rules").Body.String()
	if !strings.Contains(body, "data-confirm") {
		t.Error("deleting a rule does not ask")
	}
	if !strings.Contains(body, "журнал его решений") {
		t.Errorf("the confirmation does not say the log goes too: %s", body)
	}

	rec := post(t, s, "/rules/delete",
		url.Values{"id": {strconv.FormatInt(ruleID, 10)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	if n := len(rulesOf(t, s)); n != 0 {
		t.Errorf("%d rules left", n)
	}
	events, err := s.Store.RuleEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("%d log rows survived the rule they point at", len(events))
	}
}

// TestTheLogShowsWhySomethingWasNotSaid, in Russian, for every reason.
//
// The six refusals and the one success. «rate-limit» in a «почему мне не
// сказали» column is an answer only to whoever wrote this code.
func TestTheLogShowsWhySomethingWasNotSaid(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aTarget(t, s, "я", "100500", true)
	ruleID, err := rules.Save(ctx, s.Store, rules.Rule{
		Name: "правило", Kind: track.KindPriceChanged,
		Scope: rules.Scope{Kind: rules.ScopeFilter}, Targets: []int64{id}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	all := []rules.Reason{
		rules.ReasonNone, rules.ReasonSeed, rules.ReasonRepost,
		rules.ReasonThreshold, rules.ReasonDuplicate, rules.ReasonRateLimit,
		rules.ReasonQuietHours,
	}
	for i, reason := range all {
		if _, err := s.Store.RecordRuleEvent(ctx, store.RuleEventRow{
			RuleID: ruleID, FiredAt: int64(1_700_000_000 + i),
			Kind:        string(track.KindPriceChanged),
			SubjectKind: "listing", SubjectKey: int64(100 + i),
			SuppressedBy: string(reason),
			DedupKey:     "k" + strconv.Itoa(i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	body := get(t, s, "/rules").Body.String()
	for _, want := range []string{
		"отправлено",
		"первое наполнение",
		"под новым номером",
		"меньше порога",
		"уже сообщали",
		"слишком часто",
		"тихие часы",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the log does not explain %q: %s", want, body)
		}
	}
	// And no raw key leaks into the column a person reads.
	for _, key := range []string{"rate-limit", "quiet-hours", "suppressed"} {
		if strings.Contains(body, key) {
			t.Errorf("the log shows the stored key %q", key)
		}
	}
}

// TestATargetCanBeAddedDisabledAndDeleted, and disabling one silences it
// everywhere at once — the engine intersects, and this is the screen for it.
func TestATargetCanBeAddedDisabledAndDeleted(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()

	rec := post(t, s, "/rules/targets",
		url.Values{"name": {"мой чат"}, "address": {"100500"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("adding = %d (%s)", rec.Code, rec.Body.String())
	}
	targets, err := s.Store.Targets(ctx)
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets = %+v (%v)", targets, err)
	}
	if !targets[0].Enabled {
		t.Error("a freshly typed recipient arrived silenced")
	}
	id := targets[0].ID

	// Silenced. The engine reads EnabledTargets, so this must remove it there
	// without touching a single rule.
	rec = post(t, s, "/rules/targets", url.Values{
		"id":   {strconv.FormatInt(id, 10)},
		"name": {"мой чат"}, "address": {"100500"},
		"enabled": {"0"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("disabling = %d (%s)", rec.Code, rec.Body.String())
	}
	enabled, err := s.Store.EnabledTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Errorf("a disabled recipient is still enabled: %v", enabled)
	}

	// And back on, through the pair a browser actually posts.
	rec = post(t, s, "/rules/targets", url.Values{
		"id":   {strconv.FormatInt(id, 10)},
		"name": {"мой чат"}, "address": {"100500"},
		"enabled": {"0", "1"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("re-enabling = %d (%s)", rec.Code, rec.Body.String())
	}
	if enabled, _ = s.Store.EnabledTargets(ctx); len(enabled) != 1 {
		t.Errorf("re-enabling did not take: %v", enabled)
	}

	if !strings.Contains(get(t, s, "/rules").Body.String(), "поставлено в очередь") {
		t.Error("deleting a recipient does not say the queue goes with it")
	}
	rec = post(t, s, "/rules/targets/delete",
		url.Values{"id": {strconv.FormatInt(id, 10)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("deleting = %d (%s)", rec.Code, rec.Body.String())
	}
	if targets, _ = s.Store.Targets(ctx); len(targets) != 0 {
		t.Errorf("%d recipients left", len(targets))
	}
}

// TestARuleNameIsEscaped. It is typed by a person and lands in a confirmation
// attribute, a table cell and a log column.
func TestARuleNameIsEscaped(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)
	form := ruleFormValues(id)
	form.Set("name", `<img src=x onerror=alert(1)>`)
	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	body := get(t, s, "/rules").Body.String()
	if strings.Contains(body, "<img src=x") {
		t.Error("a typed name reached the page as markup")
	}
	if !strings.Contains(body, "&lt;img") {
		t.Errorf("the name is not on the page: %s", body)
	}
}

// TestAScopeIsPickedByNameForAWatchAndByNumberForTheRest.
//
// A watch is a thing a person named; the other three are numbers. Making all
// four a number would mean looking up a watch id to write a rule about it.
func TestAScopeIsPickedByNameForAWatchAndByNumberForTheRest(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)
	watchID := aWatch(t, s, "мой поиск")

	body := get(t, s, "/rules/edit").Body.String()
	if !strings.Contains(body, "наблюдение «мой поиск»") {
		t.Errorf("the watch is not offered by name: %s", body)
	}

	form := ruleFormValues(id)
	form.Set("scope", "watch:"+strconv.FormatInt(watchID, 10))
	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	var saved rules.Rule
	for _, r := range rulesOf(t, s) {
		if r.Name == "дешевеет" {
			saved = r
		}
	}
	if saved.Scope.Kind != rules.ScopeWatch || saved.Scope.ID != watchID {
		t.Errorf("scope = %+v", saved.Scope)
	}
}

// TestAThresholdIsTypedInRoublesAndStoredInKopecks, the same as everywhere else
// a person types money into this panel.
func TestAThresholdIsTypedInRoublesAndStoredInKopecks(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)
	form := ruleFormValues(id)
	form.Set("threshold_rub", "500")
	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rulesOf(t, s)[0].ThresholdMinor; got != 50_000 {
		t.Errorf("ThresholdMinor = %d, want 500 ₽ in kopecks", got)
	}
}

// TestAConditionValueThatIsNotANumberIsRefusedAndNamed.
//
// Dropped silently, the rule would be saved without that comparison — which
// makes it fire on far more than its owner asked for.
func TestAConditionValueThatIsNotANumberIsRefusedAndNamed(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)
	form := ruleFormValues(id)
	form["leaf_value"] = []string{"пять процентов", ""}

	rec := post(t, s, "/rules", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "пять процентов") {
		t.Errorf("the refusal does not name the value: %s", rec.Body.String())
	}
	if n := len(rulesOf(t, s)); n != 0 {
		t.Errorf("%d rules stored despite the refusal", n)
	}
}

// TestEditingARuleUpdatesItRatherThanAddingASecond.
//
// SaveRule updates on the id precisely so rule_events survive an edit. A screen
// that posted a new rule every time would orphan that history and restart the
// rate limit.
func TestEditingARuleUpdatesItRatherThanAddingASecond(t *testing.T) {
	s := newServer(t)
	id := aTarget(t, s, "я", "100500", true)
	if rec := post(t, s, "/rules", ruleFormValues(id)); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	saved := rulesOf(t, s)[0]

	form := ruleFormValues(id)
	form.Set("id", strconv.FormatInt(saved.ID, 10))
	form.Set("name", "иначе")
	if rec := post(t, s, "/rules", form); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	all := rulesOf(t, s)
	if len(all) != 1 {
		t.Fatalf("%d rules after an edit", len(all))
	}
	if all[0].ID != saved.ID || all[0].Name != "иначе" {
		t.Errorf("rule = %+v", all[0])
	}
}
