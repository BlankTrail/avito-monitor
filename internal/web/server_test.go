// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		Store: openStore(t),
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func setPassword(t *testing.T, s *Server, plain string) {
	t.Helper()
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetPanelSettings(context.Background(),
		store.PanelSettings{PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
}

// TestWithNoPasswordThePanelOpens.
//
// The default, and it is a decision rather than an oversight: the server listens
// on the loopback interface, so what a password keeps out is another program or
// another account on this same machine. Worth having on a shared machine, worth
// nothing on a personal one, and not something to decide on somebody's behalf.
func TestWithNoPasswordThePanelOpens(t *testing.T) {
	if rec := get(t, newServer(t), "/"); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200", rec.Code)
	}
}

// TestWithAPasswordEveryPageIsBehindIt.
func TestWithAPasswordEveryPageIsBehindIt(t *testing.T) {
	s := newServer(t)
	setPassword(t, s, "секрет")

	rec := get(t, s, "/")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET / = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("no Basic challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("", "секрет")
	ok := httptest.NewRecorder()
	s.Handler().ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Errorf("with the password: %d, want 200", ok.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("", "не секрет")
	bad := httptest.NewRecorder()
	s.Handler().ServeHTTP(bad, req)
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("with the wrong password: %d, want 401", bad.Code)
	}
}

// TestStaticAssetsAreNotBehindThePassword.
//
// They are the same bytes for everybody and carry nothing about this
// installation. Behind the gate, the only thing an unauthenticated visitor gets
// is an unstyled prompt.
func TestStaticAssetsAreNotBehindThePassword(t *testing.T) {
	s := newServer(t)
	setPassword(t, s, "секрет")
	if rec := get(t, s, "/static/monitor.css"); rec.Code != http.StatusOK {
		t.Errorf("GET /static/monitor.css = %d, want 200", rec.Code)
	}
}

// TestThePanelWillNotLeaveTheMachineWithoutAPasswordOfYourOwn.
//
// The refusal lives here rather than in the settings screen because a setting
// can be written by anything — a hand-edited database, a restored backup, a
// future importer — and the only place that reliably sees the combination is
// the one about to open the port. The panel holds a proxy key, a bot token and
// everything the monitor has collected.
func TestThePanelWillNotLeaveTheMachineWithoutAPasswordOfYourOwn(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()

	if _, err := s.ListenAddress(ctx, 8080, true); !errors.Is(err, ErrWouldExposeUnprotectedPanel) {
		t.Fatalf("err = %v, want the refusal", err)
	}
	// Loopback with no password is fine, and is the default.
	addr, err := s.ListenAddress(ctx, 8080, false)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want the loopback", addr)
	}

	setPassword(t, s, "секрет")
	addr, err = s.ListenAddress(ctx, 8080, true)
	if err != nil {
		t.Fatal(err)
	}
	if addr != ":8080" {
		t.Errorf("addr = %q, want every interface", addr)
	}
}

// TestTheSourceLinkIsOnEveryPage.
//
// The AGPL obliges whoever serves this to offer the source of what they are
// serving. A constant and not a setting: a configurable link is a link that can
// be pointed somewhere else.
func TestTheSourceLinkIsOnEveryPage(t *testing.T) {
	rec := get(t, newServer(t), "/")
	if !strings.Contains(rec.Body.String(), SourceURL) {
		t.Error("the page does not carry the source link")
	}
}

// TestEveryTabIsReachable. A tab in the header that answers 404 is a tab a
// person clicks once and stops trusting.
func TestEveryTabIsReachable(t *testing.T) {
	s := newServer(t)
	for _, tab := range s.tabs("/") {
		rec := get(t, s, tab.Href)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s (%s) = %d, want 200", tab.Href, tab.Label, rec.Code)
		}
	}
}

// TestExactlyOneTabIsActive, so the header says where you are.
func TestExactlyOneTabIsActive(t *testing.T) {
	s := newServer(t)
	for _, probe := range s.tabs("/") {
		active := 0
		for _, tab := range s.tabs(probe.Href) {
			if tab.Active {
				active++
			}
		}
		if active != 1 {
			t.Errorf("on %s, %d tabs are active", probe.Href, active)
		}
	}
}

// TestAValueFromTheDatabaseIsEscaped.
//
// Every handler in this package assembles its own HTML. A watch named with a
// script tag is what a person pastes in from somewhere else, and the panel
// renders it back to them on the machine that holds their proxy key.
func TestAValueFromTheDatabaseIsEscaped(t *testing.T) {
	const nasty = `<script>alert(1)</script>`
	if got := esc(nasty); strings.Contains(got, "<script>") {
		t.Errorf("esc(%q) = %q", nasty, got)
	}
	if got := attr(`" onload="alert(1)`); strings.Contains(got, `"`) {
		t.Errorf("attr left a quote unescaped: %q", got)
	}
}

// TestAPageRendersAsHTML, with the charset — a Russian panel served without one
// is a page of question marks in whatever browser guesses wrong.
func TestAPageRendersAsHTML(t *testing.T) {
	rec := get(t, newServer(t), "/")
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") || !strings.Contains(ct, "utf-8") {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestTheBannerSaysNothingCollectsBeforeATransportIsChosen, above every screen,
// because somebody who skipped the first question would otherwise see an empty
// watch list and conclude the product does not work.
func TestTheBannerSaysNothingCollectsBeforeATransportIsChosen(t *testing.T) {
	s := newServer(t)
	if !strings.Contains(get(t, s, "/").Body.String(), "Не выбран способ") {
		t.Error("the banner is missing before a transport is chosen")
	}
}
