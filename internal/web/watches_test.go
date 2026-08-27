// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func addWatchForm(name, u string, intervalSec int) url.Values {
	return url.Values{
		"name": {name}, "url": {u},
		"interval": {strconv.Itoa(intervalSec)},
		"pages":    {"1"}, "surface": {"mobile"},
	}
}

// TestAWatchIsAddedFromTheBrowser, which is the whole point of this screen.
func TestAWatchIsAddedFromTheBrowser(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /watches = %d (%s)", rec.Code, rec.Body.String())
	}
	ws, err := s.Store.Watches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Name != "iphone" || ws[0].IntervalSec != 300 {
		t.Fatalf("stored %+v", ws)
	}
	if !strings.Contains(get(t, s, "/").Body.String(), "iphone") {
		t.Error("the new watch is not on the screen")
	}
}

// TestAScheduleOverTheBudgetIsRefusedWithTheSumShown.
//
// The spec's own words. One address gives six requests a minute; twelve watches
// at one page every two minutes is exactly that, so a thirteenth cannot fit.
func TestAScheduleOverTheBudgetIsRefusedWithTheSumShown(t *testing.T) {
	s := newServer(t)
	for i := 1; i <= 12; i++ {
		n := strconv.Itoa(i)
		if rec := post(t, s, "/watches",
			addWatchForm("n"+n, "https://m.avito.ru/all?q="+n, 120)); rec.Code != http.StatusSeeOther {
			t.Fatalf("watch %d was refused: %s", i, rec.Body.String())
		}
	}
	rec := post(t, s, "/watches",
		addWatchForm("лишнее", "https://m.avito.ru/all?q=z", 120))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a schedule over the ceiling was accepted")
	}
	body := rec.Body.String()
	for _, want := range []string{"потолок", "адрес", "запр./мин"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not carry %q", want)
		}
	}
}

// TestAnIntervalUnderTheFloorIsRefusedWithTheReason.
//
// Measured 2026-08-24: within about a minute Avito returns the same page byte
// for byte, total_count included. Polling faster spends requests and collects
// nothing, so the refusal says that rather than «слишком часто».
func TestAnIntervalUnderTheFloorIsRefusedWithTheReason(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches",
		addWatchForm("часто", "https://m.avito.ru/all?q=a", 30))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a 30-second interval was accepted")
	}
	if !strings.Contains(rec.Body.String(), "тот же ответ") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
}

