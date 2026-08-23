// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fakeavito is an origin that behaves the way the real one was measured
// to behave on 2026-08-23.
//
// Every rule here is a measurement rather than an invention, and that is the
// whole point: a fake that is kinder than the original teaches the ladder the
// wrong lesson, and the lesson is only ever unlearned in production.
//
// Time is a parameter. Advance moves the server's clock without sleeping, so a
// test about a sixty-second nonce or a ten-minute window runs instantly and
// deterministically.
package fakeavito

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cookies that matter. srv_id is the long-lived identity and the thing that
// gets flagged; _avisc is a sixty-second nonce refreshed on every response.
const (
	CookieIdentity = "srv_id"
	CookieNonce    = "_avisc"
)

const (
	// NonceTTL is the measured sixty seconds.
	NonceTTL = 60 * time.Second
	// BurstLimit and BurstWindow are the measured escalation: about ninety
	// requests in ten minutes hardens the refusal from 429 to 403.
	BurstLimit  = 90
	BurstWindow = 10 * time.Minute
)

// Server is the fake origin.
type Server struct {
	mu       sync.Mutex
	now      time.Time
	requests int

	nonces   map[string]string
	issued   map[string]time.Time
	hits     map[string][]time.Time
	nonceSeq int
	idSeq    int

	dropState  bool
	softBlock  bool
	serve439   bool
	forceEmpty bool

	http *httptest.Server
}

// New starts a server and stops it when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		now:    time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
		nonces: map[string]string{},
		issued: map[string]time.Time{},
		hits:   map[string][]time.Time{},
	}
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.http.Close)
	return s
}

func (s *Server) URL() string { return s.http.URL }

// Requests counts every request that reached this server, refusals included.
// It is what a halting test asserts against: after a halt, it must not move.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Advance moves the clock without sleeping.
func (s *Server) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
}

func (s *Server) DropState(v bool)  { s.set(func() { s.dropState = v }) }
func (s *Server) SoftBlock(v bool)  { s.set(func() { s.softBlock = v }) }
func (s *Server) Serve439(v bool)   { s.set(func() { s.serve439 = v }) }
func (s *Server) ForceEmpty(v bool) { s.set(func() { s.forceEmpty = v }) }

func (s *Server) set(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

// Reset clears every identity and every switch, keeping the clock.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces = map[string]string{}
	s.issued = map[string]time.Time{}
	s.hits = map[string][]time.Time{}
	s.dropState, s.softBlock, s.serve439, s.forceEmpty = false, false, false, false
}

// Warm performs the root navigation an unwarmed identity has to make, and
// returns the identity and its first nonce.
func (s *Server) Warm(t *testing.T) (identity, nonce string) {
	t.Helper()
	resp, err := http.Get(s.URL() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		switch c.Name {
		case CookieIdentity:
			identity = c.Value
		case CookieNonce:
			nonce = c.Value
		}
	}
	if identity == "" || nonce == "" {
		t.Fatalf("root navigation issued srv_id=%q nonce=%q", identity, nonce)
	}
	return identity, nonce
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++

	// A root navigation is how an identity is minted, and it is the only way.
	// This is the rule behind the measurement that a fresh cookieless session
	// per request collects a 429 immediately.
	if r.URL.Path == "/" {
		s.idSeq++
		s.mint(w, "srv-"+strconv.Itoa(s.idSeq))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html><body>Авито</body></html>")
		return
	}

	id := cookie(r, CookieIdentity)
	nonce := cookie(r, CookieNonce)

	// Unwarmed, or carrying a nonce that has lapsed: refused, with a wall in
	// the body and without a Retry-After. This is the measured cold baseline.
	if id == "" || s.nonces[id] == "" || nonce != s.nonces[id] ||
		s.now.Sub(s.issued[id]) > NonceTTL {
		s.wall(w, http.StatusTooManyRequests)
		return
	}

	// The measured escalation, counted per identity inside a moving window.
	hits := append(s.recent(id), s.now)
	s.hits[id] = hits
	if len(hits) > BurstLimit {
		s.mint(w, id)
		s.wall(w, http.StatusForbidden)
		return
	}

	s.mint(w, id)
	switch {
	case s.serve439:
		s.wall(w, 439)
	case s.softBlock:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html><body><div>Авито</div></body></html>")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, s.page())
	}
}

// recent drops hits that have fallen out of the burst window.
func (s *Server) recent(id string) []time.Time {
	var out []time.Time
	for _, t := range s.hits[id] {
		if s.now.Sub(t) <= BurstWindow {
			out = append(out, t)
		}
	}
	return out
}

// mint issues a fresh nonce, which is what "refreshed on every response" means.
func (s *Server) mint(w http.ResponseWriter, id string) {
	s.nonceSeq++
	n := "nonce-" + strconv.Itoa(s.nonceSeq)
	s.nonces[id] = n
	s.issued[id] = s.now
	http.SetCookie(w, &http.Cookie{Name: CookieIdentity, Value: id, Path: "/"})
	http.SetCookie(w, &http.Cookie{Name: CookieNonce, Value: n, Path: "/"})
}

// wall writes a refusal. Deliberately no Retry-After, ever.
func (s *Server) wall(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprint(w, "<html><head><title>Ошибка</title></head><body>"+
		"<h1>Возникла проблема с IP-адресом</h1></body></html>")
}

// page renders a mobile results page in the shape the extractor reads: an
// attribute-less script holding window.__initialData__, plus item-wrapper
// cards. dropState removes the state block, which is what Avito does to a
// client it has classified.
func (s *Server) page() string {
	count := 59959
	extra := ""
	if s.forceEmpty {
		count = 0
		extra = `,\"emptySearch\":\"Ничего не найдено по запросу\"`
	}
	state := fmt.Sprintf(
		`<script>window.__initialData__ = "{\"search\":{\"count\":%d%s}}";</script>`,
		count, extra)
	if s.dropState {
		state = ""
	}
	var cards strings.Builder
	if !s.forceEmpty {
		for i := 1; i <= 30; i++ {
			id := 7800000000 + i
			fmt.Fprintf(&cards,
				`<div data-marker="item-wrapper(%d)">`+
					`<div data-marker="titleLabelGrid">Товар %d</div>`+
					`<div data-marker="priceLabelGrid">%d ₽</div>`+
					`<a data-marker="item/link" data-marker-id="%d" href="/moskva/t/x_%d"></a>`+
					`</div>`, id, i, 1000*i, id, id)
		}
	}
	return "<html><body>" + state + cards.String() + "</body></html>"
}

func cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
