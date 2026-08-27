// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// ErrNoSuchWatch is returned by Watch for an id that is not there.
var ErrNoSuchWatch = errors.New("store: no such watch")

// Watch is one thing being monitored.
//
// It carries the columns increment 3 reads and writes. The URL parsing half —
// query, category_id, the opaque f= blob — belongs to avito.SearchURL in
// increment 4, and the columns for it are already in the schema. Filling them
// from a second, half-written parser here would be two parsers to keep in step.
type Watch struct {
	ID                   int64
	Name                 string
	Kind                 string
	URL                  string
	Surface              avito.Surface
	MaxPages             int
	IntervalSec          int
	AbsenceConfirmations int
	AbsenceMinSpanSec    int
	// RepostWindowSec is how long after a listing ended a new one may still be
	// called its successor. Thirty days by default — declared in migration 0001
	// and read by nobody until the linker.
	RepostWindowSec int
	State           string
	HaltedReason    string
	// HaltedAt is when the wall was hit. The banner carries it because «this
	// watch is stopped» and «this watch has been stopped since Tuesday» are
	// different sentences, and only the second tells somebody whether to act.
	HaltedAt       *int64
	SeededAt       *int64
	NextDueAt      int64
	LastTotalCount *int64
	TooBroad       bool
	Enabled        bool
	CreatedAt      int64
	UpdatedAt      int64
}

const watchColumns = `id, name, kind, url, surface, max_pages, interval_sec,
	absence_confirmations, absence_min_span_sec, repost_window_sec,
	state, halted_reason,
	halted_at, seeded_at, next_due_at, last_total_count, too_broad, enabled,
	created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanWatch(sc scanner) (Watch, error) {
	var w Watch
	var surface string
	err := sc.Scan(&w.ID, &w.Name, &w.Kind, &w.URL, &surface, &w.MaxPages,
		&w.IntervalSec, &w.AbsenceConfirmations, &w.AbsenceMinSpanSec,
		&w.RepostWindowSec, &w.State, &w.HaltedReason, &w.HaltedAt,
		&w.SeededAt, &w.NextDueAt,
		&w.LastTotalCount, &w.TooBroad, &w.Enabled, &w.CreatedAt, &w.UpdatedAt)
	w.Surface = avito.Surface(surface)
	return w, err
}

func (s *Store) AddWatch(ctx context.Context, w Watch) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO watches
		  (name, kind, url, surface, max_pages, interval_sec,
		   absence_confirmations, absence_min_span_sec, next_due_at,
		   enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.Name, w.Kind, w.URL, string(w.Surface), w.MaxPages, w.IntervalSec,
		w.AbsenceConfirmations, w.AbsenceMinSpanSec, w.NextDueAt,
		w.Enabled, w.CreatedAt, w.UpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("store: add watch: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) Watch(ctx context.Context, id int64) (Watch, error) {
	w, err := scanWatch(s.db.QueryRowContext(ctx,
		`SELECT `+watchColumns+` FROM watches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, ErrNoSuchWatch
	}
	if err != nil {
		return Watch{}, fmt.Errorf("store: watch %d: %w", id, err)
	}
	return w, nil
}

// DueWatches is the scheduler's only question, and idx_watches_due is the index
// that answers it: enabled, idle, and past its time.
//
// 'running' is excluded as well as 'halted'. A watch already in flight must not
// be picked up twice by a scheduler that ticked while the first pass was still
// going — that would be two requests where the budget accounted for one, on a
// target where the request is the scarce thing.
func (s *Store) DueWatches(ctx context.Context, now int64, limit int) ([]Watch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+watchColumns+` FROM watches
		WHERE enabled = 1 AND state = 'idle' AND next_due_at <= ?
		ORDER BY next_due_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("store: due watches: %w", err)
	}
	defer rows.Close()
	var out []Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("store: due watches: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkHalted stops a watch and records why.
//
// halted_reason is never cleared by this program. A monitor that decides on its
// own that the wall it hit has gone away is a monitor that resumes into it all
// night; clearing it is a person's act, in the panel.
func (s *Store) MarkHalted(ctx context.Context, id int64, reason string, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: halt watch %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	// Halting a watch that is already halted writes nothing. Otherwise a wall
	// that is hit on every pass would produce a change per pass, and the rule's
	// six-hour floor would be suppressing a flood instead of describing a
	// state.
	res, err := tx.ExecContext(ctx, `
		UPDATE watches SET state = 'halted', halted_reason = ?, halted_at = ?,
		       updated_at = ?
		WHERE id = ? AND state <> 'halted'`, reason, at, at, id)
	if err != nil {
		return fmt.Errorf("store: halt watch %d: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return tx.Commit()
	}

	// Said out loud. Without this the watch stops and the only evidence is a
	// panel nobody has open — «Telegram gets exactly one message» needs a
	// change row to be the message, and there was none.
	if err := insertChange(ctx, tx, ChangeRow{
		Kind: track.KindTransportBlocked, WatchID: id, TS: at,
		Field: "halt", Now: reason, HasNow: true,
		Confidence: 1.0, Producer: "transport",
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNextDue schedules the next pass.
//
// It does not touch state, so a halted watch stays halted even if a caller
// schedules it: the two are independent, and a scheduler that could un-halt a
// watch by moving its clock would defeat MarkHalted entirely.
func (s *Store) SetNextDue(ctx context.Context, id, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE watches SET next_due_at = ?, updated_at = ? WHERE id = ?`,
		at, at, id)
	if err != nil {
		return fmt.Errorf("store: schedule watch %d: %w", id, err)
	}
	return nil
}

// ResumeWatch clears a halt. A person's act and never the program's.
//
// The spec is exact about what it writes and about what it does not do: state
// back to idle, the reason cleared, halted_at cleared, next_due_at now. It
// re-tests nothing and promises nothing — if the wall is still there the next
// pass halts again with a fresh reason, which costs one request and buys a
// truthful answer.
//
// The resumption is recorded as a change marked backfill, so the notifier skips
// it. An un-halt that sent «your install is blocked» through the global rule
// would be a lie in the one message that has to be trustworthy.
func (s *Store) ResumeWatch(ctx context.Context, id, at int64) (bool, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: resume watch %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	var reason string
	if err := tx.QueryRowContext(ctx,
		`SELECT halted_reason FROM watches WHERE id = ? AND state = 'halted'`,
		id).Scan(&reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Not halted. Not an error either: «возобновить всё» over a list
			// where one has already recovered must not fail on it.
			return false, nil
		}
		return false, fmt.Errorf("store: resume watch %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE watches
		SET state = 'idle', halted_reason = '', halted_at = NULL,
		    next_due_at = ?, updated_at = ?
		WHERE id = ?`, at, at, id); err != nil {
		return false, fmt.Errorf("store: resume watch %d: %w", id, err)
	}
	if err := insertChange(ctx, tx, ChangeRow{
		Kind: track.KindTransportBlocked, WatchID: id, TS: at,
		Field: "halt", Was: reason, HadBefore: true,
		// Backfill, so the notifier skips it. The reason travels in Was so the
		// panel can say what was cleared.
		Backfill: true, Confidence: 1.0, Producer: "user",
	}); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// HaltedWatches is every watch behind a wall, for «возобновить всё».
func (s *Store) HaltedWatches(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM watches WHERE state = 'halted' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: halted watches: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: halted watches: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