// TestAnUrlThatIsNotAvitoIsRefused, before a request is ever made to it.
func TestAnUrlThatIsNotAvitoIsRefused(t *testing.T) {
	s := newServer(t)
	for _, bad := range []string{
		"", "не ссылка", "https://example.com/all?q=a", "ftp://avito.ru/x",
		"https://avito.ru.example.com/x",
	} {
		rec := post(t, s, "/watches", addWatchForm("плохое", bad, 300))
		if rec.Code == http.StatusSeeOther {
			t.Errorf("%q was accepted", bad)
		}
	}
	// And the real thing is not refused by an over-eager check.
	for _, good := range []string{
		"https://avito.ru/all?q=a", "https://m.avito.ru/all?q=a",
		"https://www.avito.ru/moskva/telefony?q=a",
	} {
		if err := checkSearchURL(good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
}

// TestAddingAWatchCreatesItsDefaultRulesWhenThereIsSomebodyToTell.
//
// The command-line path already does this (cmd/avitomon/seed.go). Adding one
// from the browser must not be the path that silently produces a watch nobody
// hears about.
func TestAddingAWatchCreatesItsDefaultRulesWhenThereIsSomebodyToTell(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.Store.SaveTarget(ctx, store.TargetRow{
		Name: "мне", Kind: "telegram", Address: "42", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300)); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	rows, err := s.Store.Rules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 {
		t.Errorf("%d rules created, want the four per-watch and the five global", len(rows))
	}
}

// TestWithNobodyToTellTheScreenSaysSoRatherThanFailing.
//
// A rule with no target cannot pass Validate, deliberately. The watch is still
// worth creating — collection is useful without notification — so the screen
// says what is missing instead of refusing the save.
func TestWithNobodyToTellTheScreenSaysSoRatherThanFailing(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the watch was refused for having no recipient: %s", rec.Body.String())
	}
	if !strings.Contains(get(t, s, "/").Body.String(), "олучател") {
		t.Error("the screen does not say that nobody will be told")
	}
}

// TestTheScreenShowsWhatEachWatchCostsAndWhatIsLeft.
func TestTheScreenShowsWhatEachWatchCostsAndWhatIsLeft(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	body := get(t, s, "/").Body.String()
	for _, want := range []string{"0,2", "запр./мин", "отолок"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestTogglingAWatchOffFreesItsBudget, which is what somebody does when they
// want room for another one without deleting this.
func TestTogglingAWatchOffFreesItsBudget(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	ws, err := s.Store.Watches(context.Background())
	if err != nil || len(ws) != 1 {
		t.Fatalf("watches = %+v (%v)", ws, err)
	}
	id := strconv.FormatInt(ws[0].ID, 10)
	if rec := post(t, s, "/watches/toggle", url.Values{"id": {id}}); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	ws, _ = s.Store.Watches(context.Background())
	if ws[0].Enabled {
		t.Error("the watch is still enabled")
	}
}

// TestDeletingAWatchAsks. The confirmation is on the form, because this product
// has no undo for it.
func TestDeletingAWatchAsks(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	if !strings.Contains(get(t, s, "/").Body.String(), "data-confirm") {
		t.Error("the delete button does not ask")
	}
}

// TestAWatchNameIsEscaped, because it is a string a person pastes in and the
// panel renders back on the machine that holds their proxy key.
func TestAWatchNameIsEscaped(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches",
		addWatchForm(`<script>alert(1)</script>`, "https://m.avito.ru/all?q=a", 300))
	if strings.Contains(get(t, s, "/").Body.String(), "<script>alert(1)</script>") {
		t.Error("a watch name was rendered as markup")
	}
}

// TestARejectedFormComesBackFilledIn. Retyping a URL because one field was
// wrong is how a person learns to distrust a form.
func TestARejectedFormComesBackFilledIn(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches",
		addWatchForm("моё наблюдение", "https://m.avito.ru/all?q=iphone", 30))
	body := rec.Body.String()
	if !strings.Contains(body, "моё наблюдение") {
		t.Error("the name was lost")
	}
	if !strings.Contains(body, "q=iphone") {
		t.Error("the url was lost")
	}
}

// TestEveryFaultInOneFormIsReportedAtOnce. A form that rejects one field at a
// time is a form somebody submits five times.
func TestEveryFaultInOneFormIsReportedAtOnce(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches", url.Values{
		"name": {""}, "url": {"нет"}, "interval": {"5"}, "pages": {"99"},
		"surface": {"голубиная"},
	})
	body := rec.Body.String()
	for _, want := range []string{"название", "ссылк", "интервал", "страниц", "версия"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not mention %q: %s", want, body)
		}
	}
}

// TestTheSurfaceLabelSaysWhatEachOneActuallyDoes.
//
// It once said mobile carried more cards, which was backwards — mobile carries
// thirty against desktop's fifty. That is no longer the interesting difference:
// a live run found the desktop search page is a shell with no listings at all,
// so the card count is not what a person choosing needs to know.
//
// What they need is on the screen: what mobile costs, and that desktop does not
// collect. Both, or the choice is made blind.
func TestTheSurfaceLabelSaysWhatEachOneActuallyDoes(t *testing.T) {
	body := get(t, newServer(t), "/").Body.String()
	if strings.Contains(body, "больше карточек") {
		t.Error("the label still claims mobile has more cards")
	}
	// On the free route the desktop option is not a choice, and the screen has
	// to say so: it halts on request one, measured.
	if !strings.Contains(body, "встаёт на капче") {
		t.Error("the screen does not say that desktop halts in direct mode")
	}
	if !strings.Contains(body, "не отдаёт ни продавца") {
		t.Error("the screen does not say what the mobile surface leaves out")
	}
	if !strings.Contains(body, "avitomon enrich") {
		t.Error("the screen does not say how the missing seller is recovered")
	}
}

