// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web is the product's face: a small server that renders what the store
// holds and lets a person configure, start and watch a collection.
//
// Go templates and a page of vanilla JavaScript, with every asset in the binary.
// Spec §7 names htmx; this package does the eighty lines it would have provided
// by hand instead, for the reason static/app.js states at the top of itself —
// fifty kilobytes of third-party code vendored into a public AGPL repository, to
// replace something a reader can audit in a minute.
//
// Access is closed by default and stays on this machine until the user says
// otherwise. A monitor that shipped listening on every interface with no
// password would be a monitor whose owner's proxy credentials are one port scan
// away.
//
// Everything that needs a live third party — BlankTrail's control API, Telegram,
// the operating system's autostart entry — is a function field on Server rather
// than a call into the package that does it. The suite needs no network and no
// registry, and the wiring that supplies them lives in cmd/avitomon.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// SourceURL is where the AGPL requires this page to point.
//
// A constant rather than a setting: the licence obliges whoever serves this to
// offer the source of what they are serving, and a configurable link is a link
// that can be pointed somewhere else.
const SourceURL = "https://github.com/BlankTrail/avito-monitor"

// ErrWouldExposeUnprotectedPanel is returned when a panel anyone could walk into
// is asked to listen beyond the loopback interface.
var ErrWouldExposeUnprotectedPanel = errors.New(
	"web: панель без пароля не открывается за пределы этой машины: " +
		"задайте пароль в настройках")

// Autostart is the operating system's own start-at-login mechanism, or nil in a
// build that has none.
//
// Enabled is asked rather than remembered: an entry can be removed by anything,
// and a checkbox showing a stored intention would tell the user their monitor
// starts at login when it does not.
type Autostart interface {
	Enabled() (bool, error)
	Enable() error
	Disable() error
}

// Tab is one entry in the header navigation.
type Tab struct {
	Label  string
	Href   string
	Active bool
}

// listReport is what the last saved proxy list parsed to, held for the redirect
// that follows the save.
//
// One value and not a map: a person saves one list at a time, and a map keyed by
// channel would be a cache with no eviction on a long-running process.
type listReport struct {
	ChannelID int64
	Good      int
	Bad       []string
}

// Server serves the interface.
type Server struct {
	Store *store.Store
	Now   func() time.Time

	// CheckBlankTrail reports whether the configured control API answers. A
	// field rather than a call into the SDK so that tests do not need a live
	// one — the preflight is the SDK's and re-implementing it here would be a
	// second opinion nobody asked for.
	CheckBlankTrail func(ctx context.Context, url, apiKey string) error

	// CheckTelegram asks Telegram who this bot is, over whatever ladder the
	// wiring built.
	CheckTelegram func(ctx context.Context, token string) (username string, err error)

	// Gateways lists what the licensed proxy has configured, so a gateway is
	// picked from what exists rather than typed from memory.
	Gateways func(ctx context.Context) (blanktrail.GatewayList, error)

	// CheckChannel reports what one egress channel holds, or why it cannot be
	// used. Reading a proxy list is the engine's business; this package's tests
	// must not need a licensed service.
	CheckChannel func(ctx context.Context, id int64) (summary string, err error)

	Autostart Autostart

	// botAPIFailed records that a Telegram check has failed on this run.
	//
	// The MTProto pair is offered only after it has: the schema says «a machine
	// where the direct route works is never sent to my.telegram.org», and
	// offering it up front asks somebody to register an application they will
	// never need.
	//
	// Per server rather than per package, and not stored at all. Per package it
	// would leak between tests and make them order-dependent; stored it would
	// keep offering the pair long after the network came back.
	botAPIFailed atomic.Bool

	// lastList is the parse report of the most recent proxy list, so the first
	// unreadable line survives the redirect that follows the save.
	lastList atomic.Pointer[listReport]

	tmpl     *template.Template
	tmplOnce sync.Once
	tmplErr  error
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static assets are served without authentication: they are the same bytes
	// for everybody and carry nothing about this installation. Behind the
	// password, the only thing an unauthenticated visitor gets is an unstyled
	// prompt.
	mux.Handle("GET /static/", http.FileServerFS(staticFS))

	mux.Handle("GET /", s.auth(http.HandlerFunc(s.watchesPage)))
	mux.Handle("POST /watches", s.auth(http.HandlerFunc(s.saveWatch)))
	mux.Handle("POST /watches/delete", s.auth(http.HandlerFunc(s.deleteWatch)))
	mux.Handle("POST /watches/toggle", s.auth(http.HandlerFunc(s.toggleWatch)))

	mux.Handle("GET /first-run", s.auth(http.HandlerFunc(s.firstRunPage)))
	mux.Handle("POST /first-run", s.auth(http.HandlerFunc(s.saveFirstRun)))

	mux.Handle("GET /settings", s.auth(http.HandlerFunc(s.settingsPage)))
	mux.Handle("POST /settings", s.auth(http.HandlerFunc(s.saveSettings)))
	mux.Handle("GET /settings/blanktrail", s.auth(http.HandlerFunc(s.checkBlankTrail)))
	mux.Handle("GET /settings/telegram", s.auth(http.HandlerFunc(s.checkTelegram)))

	mux.Handle("GET /health", s.auth(http.HandlerFunc(s.healthPage)))

	mux.Handle("GET /results", s.auth(http.HandlerFunc(s.resultsPage)))
	mux.Handle("GET /results/table", s.auth(http.HandlerFunc(s.resultsFragment)))
	mux.Handle("GET /results/export", s.auth(http.HandlerFunc(s.exportResults)))

	mux.Handle("GET /history", s.auth(http.HandlerFunc(s.historyPage)))
	mux.Handle("GET /history/chart", s.auth(http.HandlerFunc(s.historyChart)))

	mux.Handle("GET /channels", s.auth(http.HandlerFunc(s.channelsPage)))
	mux.Handle("POST /channels", s.auth(http.HandlerFunc(s.saveChannel)))
	mux.Handle("POST /channels/delete", s.auth(http.HandlerFunc(s.deleteChannel)))
	mux.Handle("GET /channels/test", s.auth(http.HandlerFunc(s.testChannel)))
	mux.Handle("GET /channels/gateways", s.auth(http.HandlerFunc(s.listGateways)))

	return mux
}

