// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// ErrNoTransportChosen says nobody has answered the question yet.
//
// There is deliberately no default. Choosing one would decide on the user's
// behalf whether their traffic goes through a paid proxy, and the first run
// asks instead — on the command line here, and on the first screen once the
// panel exists in increment 6. Both write this same column.
//
// The schema spells "unanswered" as the empty string rather than as NULL,
// which is why this error exists at all: a NULL would have been a third state
// to handle everywhere, and the empty string is already inside the column's
// CHECK.
var ErrNoTransportChosen = errors.New("store: no transport mode has been chosen")

// settings is one row, and the CHECK on its id says so. The accessors here are
// typed per column rather than a key/value pair, because the columns are typed
// and a key/value store would throw that away — a settings table where
// requests_per_minute can hold "yes" is a settings table that will.
func (s *Store) TransportMode(ctx context.Context) (transport.Mode, error) {
	var v string
	if err := s.db.QueryRowContext(ctx,
		`SELECT transport_mode FROM settings WHERE id = 1`).Scan(&v); err != nil {
		return "", fmt.Errorf("store: transport mode: %w", err)
	}
	if v == "" {
		return "", ErrNoTransportChosen
	}
	return transport.Mode(v), nil
}

// SetTransportMode records the answer.
//
// The value is checked here as well as by the column's CHECK. The constraint is
// what makes the rule true of the database; this is what makes the failure
// readable — a CHECK violation names the table and not the mistake.
func (s *Store) SetTransportMode(ctx context.Context, m transport.Mode) error {
	if m != transport.ModeDirect && m != transport.ModeBlankTrail {
		return fmt.Errorf("store: %q is not a transport mode", m)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET transport_mode = ?, updated_at = ? WHERE id = 1`,
		string(m), s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: set transport mode: %w", err)
	}
	return nil
}

// BlankTrailConfig is what the collector needs to open a pool.
//
// The key is returned to a caller that is about to use it and is never logged,
// never printed and never written to any file this program creates. It lives
// in a column the panel does not display, and this is the only way out of it.
type BlankTrailConfig struct {
	ControlURL string
	APIKey     string
	Ports      int
}

func (s *Store) BlankTrailConfig(ctx context.Context) (BlankTrailConfig, error) {
	var c BlankTrailConfig
	err := s.db.QueryRowContext(ctx,
		`SELECT blanktrail_url, blanktrail_key, blanktrail_ports
		 FROM settings WHERE id = 1`).Scan(&c.ControlURL, &c.APIKey, &c.Ports)
	if err != nil {
		return c, fmt.Errorf("store: blanktrail config: %w", err)
	}
	return c, nil
}

func (s *Store) SetBlankTrailConfig(ctx context.Context, c BlankTrailConfig) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET blanktrail_url = ?, blanktrail_key = ?, blanktrail_ports = ?,
		    updated_at = ?
		WHERE id = 1`,
		c.ControlURL, c.APIKey, c.Ports, s.now().UTC().Unix())
	if err != nil {
		// The key could be in this error's text if the driver echoed the
		// statement's arguments, so the wrap deliberately does not use %w on
		// anything that carries them.
		return fmt.Errorf("store: set blanktrail config: %w", err)
	}
	return nil
}
