// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// snapshotRow is one listing_snapshots row, as the series producer reads it.
type snapshotRow struct {
	id      int64
	itemID  int64
	surface string
	ts      int64
	anchor  bool

	priceKind   string
	priceMinor  *int64
	currency    string
	discountPct *int64
	title       string

	isXL, isRemap, isReserved *bool
	sortTSMs                  *int64
}

func (r snapshotRow) reading() track.Reading {
	// title is NOT NULL DEFAULT '' in the schema, so an empty string means the
	// extractor read nothing rather than the seller writing nothing. Passing it
	// through as a nil keeps track.Diff silent about it, which is right: our
	// own blindness is not the seller renaming anything.
	var title *string
	if r.title != "" {
		t := r.title
		title = &t
	}
	return track.Reading{
		ItemID: r.itemID, Surface: r.surface, TS: r.ts,
		PriceKind: r.priceKind, PriceMinor: r.priceMinor, Currency: r.currency,
		DiscountPct: r.discountPct, Title: title,
		IsXL: r.isXL, IsRemap: r.isRemap, IsReserved: r.isReserved,
		SortTSMs: r.sortTSMs,
	}
}

// ProduceSeries writes the price and title history that two snapshots imply.
//
// Out of band, and not inside the pass transaction, for two reasons. A price
// move is a property of two snapshots rather than of one window; and holding
// the single write lock while diffing every listing on a page would make the
// pass as slow as the market is busy, on a target where the pass has a budget.
//
// It walks new snapshots in id order and differences each against the one
// before it for the same (item, surface). Anchor rows are skipped as the LATER
// side of a pair: an anchor carries the same values as the change before it
// under a later timestamp, so differencing against it would produce nothing
// while advancing the baseline past a real reading.
func (s *Store) ProduceSeries(ctx context.Context) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	from, err := s.watermark(ctx)
	if err != nil {
		return 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, item_id, surface, ts, anchor,
		       price_kind, price_minor, currency, discount_pct, title,
		       is_xl, is_remap, is_reserved
		FROM listing_snapshots
		WHERE id > ?
		ORDER BY id`, from)
	if err != nil {
		return 0, fmt.Errorf("store: series: read snapshots: %w", err)
	}
	var fresh []snapshotRow
	for rows.Next() {
		var r snapshotRow
		if err := rows.Scan(&r.id, &r.itemID, &r.surface, &r.ts, &r.anchor,
			&r.priceKind, &r.priceMinor, &r.currency, &r.discountPct, &r.title,
			&r.isXL, &r.isRemap, &r.isReserved); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: series: read snapshots: %w", err)
		}
		fresh = append(fresh, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: series: read snapshots: %w", err)
	}
	if len(fresh) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: series: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx's Rollback is a no-op

	written := 0
	highest := from
	for _, r := range fresh {
		highest = r.id
		if r.anchor {
			continue
		}
		prev, ok, err := previousSnapshot(ctx, tx, r)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue // the first reading of this listing: nothing to differ from
		}
		changes, err := track.Diff(prev.reading(), r.reading())
		if err != nil {
			// Two different listings or two surfaces. The query cannot produce
			// either, so this is a programming error rather than a data one and
			// it stops the run instead of being counted.
			return 0, fmt.Errorf("store: series: diff %d: %w", r.itemID, err)
		}
		for _, c := range changes {
			id := r.id
			if err := insertChange(ctx, tx, ChangeRow{
				Kind: c.Kind, ItemID: c.ItemID, WatchID: 0,
				TS: c.TS, Field: c.Field, Was: c.Was, Now: c.Now,
				Unit: c.Unit, HadBefore: c.HadBefore, HasNow: c.HasNow,
				Confidence: c.Confidence, Producer: "series",
				SnapshotID: &id,
			}); err != nil {
				return 0, err
			}
			written++
		}
	}

	if err := setWatermarkTx(ctx, tx, highest); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: series: commit: %w", err)
	}
	return written, nil
}

// previousSnapshot is the row before this one for the same listing and surface.
//
// Anchors are excluded from the EARLIER side too. An anchor duplicates the
// values of the change before it, so comparing against one would silently
// compare against a stale reading whenever a real change and an anchor landed
// out of order.
func previousSnapshot(ctx context.Context, tx *sql.Tx, r snapshotRow) (snapshotRow, bool, error) {
	var p snapshotRow
	err := tx.QueryRowContext(ctx, `
		SELECT id, item_id, surface, ts, anchor,
		       price_kind, price_minor, currency, discount_pct, title,
		       is_xl, is_remap, is_reserved
		FROM listing_snapshots
		WHERE item_id = ? AND surface = ? AND id < ? AND anchor = 0
		ORDER BY id DESC LIMIT 1`, r.itemID, r.surface, r.id).
		Scan(&p.id, &p.itemID, &p.surface, &p.ts, &p.anchor,
			&p.priceKind, &p.priceMinor, &p.currency, &p.discountPct, &p.title,
			&p.isXL, &p.isRemap, &p.isReserved)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return p, false, nil
	case err != nil:
		return p, false, fmt.Errorf("store: series: previous snapshot: %w", err)
	}
	return p, true, nil
}

// watermark is the highest listing_snapshots id this producer has read.
//
// It lives on the settings row, which is already where this program keeps its
// own state; a second key/value table beside it would be two places to look for
// the same kind of thing.
func (s *Store) watermark(ctx context.Context) (int64, error) {
	var v int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT series_watermark FROM settings WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: series: watermark: %w", err)
	}
	return v, nil
}

// setWatermarkTx moves it inside the same transaction that wrote the changes,
// so a crash between the two cannot lose changes or write them twice.
func setWatermarkTx(ctx context.Context, tx *sql.Tx, v int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE settings SET series_watermark = ? WHERE id = 1`, v); err != nil {
		return fmt.Errorf("store: series: watermark: %w", err)
	}
	return nil
}

// Saturation is how many slots the window has per arrival.
//
// items_on_page / arrivals, over the last n admissible passes. The spec's
// orientation and not its reciprocal, and the difference is the whole reading:
// **below two the watch is losing listings between polls.** A watch of thirty
// slots taking thirty new listings every pass scores one — the window turned
// over completely, and the watch is blind to whatever went through in between.
// The same watch taking three scores ten, and sees everything.
//
// Zero arrivals is not a division and not an alarm. It is a quiet query, and
// the bool is false so the caller says «нечего измерять» rather than showing a
// ratio: the spec is explicit that a metric inventing a number where it has no
// data is worse than one that says it has none — and a zero here would read as
// «below two», which is the opposite of the truth.
//
// The bool is also false with fewer than n admissible passes. A ratio over one
// pass is noise, and this is a number a person acts on.
func (s *Store) Saturation(ctx context.Context, watchID int64, n int) (float64, bool, error) {
	var passes int
	var items, arrivals sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(items_seen), 0), coalesce(sum(arrivals), 0)
		FROM (
		    SELECT items_seen, arrivals FROM passes
		    WHERE watch_id = ? AND evidential = 1
		    ORDER BY id DESC LIMIT ?
		)`, watchID, n).Scan(&passes, &items, &arrivals)
	if err != nil {
		return 0, false, fmt.Errorf("store: saturation: %w", err)
	}
	if passes < n || arrivals.Int64 == 0 {
		return 0, false, nil
	}
	return float64(items.Int64) / float64(arrivals.Int64), true, nil
}
