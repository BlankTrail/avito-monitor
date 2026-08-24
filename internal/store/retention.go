// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"
)

// The week and day dividers the SQL below buckets rows into are written as
// literals, because a bucket width is baked into a CASE expression rather than
// compared against a column and so cannot be bound as a query parameter.
//
//	86400   seconds in a day
//	604800  seconds in a week
//	345600  1970-01-05 00:00 UTC, the first Monday. The epoch itself fell on a
//	        Thursday, so ts/604800 would cut weeks at Thursday midnight and
//	        file a Sunday together with the Monday after it. Shifting by this
//	        makes a week mean Monday through Sunday, which is the week the
//	        person reading the chart has in mind.
//
// Lifted from WB_GO's internal/store/retention.go, with (item_id, surface) in
// place of (nm_id, dest, app_type).

// thinSnapshotsSQL keeps one snapshot per listing, surface and period, and
// deletes the rest.
//
// The ORDER BY is the whole rule:
//
//   - anchor ASC puts real changes ahead of anchors. An anchor carries the
//     same values as the change before it under a later, arbitrary timestamp,
//     so keeping it would preserve the value and lose the moment it moved.
//     Only a period that holds nothing but anchors keeps one, as evidence the
//     listing was still being watched.
//   - ts DESC keeps the last row of the period. A snapshot is written only
//     when something changed, so the series is a step function and the last
//     row is the only one still in force when the period ended; every earlier
//     row was already superseded inside the same period. Keeping the first
//     instead would redraw each period with the previous period's value and
//     would tear the seam between the thinned and the fresh window.
//   - id DESC breaks ties. Two passes can land in the same second, and without
//     this SQLite may keep either row: two copies of one database would thin
//     differently.
//
// The two CASE expressions place a row in its zone and its bucket. Both read
// the same weekly cutoff, so a row is bucketed by week or by day but never by
// both.
const thinSnapshotsSQL = `
DELETE FROM listing_snapshots
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY
                       item_id, surface,
                       CASE WHEN ts < ? THEN 1 ELSE 0 END,
                       CASE WHEN ts < ?
                            THEN (ts - 345600) / 604800
                            ELSE ts / 86400
                       END
                   ORDER BY anchor ASC, ts DESC, id DESC
               ) AS rn
        FROM listing_snapshots
        WHERE ts < ?
    )
    WHERE rn > 1
)`

// thinPassesSQL keeps one healthy pass per watch per period and deletes the
// rest. Only 'ok' and 'empty' are candidates.
//
// The class filter is a whitelist and not "class <> 'ok'", and the difference
// is not stylistic. Reading the rule as "not ok" would keep every empty pass
// for ever, and a watch on a rare query would grow without bound precisely
// because nothing was happening on it. Reading it as a whitelist means a class
// added to the Go catalogue later is kept by default, which is the safe
// direction: keeping too much is a disk-space problem with a lever, and losing
// the health history is a question nobody can answer afterwards.
//
// There is no anchor column here and no tiebreak beyond the id: a pass is a
// real event every time, and two of them can share a second.
const thinPassesSQL = `
DELETE FROM passes
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY
                       watch_id,
                       CASE WHEN started_at < ? THEN 1 ELSE 0 END,
                       CASE WHEN started_at < ?
                            THEN started_at / 86400
                            ELSE started_at / 3600
                       END
                   ORDER BY started_at DESC, id DESC
               ) AS rn
        FROM passes
        WHERE started_at < ? AND class IN ('ok', 'empty')
    )
    WHERE rn > 1
)`

func secs(d time.Duration) int64 { return int64(d / time.Second) }

// ThinSnapshots collapses the listing series to one row per period.
func (s *Store) ThinSnapshots(ctx context.Context, now int64) (int64, error) {
	r := s.Retention()
	weekly := now - secs(r.SnapshotWeeklyAfter)
	daily := now - secs(r.SnapshotDailyAfter)

	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, thinSnapshotsSQL, weekly, weekly, daily)
	if err != nil {
		return 0, fmt.Errorf("store: thin snapshots: %w", err)
	}
	return res.RowsAffected()
}

// ThinPasses collapses the healthy passes and keeps every other class.
//
// This is the rule that is deliberately not the obvious one. Keeping only
// passes that produced a change preferentially destroys the evidence that
// collection FAILED while preserving the evidence that it worked, and makes
// "was collection healthy in early March" unanswerable three weeks later.
func (s *Store) ThinPasses(ctx context.Context, now int64) (int64, error) {
	r := s.Retention()
	dailyAfter := now - secs(r.PassDailyAfter)
	hourlyAfter := now - secs(r.PassHourlyAfter)

	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, thinPassesSQL, dailyAfter, dailyAfter, hourlyAfter)
	if err != nil {
		return 0, fmt.Errorf("store: thin passes: %w", err)
	}
	return res.RowsAffected()
}

// SweepBodies bounds the archive twice: by age, then by size.
//
// An archive with an age rule and no size rule is unbounded on a broken
// install, which is exactly when it grows fastest — a blocked collector is
// what writes the failed passes that earn a body in the first place.
//
// The size pass deletes oldest-first. Newest-first would keep the pages of an
// outage's beginning and throw away its resolution, which is the half a person
// reads.
func (s *Store) SweepBodies(ctx context.Context, now int64) (int64, error) {
	r := s.Retention()

	s.wmu.Lock()
	defer s.wmu.Unlock()

	var deleted int64
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM bodies WHERE first_seen_at < ?`, now-secs(r.BodyMaxAge))
	if err != nil {
		return 0, fmt.Errorf("store: sweep bodies: age: %w", err)
	}
	n, _ := res.RowsAffected()
	deleted += n

	// The size bound is over bytes on disk rather than size_raw: what fills a
	// disk is what is stored, and a compressed body costs what it costs.
	for {
		var total int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT coalesce(sum(length(bytes)), 0) FROM bodies`).Scan(&total); err != nil {
			return deleted, fmt.Errorf("store: sweep bodies: measure: %w", err)
		}
		if total <= r.BodyMaxBytes {
			return deleted, nil
		}
		res, err := s.db.ExecContext(ctx, `
			DELETE FROM bodies WHERE sha256 = (
			    SELECT sha256 FROM bodies ORDER BY first_seen_at ASC, sha256 ASC LIMIT 1
			)`)
		if err != nil {
			return deleted, fmt.Errorf("store: sweep bodies: size: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// Over the bound with nothing left to delete. Impossible unless the
			// bound is smaller than one body, and looping for ever would be a
			// worse answer than stopping.
			return deleted, nil
		}
		deleted += n
	}
}

// checkpoint truncates the write-ahead log.
//
// Under WAL a DELETE writes the freed pages into the log, so a sweep run to
// shrink the file grows it until this runs. Reclaim calls it after every step
// for exactly that reason.
func (s *Store) checkpoint(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	return nil
}
