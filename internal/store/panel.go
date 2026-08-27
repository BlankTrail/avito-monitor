// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// This file is what the panel reads and writes that the collector does not.

// PanelSettings is how the interface is reached.
//
// The zero value is a closed panel: no password, no LAN, no autostart. That is
// the state a fresh install is in, and it is the safe one — the panel holds a
// proxy key, a bot token and everything the monitor has collected.
type PanelSettings struct {
	// PasswordHash is empty when no password has been set. It is a hash and
	// never a password: this column is in a file people copy, back up and paste
	// into bug reports.
	PasswordHash string
	LAN          bool
	// Autostart is what the user asked for, not what is true. Whether an entry
	// exists is asked of the operating system — see internal/autostart.
	Autostart bool
}

func (s *Store) PanelSettings(ctx context.Context) (PanelSettings, error) {
	var p PanelSettings
	err := s.db.QueryRowContext(ctx,
		`SELECT panel_password_hash, panel_lan, autostart FROM settings WHERE id = 1`).
		Scan(&p.PasswordHash, &p.LAN, &p.Autostart)
	if errors.Is(err, sql.ErrNoRows) {
		return PanelSettings{}, nil
	}
	if err != nil {
		// The hash is never echoed back into this message.
		return PanelSettings{}, fmt.Errorf("store: panel settings: %w", err)
	}
	return p, nil
}

func (s *Store) SetPanelSettings(ctx context.Context, p PanelSettings) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET panel_password_hash = ?, panel_lan = ?, autostart = ?, updated_at = ?
		WHERE id = 1`,
		p.PasswordHash, p.LAN, p.Autostart, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: panel settings: %w", err)
	}
	return nil
}

// Watches is every watch, in id order.
//
// Every one, including the disabled and the halted. DueWatches answers the
// scheduler's question; a screen that showed only what is due would be empty
// for most of every interval, which reads as "my watches are gone".
func (s *Store) Watches(ctx context.Context) ([]Watch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+watchColumns+` FROM watches ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: watches: %w", err)
	}
	defer rows.Close()

	var out []Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("store: watches: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWatch saves the fields a person can edit, and only those.
//
// state, halted_reason, seeded_at, next_due_at, last_total_count and too_broad
// belong to the collector. A save that wrote the form's zero values over them
// would un-seed a watch every time somebody renamed it — making its next pass a
// seeding pass, and silencing a day of alerts for an edit nobody connected to
// it.
func (s *Store) UpdateWatch(ctx context.Context, w Watch) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		UPDATE watches
		SET name = ?, url = ?, surface = ?, max_pages = ?, interval_sec = ?,
		    absence_confirmations = ?, absence_min_span_sec = ?, enabled = ?,
		    updated_at = ?
		WHERE id = ?`,
		w.Name, w.URL, string(w.Surface), w.MaxPages, w.IntervalSec,
		w.AbsenceConfirmations, w.AbsenceMinSpanSec, w.Enabled,
		s.now().UTC().Unix(), w.ID)
	if err != nil {
		return fmt.Errorf("store: update watch %d: %w", w.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchWatch
	}
	return nil
}

// DeleteWatch removes the watch and leaves its history.
//
// passes.watch_id carries no cascade, for the same reason changes.pass_id
// carries none: «почему мне сказали, что это пропало» has to stay answerable
// after somebody tidied up the watch that said it.
func (s *Store) DeleteWatch(ctx context.Context, id int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM watches WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete watch %d: %w", id, err)
	}
	return nil
}

// StoredRetention reads the persisted thresholds.
//
// A zero column means the built-in default and never "no limit". That is not
// defensive tidiness: a zero is what a hand-edited database, a restored backup
// and a future importer all leave, and read as unbounded it is an archive that
// grows without bound on precisely the install that is already broken.
func (s *Store) StoredRetention(ctx context.Context) (Retention, error) {
	d := DefaultRetention()
	var snapDaily, snapWeekly, passHourly, passDaily, bodyAge, anchor int64
	var bodyBytes int64
	err := s.db.QueryRowContext(ctx, `
		SELECT retention_snapshot_daily_sec, retention_snapshot_weekly_sec,
		       retention_pass_hourly_sec, retention_pass_daily_sec,
		       retention_body_max_age_sec, retention_body_max_bytes,
		       retention_anchor_sec
		FROM settings WHERE id = 1`).
		Scan(&snapDaily, &snapWeekly, &passHourly, &passDaily, &bodyAge,
			&bodyBytes, &anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("store: retention: %w", err)
	}

	secs := func(v int64, fallback time.Duration) time.Duration {
		if v <= 0 {
			return fallback
		}
		return time.Duration(v) * time.Second
	}
	return Retention{
		SnapshotDailyAfter:  secs(snapDaily, d.SnapshotDailyAfter),
		SnapshotWeeklyAfter: secs(snapWeekly, d.SnapshotWeeklyAfter),
		PassHourlyAfter:     secs(passHourly, d.PassHourlyAfter),
		PassDailyAfter:      secs(passDaily, d.PassDailyAfter),
		BodyMaxAge:          secs(bodyAge, d.BodyMaxAge),
		BodyMaxBytes:        orDefault(bodyBytes, d.BodyMaxBytes),
		AnchorEvery:         secs(anchor, d.AnchorEvery),
	}, nil
}

func orDefault(v, fallback int64) int64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func (s *Store) SetStoredRetention(ctx context.Context, r Retention) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	sec := func(d time.Duration) int64 { return int64(d / time.Second) }
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET retention_snapshot_daily_sec = ?, retention_snapshot_weekly_sec = ?,
		    retention_pass_hourly_sec = ?, retention_pass_daily_sec = ?,
		    retention_body_max_age_sec = ?, retention_body_max_bytes = ?,
		    retention_anchor_sec = ?, updated_at = ?
		WHERE id = 1`,
		sec(r.SnapshotDailyAfter), sec(r.SnapshotWeeklyAfter),
		sec(r.PassHourlyAfter), sec(r.PassDailyAfter),
		sec(r.BodyMaxAge), r.BodyMaxBytes, sec(r.AnchorEvery),
		s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: retention: %w", err)
	}
	return nil
}

