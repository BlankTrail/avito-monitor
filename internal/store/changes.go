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
	ID      int64
	Kind    track.Kind
	ItemID  int64
	WatchID int64
	// PassID and SnapshotID are the evidence, and exactly one is set: the
	// window producer points at the pass it read, the series producer at the
	// later of the two snapshots it differenced.
	PassID     *int64
	SnapshotID *int64
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
		  (kind, item_id, watch_id, pass_id, snapshot_id, ts, saved_at,
		   field, was, now, unit, had_before, has_now, confidence, backfill,
		   producer)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.Kind), c.ItemID, c.WatchID, c.PassID, c.SnapshotID, c.TS, c.TS,
		c.Field, c.Was, c.Now, string(c.Unit),
		c.HadBefore, c.HasNow, c.Confidence, c.Backfill, c.Producer)
	if err != nil {
		return fmt.Errorf("store: insert change %s: %w", c.Kind, err)
	}
	return nil
}

const changeColumns = `c.id, c.kind, c.item_id, c.watch_id, c.pass_id,
	c.snapshot_id, c.ts, c.field, c.was, c.now, c.unit, c.had_before, c.has_now,
	c.confidence, c.backfill, c.producer`

func scanChange(sc scanner) (ChangeRow, error) {
	var c ChangeRow
	var kind, unit string
	err := sc.Scan(&c.ID, &kind, &c.ItemID, &c.WatchID, &c.PassID, &c.SnapshotID,
		&c.TS, &c.Field, &c.Was, &c.Now, &unit, &c.HadBefore, &c.HasNow,
		&c.Confidence, &c.Backfill, &c.Producer)
	c.Kind, c.Unit = track.Kind(kind), track.Unit(unit)
	return c, err
}

// Changes reads everything this watch would want to be told about, in the order
// it was written.
//
// Two shapes of row, and the union is the point. The window producer writes
// watch_id, because an appearance or a departure is a fact about a watch's
// result set. The series producer writes watch_id 0, because a price move is a
// fact about a LISTING and one listing belongs to several watches — attributing
// it to one would make the same move three different events, and attributing it
// to all of them at write time would write it three times.
//
// So the join happens here: a series change is this watch's business when the
// listing is one of its members. The member row is never deleted, so a price
// change on a listing that has since vanished is still reported to the watch
// that was following it, which is what somebody asking "what happened to it"
// wants.
//
// Ordered by id and not by ts: two changes can share a second, and a person
// reading a list wants the order things happened in rather than whichever order
// SQLite found convenient.
func (s *Store) Changes(ctx context.Context, watchID, since int64, limit int) ([]ChangeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+changeColumns+` FROM changes c
		WHERE c.ts >= ?
		  AND (c.watch_id = ?
		       OR (c.watch_id = 0 AND EXISTS (
		             SELECT 1 FROM watch_members m
		             WHERE m.watch_id = ? AND m.item_id = c.item_id)))
		ORDER BY c.id LIMIT ?`, since, watchID, watchID, limit)
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