// auth is the password gate, when there is one.
//
// Off by default. The server listens on the loopback interface, so what a
// password keeps out here is another program or another account on this same
// machine — worth having on a shared machine, worth nothing on a personal one,
// and not something to decide on somebody's behalf.
//
// Basic authentication rather than a login form and a session cookie, which
// would buy nothing: there is one user, and a cookie would add a store, an
// expiry and a logout button to arrive at the same place.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read per request rather than at start, so that setting a password
		// takes effect on the next page instead of the next restart — a setting
		// that needs a restart is a setting people believe they changed.
		p, err := s.Store.PanelSettings(r.Context())
		if err != nil {
			http.Error(w, "settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if p.PasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}
		_, given, ok := r.BasicAuth()
		if !ok || !VerifyPassword(p.PasswordHash, given) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Avito Monitor"`)
			http.Error(w, "требуется пароль", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListenAddress decides where to listen.
//
// lan comes from the setting the user ticked. The refusal is here rather than in
// the settings screen because a setting can be written by anything — a
// hand-edited database, a restored backup, a future importer — and the only
// place that reliably sees the combination is the one about to open the port.
func (s *Server) ListenAddress(ctx context.Context, port int, lan bool) (string, error) {
	if !lan {
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
	p, err := s.Store.PanelSettings(ctx)
	if err != nil {
		return "", err
	}
	if p.PasswordHash == "" {
		return "", ErrWouldExposeUnprotectedPanel
	}
	return fmt.Sprintf(":%d", port), nil
}

// tabs is the header navigation.
//
// Правила arrives with Task 6. A tab that answers 404 is a tab a person clicks
// once and stops trusting, so each is added when its screen is.
func (s *Server) tabs(path string) []Tab {
	out := []Tab{
		{Label: "Наблюдения", Href: "/"},
		{Label: "Результаты", Href: "/results"},
		{Label: "Здоровье сбора", Href: "/health"},
		{Label: "История", Href: "/history"},
		{Label: "Прокси", Href: "/channels"},
		{Label: "Настройки", Href: "/settings"},
	}
	claimed := false
	for i := range out {
		// A prefix and not an equality, because История is reached as
		// /history?thread=100 and its chart as /history/chart. A person who
		// followed a link into a chart and sees «Наблюдения» highlighted has
		// been told they are somewhere they are not.
		out[i].Active = out[i].Href == path ||
			(out[i].Href != "/" && strings.HasPrefix(path, out[i].Href))
		claimed = claimed || out[i].Active
	}
	// The first tab is active for anything not otherwise claimed, so a screen
	// reached from a banner still highlights one.
	if !claimed {
		out[0].Active = true
	}
	return out
}

// page is what the layout template is given.
type page struct {
	Title     string
	Body      template.HTML
	Tabs      []Tab
	SourceURL string
	// Banner is a persistent warning above everything — a halted watch, a
	// transport nobody has chosen, a disk about to fill. Already rendered.
	Banner template.HTML
}

func (s *Server) templates() (*template.Template, error) {
	s.tmplOnce.Do(func() {
		s.tmpl, s.tmplErr = template.ParseFS(templateFS, "templates/*.html")
	})
	return s.tmpl, s.tmplErr
}

// render writes one full page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, p page) {
	t, err := s.templates()
	if err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.SourceURL = SourceURL
	p.Tabs = s.tabs(r.URL.Path)
	p.Banner = rawHTML(s.banner(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", p); err != nil {
		// The status is already sent by now, so this cannot become a 500.
		// Saying so beats a silently truncated page.
		fmt.Fprintf(w, "<!-- render: %s -->", esc(err.Error()))
	}
}

// banner is what has to be said above every screen.
//
// Three things, in this order, and the order is the urgency: nothing collects
// until a transport is chosen; a halted watch has stopped and will not restart
// itself; a full disk stops collection at the next pass. Each is a state a
// person would otherwise discover as silence.
func (s *Server) banner(ctx context.Context) string {
	var b strings.Builder

	if _, err := s.Store.TransportMode(ctx); errors.Is(err, store.ErrNoTransportChosen) {
		b.WriteString(`<div class="banner warning">` +
			`Не выбран способ выхода в сеть — сбор не идёт. ` +
			`<a href="/first-run">Выбрать</a></div>`)
	}
	if watches, err := s.Store.Watches(ctx); err == nil {
		for _, w := range watches {
			if w.State != "halted" {
				continue
			}
			fmt.Fprintf(&b,
				`<div class="banner bad">Наблюдение «%s» остановлено: %s</div>`,
				esc(w.Name), esc(w.HaltedReason))
		}
	}
	if err := s.Store.CheckSpace(ctx); err != nil {
		fmt.Fprintf(&b, `<div class="banner bad">%s</div>`, esc(err.Error()))
	}
	return b.String()
}