// SpaceFloorBytes is the persisted absolute floor. Zero means the built-in.
func (s *Store) SpaceFloorBytes(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx,
		`SELECT space_floor_bytes FROM settings WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: space floor: %w", err)
	}
	return v, nil
}

func (s *Store) SetSpaceFloorBytes(ctx context.Context, bytes int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET space_floor_bytes = ?, updated_at = ? WHERE id = 1`,
		bytes, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: space floor: %w", err)
	}
	return nil
}

// RequestsPerMinute is the user's own ceiling per egress address.
//
// It is the tighter half of a pair, and the schema's comment says which: the
// ten-minute window of sixty divided out, not the three-and-a-half-second
// per-identity floor. The effective ceiling is the lower of this figure and the
// transport's own.
func (s *Store) RequestsPerMinute(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx,
		`SELECT requests_per_minute FROM settings WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: requests per minute: %w", err)
	}
	return v, nil
}

func (s *Store) SetRequestsPerMinute(ctx context.Context, v int) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET requests_per_minute = ?, updated_at = ? WHERE id = 1`,
		v, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: requests per minute: %w", err)
	}
	return nil
}

// QuietHours is the do-not-disturb window in minutes since local midnight.
//
// -1 in either column means there is no window, which is the schema's default
// and what an empty box on the settings screen means. Not zero: zero is
// midnight, and a window from midnight to midnight is a real thing somebody
// might ask for.
func (s *Store) QuietHours(ctx context.Context) (fromMin, toMin int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT quiet_from_min, quiet_to_min FROM settings WHERE id = 1`).
		Scan(&fromMin, &toMin)
	if errors.Is(err, sql.ErrNoRows) {
		return -1, -1, nil
	}
	if err != nil {
		return -1, -1, fmt.Errorf("store: quiet hours: %w", err)
	}
	return fromMin, toMin, nil
}

func (s *Store) SetQuietHours(ctx context.Context, fromMin, toMin int) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings SET quiet_from_min = ?, quiet_to_min = ?, updated_at = ?
		WHERE id = 1`, fromMin, toMin, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: quiet hours: %w", err)
	}
	return nil
}

// LastPassAt is when the newest pass was written, or zero when there is none.
//
// The panel's answer to «идёт ли сбор». Read from the data and not from a
// setting: a setting says what was intended, and this has to say what happened.
func (s *Store) LastPassAt(ctx context.Context) (int64, error) {
	var at sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT max(started_at) FROM passes`).Scan(&at); err != nil {
		return 0, fmt.Errorf("store: last pass: %w", err)
	}
	return at.Int64, nil
}
