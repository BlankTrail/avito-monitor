// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/web"
)

func openPanelStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestTheWiringSuppliesEveryCheckTheScreensNeed.
//
// The Server's function fields default to nil, and a nil one is a button that
// answers «эта сборка не умеет». That is right for a test and wrong for the
// product, so this asserts the wiring fills them in.
func TestTheWiringSuppliesEveryCheckTheScreensNeed(t *testing.T) {
	s := buildPanel(openPanelStore(t))
	if s.CheckBlankTrail == nil {
		t.Error("CheckBlankTrail is not wired")
	}
	if s.CheckTelegram == nil {
		t.Error("CheckTelegram is not wired")
	}
	if s.Gateways == nil {
		t.Error("Gateways is not wired")
	}
	if s.CheckChannel == nil {
		t.Error("CheckChannel is not wired")
	}
	if s.Autostart == nil {
		t.Error("Autostart is not wired")
	}
}

// TestThePanelRefusesTheLanBeforeItOpensAPort.
//
// The one thing this command must not get wrong: the refusal has to happen
// before Listen, not after.
func TestThePanelRefusesTheLanBeforeItOpensAPort(t *testing.T) {
	st := openPanelStore(t)
	ctx := context.Background()
	if err := st.SetPanelSettings(ctx, store.PanelSettings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	s := buildPanel(st)
	if _, err := s.ListenAddress(ctx, 8080, true); !errors.Is(err, web.ErrWouldExposeUnprotectedPanel) {
		t.Errorf("err = %v, want the refusal", err)
	}
}

// TestTheChannelCheckReadsAndDoesNotDial.
//
// Dialling every address of a thousand-line list from a settings screen is a
// thousand connections a person did not ask for. The pool finds out which ones
// work by using them, and Mixer demotes the ones that do not.
func TestTheChannelCheckReadsAndDoesNotDial(t *testing.T) {
	st := openPanelStore(t)
	ctx := context.Background()
	id, err := st.SaveChannel(ctx, store.Channel{
		Name: "провайдер", Kind: "proxy-list", Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceProxies(ctx, id, []store.Proxy{
		// A port nothing listens on. If the check dialled, this would fail.
		{ChannelID: id, Scheme: "socks5", Host: "127.0.0.1", Port: 1, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := buildPanel(st).CheckChannel(ctx, id)
	if err != nil {
		t.Fatalf("the check failed on an address nothing answers: %v", err)
	}
	if summary == "" {
		t.Error("the check said nothing")
	}
}

// TestAChannelWithNoAddressesSaysSo, which is a real state — a list whose file
// went missing, or one saved before it was filled in.
func TestAChannelWithNoAddressesSaysSo(t *testing.T) {
	st := openPanelStore(t)
	ctx := context.Background()
	id, err := st.SaveChannel(ctx, store.Channel{
		Name: "пусто", Kind: "proxy-list", Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildPanel(st).CheckChannel(ctx, id); err == nil {
		t.Error("an empty channel reported itself as fine")
	}
}
