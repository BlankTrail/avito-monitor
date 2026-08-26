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
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	srv := buildPanel(s)

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
	if _, err := s.TransportMode(ctx); errors.Is(err, store.ErrNoTransportChosen) {
		fmt.Println("Способ выхода в сеть ещё не выбран — сбор не идёт. " +
			"Откройте панель и ответьте на первый вопрос.")
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

	// The list is read and counted, and nothing is dialled. Dialling every
	// address of a thousand-line list from a settings screen is a thousand
	// connections a person did not ask for; the pool finds out which ones work
	// by using them, and Mixer demotes the ones that do not.
	srv.CheckChannel = func(ctx context.Context, id int64) (string, error) {
		ps, err := s.Proxies(ctx, id)
		if err != nil {
			return "", err
		}
		if len(ps) == 0 {
			return "", errors.New("в этом выходе нет ни одного адреса")
		}
		// The first address by host and port, and never its credential.
		return fmt.Sprintf("%d в списке, первый %s:%d",
			len(ps), ps[0].Host, ps[0].Port), nil
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
