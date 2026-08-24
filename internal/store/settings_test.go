// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// TestTransportModeHasNoDefault is decision 3, enforced where it can actually
// be enforced.
//
// A default here would be a choice made on the user's behalf about whether
// their traffic goes through a paid proxy. The first run asks; until it has
// been answered, collection refuses to start rather than picking one.
func TestTransportModeHasNoDefault(t *testing.T) {
	s := openTemp(t)
	_, err := s.TransportMode(context.Background())
	if !errors.Is(err, ErrNoTransportChosen) {
		t.Fatalf("TransportMode on a fresh database = %v, want ErrNoTransportChosen — "+
			"a default here decides for the user whether their traffic is paid for", err)
	}
}

func TestTransportModeRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for _, m := range []transport.Mode{transport.ModeDirect, transport.ModeBlankTrail} {
		if err := s.SetTransportMode(ctx, m); err != nil {
			t.Fatalf("SetTransportMode(%v): %v", m, err)
		}
		got, err := s.TransportMode(ctx)
		if err != nil {
			t.Fatalf("TransportMode: %v", err)
		}
		if got != m {
			t.Errorf("TransportMode = %v, want %v", got, m)
		}
	}
}

// TestSetTransportModeRefusesAnUnknownMode. The column has no CHECK — the
// catalogue lives in Go — so the guard has to be here.
func TestSetTransportModeRefusesAnUnknownMode(t *testing.T) {
	s := openTemp(t)
	if err := s.SetTransportMode(context.Background(), transport.Mode("carrier-pigeon")); err == nil {
		t.Error("an unknown transport mode was accepted")
	}
}

// TestSettingsIsOneRowAndTheSchemaSaysSo. The accessors read WHERE id = 1
// without checking that anything is there, which is only safe because the
// migration inserts the row and the CHECK forbids a second.
func TestSettingsIsOneRowAndTheSchemaSaysSo(t *testing.T) {
	s := openTemp(t)

	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM settings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("settings has %d rows, want the 1 the migration inserts", n)
	}
	if _, err := s.DB().Exec(`INSERT INTO settings (id, updated_at) VALUES (2, 0)`); err == nil {
		t.Error("a second settings row was accepted; every accessor here reads " +
			"WHERE id = 1 and would silently ignore it")
	}
}

// TestBlankTrailConfigRoundTrips. The key is returned to a caller about to use
// it and goes nowhere else — never logged, never printed, never written to a
// file this program creates.
func TestBlankTrailConfigRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	want := BlankTrailConfig{ControlURL: "http://127.0.0.1:8891", APIKey: "k", Ports: 4}
	if err := s.SetBlankTrailConfig(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.BlankTrailConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("BlankTrailConfig = %+v, want %+v", got, want)
	}
}
