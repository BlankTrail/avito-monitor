// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// ChangeRow is one row of the changes table.
//
// PassID is a pointer with no foreign key, and that is deliberate: retention
// removes old passes, and a change must outlive the reading it rests on rather
// than be cascaded away with it. "Why was I told this was gone" has to stay
// answerable after the evidence has been thinned.
type ChangeRow struct {
	ID         int64
	Kind       track.Kind
	ItemID     int64
	WatchID    int64
	PassID     *int64
	TS         int64
	Field      string
	Was, Now   string
	Unit       track.Unit
	HadBefore  bool
	HasNow     bool
	Confidence float64
	Backfill   bool
	Producer   string
}

func insertChange(ctx context.Context, tx *sql.Tx, c ChangeRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO changes
		  (kind, item_id, watch_id, pass_id, ts, saved_at, field, was, now,
		   unit, had_before, has_now, confidence, backfill, producer)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.Kind), c.ItemID, c.WatchID, c.PassID, c.TS, c.TS,
		c.Field, c.Was, c.Now, string(c.Unit),
		c.HadBefore, c.HasNow, c.Confidence, c.Backfill, c.Producer)
	if err != nil {
		return fmt.Errorf("store: insert change %s: %w", c.Kind, err)
	}
	return nil
}

const changeColumns = `id, kind, item_id, watch_id, pass_id, ts, field, was, now,
	unit, had_before, has_now, confidence, backfill, producer`

func scanChange(sc scanner) (ChangeRow, error) {
	var c ChangeRow
	var kind, unit string
	err := sc.Scan(&c.ID, &kind, &c.ItemID, &c.WatchID, &c.PassID, &c.TS,
		&c.Field, &c.Was, &c.Now, &unit, &c.HadBefore, &c.HasNow,
		&c.Confidence, &c.Backfill, &c.Producer)
	c.Kind, c.Unit = track.Kind(kind), track.Unit(unit)
	return c, err
}

// Changes reads a watch's changes in the order they were written.
//
// Ordered by id and not by ts: two changes can share a second, and a person
// reading a list wants the order things happened in rather than whichever order
// SQLite found convenient.
func (s *Store) Changes(ctx context.Context, watchID, since int64, limit int) ([]ChangeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+changeColumns+` FROM changes
		WHERE watch_id = ? AND ts >= ?
		ORDER BY id LIMIT ?`, watchID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("store: changes: %w", err)
	}
	defer rows.Close()

	var out []ChangeRow
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("store: changes: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
