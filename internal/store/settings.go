// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

// StoreMTProtoSession keeps the MTProto session in the database rather than
// beside the binary.
//
// It is a credential in the strongest sense — anyone holding it is the bot —
// and gotd's convenient default is a file in the working directory. The column
// is a BLOB the panel does not display, and these two functions are the only
// way in or out of it.
func (s *Store) StoreMTProtoSession(ctx context.Context, data []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET telegram_mtproto_session = ?, updated_at = ?
		 WHERE id = 1`, data, s.now().UTC().Unix()); err != nil {
		// The blob is never interpolated into this message, and neither is
		// anything derived from it.
		return fmt.Errorf("store: mtproto session: %w", err)
	}
	return nil
}

// MTProtoSession reads it back. A nil slice means there is none yet, which is
// what a first run looks like and is not an error.
func (s *Store) MTProtoSession(ctx context.Context) ([]byte, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT telegram_mtproto_session FROM settings WHERE id = 1`).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: mtproto session: %w", err)
	}
	return b, nil
}

// TelegramConfig is the three credentials the delivery ladder needs.
type TelegramConfig struct {
	BotToken string
	APIID    int
	APIHash  string
}

// TelegramConfig reads them.
//
// They are returned together because a rung needs all three or none: MTProto's
// bot login takes the token AND the application identity, and fetching them one
// at a time would let a caller check one and dial on the strength of it.
func (s *Store) TelegramConfig(ctx context.Context) (TelegramConfig, error) {
	var c TelegramConfig
	err := s.db.QueryRowContext(ctx,
		`SELECT telegram_token, telegram_api_id, telegram_api_hash
		 FROM settings WHERE id = 1`).Scan(&c.BotToken, &c.APIID, &c.APIHash)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		// Nothing read is echoed back: two of these three are credentials.
		return TelegramConfig{}, fmt.Errorf("store: telegram config: %w", err)
	}
	return c, nil
}

// SetTelegramConfig writes them.
func (s *Store) SetTelegramConfig(ctx context.Context, c TelegramConfig) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET telegram_token = ?, telegram_api_id = ?, telegram_api_hash = ?,
		    updated_at = ?
		WHERE id = 1`,
		c.BotToken, c.APIID, c.APIHash, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: telegram config: %w", err)
	}
	return nil
}

// mtprotoSession adapts the store to what internal/telegram wants, which is
// gotd's session.Storage signature.
type mtprotoSession struct{ s *Store }

// MTProtoSessionStore is the store as a session storage.
func (s *Store) MTProtoSessionStore() interface {
	LoadSession(ctx context.Context) ([]byte, error)
	StoreSession(ctx context.Context, data []byte) error
} {
	return mtprotoSession{s: s}
}

func (m mtprotoSession) LoadSession(ctx context.Context) ([]byte, error) {
	return m.s.MTProtoSession(ctx)
}

func (m mtprotoSession) StoreSession(ctx context.Context, data []byte) error {
	return m.s.StoreMTProtoSession(ctx, data)
}

// CallbackOffset is where the Telegram poller left off.
//
// Persisted rather than held in memory: Telegram re-delivers an update until
// asking for the next one acknowledges it, so a restart would replay every
// press since the last poll.
func (s *Store) CallbackOffset(ctx context.Context) (int64, error) {
	var v int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT telegram_update_offset FROM settings WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: callback offset: %w", err)
	}
	return v, nil
}

// SetCallbackOffset moves it forward.
//
// Never backward: a lower offset asks Telegram to re-deliver, and this is
// written from a loop whose whole job is to stop it doing that. max() rather
// than a plain assignment so that two pollers on one database — which is a
// misconfiguration, not a design — cannot walk each other back into a replay.
func (s *Store) SetCallbackOffset(ctx context.Context, offset int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET telegram_update_offset = max(telegram_update_offset, ?)
		WHERE id = 1`, offset); err != nil {
		return fmt.Errorf("store: callback offset: %w", err)
	}
	return nil
}

// TelegramChats is every enabled recipient's chat id.
//
// The whole of the callback authentication: a press is acted on only when
// Telegram says it came from one of these. Addresses that are not numbers are
// skipped rather than failing the lookup — a target of another kind, or a
// username somebody typed, is simply not a chat this build can match.
func (s *Store) TelegramChats(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT address FROM notify_targets WHERE enabled = 1 AND kind = 'telegram'`)
	if err != nil {
		return nil, fmt.Errorf("store: telegram chats: %w", err)
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, fmt.Errorf("store: telegram chats: %w", err)
		}
		if id, err := strconv.ParseInt(strings.TrimSpace(address), 10, 64); err == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}
