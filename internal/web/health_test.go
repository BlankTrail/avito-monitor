// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func aPass(t *testing.T, s *Server, watchID, at int64, class, path string, evidential bool, items int) {
	t.Helper()
	_, err := s.Store.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, path,
		   mode, evidential, items_seen)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, 'direct', ?, ?)`,
		watchID, at, at, class, path, evidential, items)
	if err != nil {
		t.Fatal(err)
	}
}

func aWatch(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	rec := post(t, s, "/watches",
		addWatchForm(name, "https://m.avito.ru/all?q="+name, 300))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("adding a watch: %s", rec.Body.String())
	}
	ws, err := s.Store.Watches(context.Background())
	if err != nil || len(ws) == 0 {
		t.Fatalf("watches = %+v (%v)", ws, err)
	}
	return ws[len(ws)-1].ID
}

// TestTheHealthScreenIsReachable.
func TestTheHealthScreenIsReachable(t *testing.T) {
	if rec := get(t, newServer(t), "/health"); rec.Code != http.StatusOK {
		t.Errorf("GET /health = %d", rec.Code)
	}
}

// TestShapeIsLabelledAsOursAndNotTheirs.
//
// The spec asks for exactly this. «shape» filed with the site's refusals reads
// as the site blocking us; it is our parser having stopped understanding the
// payload, which is our bug and a different thing to go and fix.
func TestShapeIsLabelledAsOursAndNotTheirs(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, s.now().Unix()-600, "shape", "", false, 0)
	aPass(t, s, id, s.now().Unix()-600, "throttled", "", false, 0)

	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "наш разбор") {
		t.Errorf("shape is not marked as ours: %s", body)
	}
}

// TestAWatchWithNoEvidentialPassSaysSoRatherThanShowing1970.
//
// The one line that separates a quiet market from a dead collector, per the
// spec. A date of 1 January 1970 is neither.
func TestAWatchWithNoEvidentialPassSaysSoRatherThanShowing1970(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, s.now().Unix()-600, "soft-block", "", false, 0)

	body := get(t, s, "/health").Body.String()
	if strings.Contains(body, "1970") {
		t.Error("the screen shows the epoch as a date")
	}
	if !strings.Contains(body, "ни разу") {
		t.Errorf("the screen does not say there has never been one: %s", body)
	}
}

// TestTheTwoAgesAreBothShown.
//
// A watch polling every five minutes and answering soft-block every time has a
// recent last pass and an ancient last evidential one, and only the second says
// anything is wrong. Showing one of the two would hide exactly that.
func TestTheTwoAgesAreBothShown(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, s.now().Unix()-7200, "ok", "dom", true, 30)
	aPass(t, s, id, s.now().Unix()-60, "soft-block", "", false, 0)

	body := get(t, s, "/health").Body.String()
	for _, want := range []string{"оследний проход", "оследний доказательный"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestCardYieldSaysItIsNotArmedRatherThanShowingAZero.
//
// A ratio of nought on a healthy new watch would read as a total failure of
// extraction. «Ещё не набралось десяти проходов» is the truth.
func TestCardYieldSaysItIsNotArmedRatherThanShowingAZero(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := 0; i < 3; i++ {
		aPass(t, s, id, s.now().Unix()-600+int64(i), "ok", "dom", true, 30)
	}
	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "не набралось") {
		t.Errorf("the screen does not say the alarm is unarmed: %s", body)
	}
}

// TestSaturationWithNoArrivalsIsAQuietQueryAndNotAnAlarm.
//
// Spec: zero arrivals across the whole window is not a division by zero and not
// an alarm — it is a quiet query, reported as «нечего измерять» rather than as a
// ratio, because a metric that invents a number where it has no data is worse
// than one that says it has none.
func TestSaturationWithNoArrivalsIsAQuietQueryAndNotAnAlarm(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := 0; i < 10; i++ {
		aPass(t, s, id, s.now().Unix()-600+int64(i), "ok", "dom", true, 30)
	}
	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "нечего измерять") {
		t.Errorf("a quiet query is not reported as one: %s", body)
	}
	if strings.Contains(body, "теряет объявления") {
		t.Error("a quiet query raised the undersampled alarm")
	}
}

// TestTheDatabaseSizeAndRowCountsAreShown, which is where somebody looks when
// the disk is filling.
func TestTheDatabaseSizeAndRowCountsAreShown(t *testing.T) {
	s := newServer(t)
	aWatch(t, s, "наблюдение")
	body := get(t, s, "/health").Body.String()
	for _, want := range []string{"МБ", "passes", "listing_snapshots"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestAnEmptyInstallIsNotAWallOfZeroes.
//
// A fresh install has no passes at all, and a screen of «0%» and «0» reads as a
// broken collector rather than as one that has not run yet.
func TestAnEmptyInstallIsNotAWallOfZeroes(t *testing.T) {
	body := get(t, newServer(t), "/health").Body.String()
	if !strings.Contains(body, "ещё не было ни одного прохода") {
		t.Errorf("an install with no passes does not say so: %s", body)
	}
}

// TestTheHealthTabIsInTheHeader, and reachable.
func TestTheHealthTabIsInTheHeader(t *testing.T) {
	s := newServer(t)
	found := false
	for _, tab := range s.tabs("/") {
		if tab.Href == "/health" {
			found = true
		}
	}
	if !found {
		t.Error("there is no tab for the health screen")
	}
}

// TestTheWindowKeepsLastWeekOutOfTodaysPicture.
//
// A day, which is the spec's word. A switch from state to dom this morning must
// not be buried under a month of health.
func TestTheWindowKeepsLastWeekOutOfTodaysPicture(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, s.now().Unix()-7*24*3600, "ok", "state", true, 30)
	aPass(t, s, id, s.now().Unix()-600, "ok", "dom", true, 30)

	body := get(t, s, "/health").Body.String()
	if strings.Contains(body, "состояние страницы") {
		t.Error("a pass from last week is in today's share")
	}
	if !strings.Contains(body, "разметка") {
		t.Error("today's pass is missing from the share")
	}
}

// TestAnAgeIsHowLongAgoAndNotATimestamp. «1699999999» is not something a person
// reads, and «два часа назад» is the question they were asking.
func TestAnAgeIsHowLongAgoAndNotATimestamp(t *testing.T) {
	now := int64(s0Unix)
	cases := []struct {
		at   int64
		want string
	}{
		{0, "ни разу"},
		{now - 30, "30 секунд назад"},
		{now - 60, "1 минуту назад"},
		{now - 3*60, "3 минуты назад"},
		{now - 2*3600, "2 часа назад"},
		{now - 5*86400, "5 дней назад"},
		{now - 86400, "1 день назад"},
	}
	for _, c := range cases {
		if got := ago(c.at, now); got != c.want {
			t.Errorf("ago(%d) = %q, want %q", c.at, got, c.want)
		}
	}
}

// s0Unix is the fixed clock newServer hands the Server.
const s0Unix = 1_700_000_000
