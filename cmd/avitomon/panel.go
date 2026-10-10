// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/autostart"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/telegram"
	"github.com/BlankTrail/avito-monitor/internal/web"
)

func panelCmd(args []string) error {
	fs := flag.NewFlagSet("panel", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "файл базы")
	port := fs.Int("port", 8765, "порт")
	noCollect := fs.Bool("no-collect", false,
		"только панель, без сбора: сбор ведёт другой процесс")
	var opts leaserOpts
	fs.StringVar(&opts.control, "control", "", "адрес control API BlankTrail")
	fs.StringVar(&opts.key, "key", "", "ключ BlankTrail (или "+keyEnv+")")
	fs.StringVar(&opts.proxyFile, "proxies", "", "файл со списком прокси")
	fs.StringVar(&opts.statePath, "state", "", "файл сессии прямого режима")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Cancelled when the process is asked to stop, which is what ends every
	// supervised loop. There is no other stop, deliberately: a second way to
	// end one is a second way for one to end silently.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	srv := buildPanel(s)
	// What the screens say about collection comes from the collector itself,
	// not from the age of the last pass: «собирает 5 из 5» over a collector
	// that never started was the lie this replaces.
	collecting := &collectorState{}
	switch {
	case *noCollect:
		// Another process collects; this one cannot see it, and says nothing.
	default:
		collecting.set(false, "сбор ещё не запущен")
		srv.Collector = collecting.State
	}

	panel, err := s.PanelSettings(ctx)
	if err != nil {
		return err
	}
	addr, err := srv.ListenAddress(ctx, *port, panel.LAN)
	if err != nil {
		// Returned rather than logged: somebody who ticked the box and
		// restarted needs to know why nothing came up, on the terminal they are
		// looking at.
		return err
	}

	// Retention and the free-space floor are settings, and the process has to be
	// told them: store.SetRetention holds them in memory, and nothing reads the
	// columns on its own.
	if r, err := s.StoredRetention(ctx); err == nil {
		s.SetRetention(r)
	}
	if floor, err := s.SpaceFloorBytes(ctx); err == nil && floor > 0 {
		s.SetSpaceFloor(floor)
	}

	fmt.Printf("Панель на http://%s\n", addr)
	if panel.PasswordHash == "" && strings.HasPrefix(addr, "127.0.0.1") {
		fmt.Println("Пароля нет — панель открыта только с этой машины.")
	}
	// Collection starts here, in the same process. That is the whole point of
	// this increment: until now a pass happened because somebody typed
	// «avitomon run --once», and six measurement documents in a row recorded
	// that as the one place «the whole product from a browser» was not true.
	//
	// Decision 3 first. No transport chosen means no collection, and saying so
	// on the terminal beats a scheduler looping on an error nobody reads.
	switch _, err := s.TransportMode(ctx); {
	case errors.Is(err, store.ErrNoTransportChosen):
		fmt.Println("Способ выхода в сеть ещё не выбран — сбор не идёт. " +
			"Откройте панель и ответьте на первый вопрос.")
	case err != nil:
		return err
	case *noCollect:
		fmt.Println("Сбор в этом процессе не ведётся: запущено с -no-collect.")
	default:
		// Supervised, so a key pasted or an exit added in the panel takes
		// effect without a restart — see lifecycle.go.
		go superviseCollection(ctx, s, opts, collecting)
	}

	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return hs.ListenAndServe()
}

// buildPanel wires the live third parties the screens need.
//
// Every one is a field on the Server rather than a call inside internal/web, so
// that package's tests need no network, no licensed proxy and no registry. This
// function is where the product differs from the suite, and it is deliberately
// the only place.
func buildPanel(s *store.Store) *web.Server {
	srv := &web.Server{Store: s, Now: time.Now}

	// The replay behind «Это не перепубликация». The panel does the pin and the
	// split itself; this is the third act, and it lives here because
	// enqueueing a message means rendering one and internal/web must not learn
	// how a notification is worded.
	//
	// It writes a listing-appeared change and runs the rules over that one
	// change immediately, rather than leaving it for the next pass. A person
	// who has just corrected the product should not have to wait five minutes
	// to find out whether the correction produced anything — and the change is
	// written whatever the rules decide, so the record of it is not contingent
	// on a rule existing.
	srv.ReplayAppearance = func(ctx context.Context, itemID int64) (int, error) {
		return replayAppearance(ctx, s, itemID)
	}

	srv.CheckBlankTrail = func(ctx context.Context, url, apiKey string) error {
		c, err := blanktrail.NewClient(url, apiKey)
		if err != nil {
			return err
		}
		return c.Health(ctx)
	}

	srv.CheckTelegram = func(ctx context.Context, token string) (string, error) {
		return telegram.NewBot(token, nil).GetMe(ctx)
	}

	srv.Gateways = func(ctx context.Context) (blanktrail.GatewayList, error) {
		cfg, err := s.BlankTrailConfig(ctx)
		if err != nil {
			return blanktrail.GatewayList{}, err
		}
		c, err := blanktrail.NewClient(cfg.ControlURL, cfg.APIKey)
		if err != nil {
			return blanktrail.GatewayList{}, err
		}
		return c.Gateways(ctx)
	}

	// A real check: a sample of a list's exits opening TLS to the site, every
	// gateway of a set through BlankTrail. See probe.go for why counting the
	// lines was not one.
	srv.CheckChannel = func(ctx context.Context, id int64) (string, error) {
		return checkChannel(ctx, s, id)
	}

	srv.Autostart = osAutostart{}
	return srv
}

// osAutostart is this platform's start-at-login entry, as internal/web wants it.
type osAutostart struct{}

func (osAutostart) Enabled() (bool, error) { return autostart.Enabled() }

func (osAutostart) Enable() error {
	// The panel command, not the bare binary: an entry that started the process
	// with no arguments would start something that prints usage and exits.
	c, err := autostart.Self("panel")
	if err != nil {
		return err
	}
	return autostart.Enable(c)
}

func (osAutostart) Disable() error { return autostart.Disable() }