// TestTheDefaultSurfaceFollowsTheTransport.
//
// Not a preference — measured on live Avito from one machine within minutes.
// Direct mode met a geetest challenge on request one to www.avito.ru and never
// met one on m.avito.ru; through BlankTrail the desktop page answered and,
// with its canonical link followed, filled 49 of 50 sellers against mobile's
// none. So direct gets the only surface that answers and BlankTrail gets the
// one that carries what the repost ladder reads.
func TestTheDefaultSurfaceFollowsTheTransport(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetTransportMode(context.Background(),
		transport.ModeDirect); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/").Body.String()
	i := strings.Index(body, `name="surface"`)
	if i < 0 {
		t.Fatal("no surface control on the page")
	}
	sel := body[i:]
	sel = sel[:strings.Index(sel, "</select>")]
	if !strings.Contains(sel, `value="mobile" selected`) {
		t.Errorf("mobile is not the selected option: %s", sel)
	}
	if strings.Contains(sel, `value="desktop" selected`) {
		t.Error("both options are selected")
	}
	// Desktop stays on the list — it works through BlankTrail — but direct
	// mode is told plainly why it is not a choice here.
	if !strings.Contains(sel, `value="desktop"`) {
		t.Error("the desktop surface is no longer offered at all")
	}
	if !strings.Contains(body, "встаёт на капче") {
		t.Error("direct mode is not told why desktop is not a choice for it")
	}
	if !strings.Contains(body, "не отдаёт ни продавца") {
		t.Error("the screen stopped saying what the mobile surface leaves out")
	}
}

// TestARejectedFormComesBackWithTheSurfaceThePersonChose.
//
// The draft is what the form is redrawn from, and a person who picked one
// surface and mistyped the URL must not silently get the other back.
func TestARejectedFormComesBackWithTheSurfaceThePersonChose(t *testing.T) {
	form := addWatchForm("наблюдение", "https://m.avito.ru/all?q=x", 300)
	form.Set("surface", "desktop")
	form.Set("url", "не ссылка вовсе")

	rec := post(t, newServer(t), "/watches", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	sel := rec.Body.String()
	i := strings.Index(sel, `name="surface"`)
	if i < 0 {
		t.Fatal("no surface control on the redrawn page")
	}
	sel = sel[i:]
	sel = sel[:strings.Index(sel, "</select>")]
	if !strings.Contains(sel, `value="desktop" selected`) {
		t.Errorf("the chosen surface was lost to the refusal: %s", sel)
	}
}

// TestTheWatchesScreenSaysWhetherCollectionIsRunning.
//
// On the screen where the watches are, not in Настройки. A monitor that
// silently stopped collecting is the failure this whole product is arranged
// against, and burying the answer two screens away is how somebody spends a
// week believing they are being watched over.
func TestTheWatchesScreenSaysWhetherCollectionIsRunning(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")

	// No pass at all: said plainly rather than left blank.
	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "Ни одного прохода ещё не было") {
		t.Errorf("a fresh install says nothing about collection: %s", body)
	}

	// A recent pass: collection is running.
	aPass(t, s, id, s.now().Unix()-30, "ok", "dom", true, 30)
	body = get(t, s, "/").Body.String()
	if !strings.Contains(body, "Сбор идёт") {
		t.Errorf("a recent pass does not read as collecting: %s", body)
	}
	// ago() already ends in «назад». Adding it again produced «23 секунды
	// назад назад» on the live screen, which is the kind of thing only looking
	// at it catches — so the test looks for it now.
	if strings.Contains(body, "назад назад") {
		t.Errorf("the sentence says «назад» twice: %s", body)
	}
}

// TestALongSilenceReadsAsStoppedAndNotAsQuiet.
//
// Three intervals, the same slack the lifecycle uses to call a pass a re-seed.
// Two would cry wolf on one slow pass; ten would let a day of silence look
// normal.
func TestALongSilenceReadsAsStoppedAndNotAsQuiet(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	// The watch's interval is 300 s, so an hour is well past three of them.
	aPass(t, s, id, s.now().Unix()-3600, "ok", "dom", true, 30)

	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "Сбор не идёт") {
		t.Errorf("an hour of silence reads as collecting: %s", body)
	}
	if !strings.Contains(body, "no-collect") {
		t.Error("the screen does not say what to check")
	}
}

