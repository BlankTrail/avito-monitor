// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func post(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestTheFirstScreenOffersNoDefault.
//
// Decision 3. Neither radio starts selected, and the form refuses a post with
// no choice: a pre-selected option is a decision made on somebody's behalf
// about whether their traffic goes through a paid service.
func TestTheFirstScreenOffersNoDefault(t *testing.T) {
	s := newServer(t)
	body := get(t, s, "/first-run").Body.String()

	if strings.Contains(body, "checked") {
		t.Error("one of the two transports is pre-selected")
	}
	for _, want := range []string{"direct", "blanktrail"} {
		if !strings.Contains(body, want) {
			t.Errorf("the form does not offer %q", want)
		}
	}
	if rec := post(t, s, "/first-run", url.Values{}); rec.Code == http.StatusSeeOther {
		t.Error("a post with no choice was accepted")
	}
	if _, err := s.Store.TransportMode(context.Background()); err == nil {
		t.Error("a transport was chosen by a post that chose nothing")
	}
}

// TestTheScreenQuotesTheSoakAndItsCaveat.
//
// Both figures come from one run, from one shared egress address, stopped by
// the operator rather than by the site. A number on a settings screen without
// its sample size is a number somebody plans capacity against.
func TestTheScreenQuotesTheSoakAndItsCaveat(t *testing.T) {
	body := get(t, newServer(t), "/first-run").Body.String()
	for _, want := range []string{"250", "360", "адрес", "оператор"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not mention %q", want)
		}
	}
	// The refuted number must not be back.
	for _, gone := range []string{"300–700", "300-700"} {
		if strings.Contains(body, gone) {
			t.Errorf("the screen quotes %q, which the soak refuted", gone)
		}
	}
}

// TestChoosingDirectSaysWhatStopsIt. Mode (b) is first class, and «бесплатно»
// without «останавливается на капче» is an advertisement rather than a choice.
func TestChoosingDirectSaysWhatStopsIt(t *testing.T) {
	body := get(t, newServer(t), "/first-run").Body.String()
	if !strings.Contains(body, "капч") {
		t.Error("the direct option does not say that a captcha stops it")
	}
	if !strings.Contains(body, "прокси") {
		t.Error("the direct option does not say that a proxy list multiplies it")
	}
}

// TestSavingAChoiceRedirectsToTheWatches, which is what somebody wants next.
func TestSavingAChoiceRedirectsToTheWatches(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/first-run", url.Values{"mode": {"direct"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /first-run = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	mode, err := s.Store.TransportMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mode != transport.ModeDirect {
		t.Errorf("mode = %q", mode)
	}
}

// TestAModeThisBuildDoesNotKnowIsRefused, rather than written into a column
// with a CHECK that would fail later with a message about SQL.
func TestAModeThisBuildDoesNotKnowIsRefused(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/first-run", url.Values{"mode": {"carrier-pigeon"}})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("an unknown transport was accepted")
	}
	if _, err := s.Store.TransportMode(context.Background()); err == nil {
		t.Error("an unknown transport was stored")
	}
}

// TestTheBannerGoesAwayOnceTheQuestionIsAnswered.
func TestTheBannerGoesAwayOnceTheQuestionIsAnswered(t *testing.T) {
	s := newServer(t)
	if body := get(t, s, "/settings").Body.String(); !strings.Contains(body, "Не выбран способ") {
		t.Error("the banner is missing before a transport is chosen")
	}
	if rec := post(t, s, "/first-run", url.Values{"mode": {"blanktrail"}}); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	if body := get(t, s, "/settings").Body.String(); strings.Contains(body, "Не выбран способ") {
		t.Error("the banner is still there after a transport was chosen")
	}
}
