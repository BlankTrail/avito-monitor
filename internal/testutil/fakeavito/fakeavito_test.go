// SPDX-License-Identifier: AGPL-3.0-or-later

package fakeavito

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

// get is a bare client: no jar, no fingerprint. It is what an unwarmed
// identity looks like, which is the state most of these rules are about.
func get(t *testing.T, s *Server, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.URL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return string(b)
}

// TestColdRequestIsRefusedWithAWall is the measured baseline: plain curl, no
// jar, HTTP 429 with wall markers on request one.
func TestColdRequestIsRefusedWithAWall(t *testing.T) {
	s := New(t)
	resp := get(t, s, "/all?q=iphone")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 on an unwarmed identity", resp.StatusCode)
	}
	if !strings.Contains(body(t, resp), "проблема с IP") {
		t.Error("the refusal carried no wall marker; the measured one does, and " +
			"that is why the classifier needs a ClassCold row")
	}
}

// TestRetryAfterIsNeverSent. There is no Retry-After on this target, so a
// ladder that waited for one would wait for a header that never comes.
func TestRetryAfterIsNeverSent(t *testing.T) {
	s := New(t)
	resp := get(t, s, "/all?q=iphone")
	defer resp.Body.Close()
	if v := resp.Header.Get("Retry-After"); v != "" {
		t.Errorf("Retry-After = %q; this target never sends one", v)
	}
}

// TestRootNavigationIssuesTheIdentity. A fresh cookieless session per request
// collects a 429 immediately; the way out is a root navigation, which is what
// mints srv_id and the first nonce.
func TestRootNavigationIssuesTheIdentity(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	ok := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("warmed request status = %d, want 200", ok.StatusCode)
	}
}

// TestNonceMustBeFresh. _avisc lasts sixty seconds. A request one second late
// must go out with the STALE value rather than with none — sending none is
// strictly worse — so the server accepts it inside the window and refuses past.
func TestNonceMustBeFresh(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	s.Advance(NonceTTL + time.Second)
	stale := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer stale.Body.Close()
	if stale.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d with a nonce past its TTL, want 429", stale.StatusCode)
	}
}

// TestNonceIsRefreshedOnEveryResponse. The client has to write the new value
// back after every response; a jar that only stores the first one goes stale
// inside a minute.
func TestNonceIsRefreshedOnEveryResponse(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	resp := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	defer resp.Body.Close()
	next := ""
	for _, c := range resp.Cookies() {
		if c.Name == CookieNonce {
			next = c.Value
		}
	}
	if next == "" {
		t.Fatal("no fresh nonce on the response")
	}
	if next == nonce {
		t.Error("the nonce did not change; a nonce that never moves is not one")
	}
}

// TestEscalationTo403 is the measured hardening past ninety requests in ten
// minutes.
func TestEscalationTo403(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	last := 0
	for i := 0; i < BurstLimit+5; i++ {
		resp := get(t, s, "/all?q=iphone",
			&http.Cookie{Name: CookieIdentity, Value: srv},
			&http.Cookie{Name: CookieNonce, Value: nonce})
		for _, c := range resp.Cookies() {
			if c.Name == CookieNonce {
				nonce = c.Value
			}
		}
		last = resp.StatusCode
		resp.Body.Close()
	}
	if last != http.StatusForbidden {
		t.Errorf("status after %d requests = %d, want 403 — the measured "+
			"escalation hardens past ninety in ten minutes", BurstLimit+5, last)
	}
}

// TestSwitchesProduceTheThreeShapesTheClassifierMustTellApart. An empty answer,
// a soft block and a 439 are three different things and only the body tells
// them apart.
func TestSwitchesProduceTheThreeShapesTheClassifierMustTellApart(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	fetch := func() (int, string) {
		resp := get(t, s, "/all?q=iphone",
			&http.Cookie{Name: CookieIdentity, Value: srv},
			&http.Cookie{Name: CookieNonce, Value: nonce})
		for _, c := range resp.Cookies() {
			if c.Name == CookieNonce {
				nonce = c.Value
			}
		}
		return resp.StatusCode, body(t, resp)
	}

	s.ForceEmpty(true)
	if st, b := fetch(); st != 200 || !strings.Contains(b, "Ничего не найдено") {
		t.Errorf("empty: status=%d, site wording present=%v", st,
			strings.Contains(b, "Ничего не найдено"))
	}
	s.ForceEmpty(false)

	s.SoftBlock(true)
	if st, b := fetch(); st != 200 || strings.Contains(b, "__initialData__") {
		t.Errorf("soft block: status=%d state-present=%v; a soft block is a 200 "+
			"with no container at all", st, strings.Contains(b, "__initialData__"))
	}
	s.SoftBlock(false)

	s.Serve439(true)
	if st, _ := fetch(); st != 439 {
		t.Errorf("status = %d, want 439", st)
	}
}

// TestPageIsReadableByTheRealExtractor. A fake whose markup only this package
// understands would let the ladder pass while the parser rots.
func TestPageIsReadableByTheRealExtractor(t *testing.T) {
	s := New(t)
	srv, nonce := s.Warm(t)
	resp := get(t, s, "/all?q=iphone",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	b := body(t, resp)

	page, err := avito.Extract(avito.Document{
		Surface: avito.SurfaceMobile, Body: []byte(b)}, avito.Options{})
	if err != nil {
		t.Fatalf("the real extractor could not read the fake's page: %v", err)
	}
	if !page.Ok() || len(page.Listings) != 30 {
		t.Errorf("listings = %d ok = %v, want 30 and true", len(page.Listings), page.Ok())
	}
	if page.TotalCount == nil || *page.TotalCount != 59959 {
		t.Errorf("TotalCount = %v, want 59959", page.TotalCount)
	}
}

// TestEmptyPageIsReadableToo. The empty answer is the one the classifier must
// not mistake for a soft block, so the extractor has to read it as an answer.
func TestEmptyPageIsReadableToo(t *testing.T) {
	s := New(t)
	s.ForceEmpty(true)
	srv, nonce := s.Warm(t)
	resp := get(t, s, "/all?q=nothing",
		&http.Cookie{Name: CookieIdentity, Value: srv},
		&http.Cookie{Name: CookieNonce, Value: nonce})
	page, err := avito.Extract(avito.Document{
		Surface: avito.SurfaceMobile, Body: []byte(body(t, resp))}, avito.Options{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !page.EmptyDeclared {
		t.Error("EmptyDeclared = false; the site said so in its own words and " +
			"the classifier reads exactly this to avoid calling it a soft block")
	}
	if page.TotalCount == nil || *page.TotalCount != 0 {
		t.Errorf("TotalCount = %v, want 0", page.TotalCount)
	}
}