// TestAnInstallationWithNoWatchesIsToldNothingAboutCollection.
//
// There is nothing to collect, so «сбор не идёт» would be a warning about a
// state the person deliberately put the product in.
func TestAnInstallationWithNoWatchesIsToldNothingAboutCollection(t *testing.T) {
	body := get(t, newServer(t), "/").Body.String()
	if strings.Contains(body, "Сбор не идёт") {
		t.Error("an installation with no watches was warned about collection")
	}
}

// TestAWatchThatCannotKeepItsIntervalIsNamed.
//
// Measured: a desktop pass through BlankTrail took 7m41s against a 300-second
// interval. The scheduler does not know — it takes what is due and reads in
// order — and the budget line cannot tell, because that arithmetic counts
// requests and not seconds. Such a watch is simply read less often than its own
// row claims, and until this line nothing anywhere said so.
func TestAWatchThatCannotKeepItsIntervalIsNamed(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "медленное")
	// The watch's interval is 300 s. Five passes of seven minutes each.
	for i := 0; i < 5; i++ {
		slowPass(t, s, id, s.now().Unix()-int64(600*i), 7*60)
	}

	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "не успевает за своим интервалом") {
		t.Errorf("a watch that cannot keep up is not named: %s", body)
	}
	if !strings.Contains(body, "420") {
		t.Error("the measured duration is not on the screen")
	}
	if !strings.Contains(body, "300") {
		t.Error("the interval it is being compared against is not on the screen")
	}
}

// TestAWatchThatKeepsUpIsSaidNothingAbout.
func TestAWatchThatKeepsUpIsSaidNothingAbout(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "быстрое")
	for i := 0; i < 5; i++ {
		slowPass(t, s, id, s.now().Unix()-int64(600*i), 3)
	}
	if strings.Contains(get(t, s, "/").Body.String(), "не успевает") {
		t.Error("a watch that keeps up was warned about")
	}
}

// TestOneSlowPassIsNotAPattern.
//
// Below five readings the median is one slow start being mistaken for a habit —
// and the first pass of any watch is its seeding pass, which is the slowest it
// will ever be.
func TestOneSlowPassIsNotAPattern(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "новое")
	slowPass(t, s, id, s.now().Unix(), 7*60)

	if strings.Contains(get(t, s, "/").Body.String(), "не успевает") {
		t.Error("one slow pass produced a verdict")
	}
}

// slowPass writes one successful pass that took a given number of seconds.
func slowPass(t *testing.T, s *Server, watchID, at int64, seconds int64) {
	t.Helper()
	if _, err := s.Store.DB().Exec(`
		INSERT INTO passes (watch_id, started_at, finished_at, page, surface,
		                    class, path, mode, evidential, items_seen, elapsed_ms)
		VALUES (?, ?, ?, 1, 'mobile', 'ok', 'dom', 'direct', 1, 30, ?)`,
		watchID, at, at+seconds, seconds*1000); err != nil {
		t.Fatal(err)
	}
}

