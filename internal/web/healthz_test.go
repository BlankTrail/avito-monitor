// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestHealthzIsReadableWithoutThePassword.
//
// A health endpoint a supervisor cannot read is a health endpoint that does
// nothing. What it discloses is the shape of this installation's trouble, not
// any of its data, and somebody who can reach this port can already reach the
// login prompt.
func TestHealthzIsReadableWithoutThePassword(t *testing.T) {
	s := newServer(t)
	setPassword(t, s, "пароль")

	if rec := get(t, s, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz behind a password = %d (%s)", rec.Code, rec.Body.String())
	}
	// And the pages behind it are still behind it.
	if rec := get(t, s, "/"); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET / = %d; the password stopped working", rec.Code)
	}
}

// TestAnInstallWithNoWatchesIsHealthy.
//
// Nothing to collect is not a fault. An install somebody has not finished
// setting up must not read as broken to a supervisor that would restart it
// hourly for ever.
func TestAnInstallWithNoWatchesIsHealthy(t *testing.T) {
	rec := get(t, newServer(t), "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("= %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "наблюдений нет") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestEveryWatchHaltedFailsReadinessAndNamesWhy.
//
// The spec's own sentence: the process fails its readiness rather than exiting,
// because a process that exits is restarted by systemd into the same wall for
// ever, and because the panel is the only place the reason can be read.
func TestEveryWatchHaltedFailsReadinessAndNamesWhy(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aWatch(t, s, "наблюдение")
	if err := s.Store.MarkHalted(ctx, id, "geetest", 1500); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/healthz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("= %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "geetest") {
		t.Errorf("the marker is not named: %q", body)
	}
	if !strings.Contains(body, "наблюдение") {
		t.Errorf("the watch is not named: %q", body)
	}
	// And it says the one thing a supervisor most needs to be told, because
	// restarting is exactly what it would otherwise do.
	if !strings.Contains(body, "Перезапуск не поможет") {
		t.Errorf("the body does not say restarting cannot help: %q", body)
	}
}

// TestOneWatchStillCollectingIsHealthy.
//
// Readiness is about the installation and not about each watch. One watch
// behind a wall while three others collect is a product working, and a
// supervisor told otherwise would restart a process that is doing its job.
func TestOneWatchStillCollectingIsHealthy(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	halted := aWatch(t, s, "остановлено")
	aWatch(t, s, "работает")
	if err := s.Store.MarkHalted(ctx, halted, "geetest", 1500); err != nil {
		t.Fatal(err)
	}

	rec := get(t, s, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d (%s)", rec.Code, rec.Body.String())
	}
	// The halted one is still named. «ok» with nothing else would hide a watch
	// that has been behind a wall for a week.
	if !strings.Contains(rec.Body.String(), "geetest") {
		t.Errorf("a halted watch went unmentioned: %q", rec.Body.String())
	}
}

// TestADisabledWatchIsNotCountedEitherWay.
//
// Somebody who switched a watch off is not being failed by the product, and a
// disabled watch that was halted before it was switched off must not keep the
// whole install unready for ever.
func TestADisabledWatchIsNotCountedEitherWay(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aWatch(t, s, "выключено")
	if err := s.Store.MarkHalted(ctx, id, "geetest", 1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB().Exec(
		`UPDATE watches SET enabled = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if rec := get(t, s, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("= %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestOneHaltedWatchGetsAGrammaticalSentence.
//
// «все 1 наблюдение остановлены» agrees with nothing, and one watch is what a
// fresh install has — so the ungrammatical branch was the one almost everybody
// would read. Found by halting a watch and reading the endpoint.
func TestOneHaltedWatchGetsAGrammaticalSentence(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aWatch(t, s, "магазин")
	if err := s.Store.MarkHalted(ctx, id, "ip-blocked", 1500); err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/healthz").Body.String()
	if strings.Contains(body, "все 1") {
		t.Errorf("несогласованная фраза:\n%s", body)
	}
	if !strings.Contains(body, "единственное наблюдение остановлено") {
		t.Errorf("одно наблюдение названо не так:\n%s", body)
	}
}

// TestACollectorThatNeverOpenedIsNotCollecting.
//
// Found on 11 October 2026: a fresh install set up through the panel — mode
// chosen, key pasted after start — whose collector had failed on «needs a key»
// while /healthz answered «ok: собирает 5 из 5».
func TestACollectorThatNeverOpenedIsNotCollecting(t *testing.T) {
	s := newServer(t)
	aWatch(t, s, "магазин")
	s.Collector = func() (bool, string) { return false, "blanktrail mode needs a key" }

	rec := get(t, s, "/healthz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "needs a key") {
		t.Errorf("the reason is not said:\n%s", rec.Body.String())
	}
	if body := get(t, s, "/").Body.String(); !strings.Contains(body, "Сбор не идёт") {
		t.Error("the watches screen does not say collection is down")
	}
}