// TestAHaltedWatchCarriesItsWayOut.
//
// A halt is the only state this program will not leave on its own —
// halted_reason is never cleared by the program — so the row that shows it has
// to carry the button, or the exit is a command line somebody has to find.
func TestAHaltedWatchCarriesItsWayOut(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aWatch(t, s, "остановлено")
	if err := s.Store.MarkHalted(ctx, id, "geetest", 1500); err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "geetest") {
		t.Error("the reason is not shown")
	}
	if !strings.Contains(body, "Возобновить") {
		t.Errorf("a halted watch has no way out on screen: %s", body)
	}
	// And it says plainly that resuming promises nothing.
	if !strings.Contains(body, "Ничего не проверяется") {
		t.Error("the confirmation over-promises")
	}

	rec := post(t, s, "/watches/resume", url.Values{"id": {strconv.FormatInt(id, 10)}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	w, err := s.Store.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "idle" || w.HaltedReason != "" {
		t.Errorf("watch = %q / %q", w.State, w.HaltedReason)
	}
}

// TestAHealthyWatchHasNoResumeButton, because there is nothing to resume.
func TestAHealthyWatchHasNoResumeButton(t *testing.T) {
	s := newServer(t)
	aWatch(t, s, "работает")
	if strings.Contains(get(t, s, "/").Body.String(), "Возобновить") {
		t.Error("a healthy watch was offered a resume button")
	}
}

// TestAWatchWiderThanTheSiteWillPageIsNamedAndTold What To Do.
//
// Avito pages through at most five thousand results, so above that a
// date-sorted page one is the top of a firehose and «new listing» stops being a
// set difference. The spec asks for pmin and pmax by name, because a person who
// has to go and find that out is a person who narrows the watch by deleting it.
func TestAWatchWiderThanTheSiteWillPageIsNamed(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "всё подряд")
	if _, err := s.Store.DB().Exec(`
		UPDATE watches SET too_broad = 1, last_total_count = 59959 WHERE id = ?`,
		id); err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/").Body.String()
	if !strings.Contains(body, "слишком широкое") {
		t.Errorf("a watch over the cap is not named: %s", body)
	}
	if !strings.Contains(body, "59959") {
		t.Error("the measured count is not shown")
	}
	if !strings.Contains(body, "pmin") || !strings.Contains(body, "pmax") {
		t.Error("the two parameters that narrow it are not named")
	}
	// And it says the product will not do it for them, which is a decision
	// rather than a limitation.
	if !strings.Contains(body, "не сузит") {
		t.Error("the screen does not say the product will not narrow it")
	}
}

// TestANarrowWatchIsSaidNothingAbout.
func TestANarrowWatchIsSaidNothingAbout(t *testing.T) {
	s := newServer(t)
	aWatch(t, s, "узкое")
	if strings.Contains(get(t, s, "/").Body.String(), "слишком широкое") {
		t.Error("a narrow watch was warned about")
	}
}

// TestThePageCapAgreesWithTheSchema.
//
// This screen said ten and the column is CHECK-constrained to two, so anybody
// who typed three got a raw constraint failure naming a table instead of a
// refusal naming the mistake — the exact thing SetTransportMode's comment says
// must not happen.
func TestThePageCapAgreesWithTheSchema(t *testing.T) {
	if maxPagesPerPass != 2 {
		t.Fatalf("maxPagesPerPass = %d; watches.max_pages is CHECK(1..2)",
			maxPagesPerPass)
	}
	s := newServer(t)
	form := addWatchForm("наблюдение", "https://m.avito.ru/all?q=x", 300)
	form.Set("pages", "3")

	rec := post(t, s, "/watches", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d; three pages was accepted", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "CHECK") || strings.Contains(body, "constraint") {
		t.Errorf("the refusal is a database error: %s", body)
	}
	if !strings.Contains(body, "страниц") {
		t.Errorf("the refusal does not name the mistake: %s", body)
	}
}

// TestTheScreenSaysWhyTwoPages, because a limit with no reason reads as
// timidity and gets raised by the first person who wants more.
func TestTheScreenSaysWhyTwoPages(t *testing.T) {
	if !strings.Contains(get(t, newServer(t), "/").Body.String(), "429 в 403") {
		t.Error("the page limit is stated without its reason")
	}
}

// TestTheHaltBannerCarriesMarkerUrlAndWhen.
//
// A halt is a state and not an incident, which is why it is a banner rather
// than a toast — and a state is described by how long it has lasted as much as
// by what it is. «Остановлено» tells somebody to look; «остановлено во вторник»
// tells them whether to hurry.
func TestTheHaltBannerCarriesMarkerUrlAndWhen(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aWatch(t, s, "наблюдение")
	if err := s.Store.MarkHalted(ctx, id, "geetest", s.now().Unix()-7200); err != nil {
		t.Fatal(err)
	}

	// Any screen, because a banner is above everything.
	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "geetest") {
		t.Error("the banner does not carry the marker")
	}
	if !strings.Contains(body, "avito.ru") {
		t.Errorf("the banner does not carry the URL: %s", body)
	}
	if !strings.Contains(body, "назад") {
		t.Error("the banner does not say how long it has been stopped")
	}
}
