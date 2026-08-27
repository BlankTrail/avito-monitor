// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/export"
)

// This file is what Результаты reads and what the download streams.
//
// It imports internal/export for one struct with no behaviour, and that
// direction is deliberate. The alternatives are worse: the export package
// importing this one would make a file format depend on a schema, and a third
// package holding one struct would exist only to avoid an import. This store
// already imports internal/lifecycle and internal/transport for the same kind
// of thing.

// ExportFilter narrows what is exported, and it is the same filter the screen
// shows. A download that ignored what was on screen is the failure that screen
// would be blamed for.
type ExportFilter struct {
	WatchID int64 // 0 is every watch
	// PresentOnly keeps what is in the window now. Off by default, because
	// somebody exporting to find out what disappeared needs the rows that are
	// gone — which is most of the reason to export at all.
	PresentOnly bool
	// Since is a floor on the snapshot's moment; 0 is everything.
	Since int64
	// PriceMin and PriceMax are minor units, inclusive of both ends. Zero on
	// either side is no bound there.
	PriceMin int64
	PriceMax int64
	// Query is a substring of the title, matched case-insensitively.
	Query string
}

// exportSelect is the one query both the count and the stream run.
//
// One row per listing per watch, carrying that listing's newest snapshot. Not
// one row per snapshot: «Результаты» is what is there now, and the history is
// История's job — a spreadsheet with four rows per phone is one nobody can
// pivot.
//
// The newest snapshot is a correlated subquery on max(ts) rather than a window
// function. Both work in this SQLite; the subquery is served by
// idx_listing_snapshots_item_ts, which this schema already has.
const exportSelect = `
	SELECT l.item_id, l.thread_id, m.watch_id, w.name,
	       l.title, l.url_path,
	       coalesce(s.price_kind, 'absent'), s.price_minor,
	       coalesce(s.currency, 'RUB'), s.discount_pct,
	       coalesce(l.seller_id, 0),
	       m.last_position,
	       m.first_seen_at, m.last_seen_at,
	       coalesce(s.ts, 0), m.present,
	       s.is_xl, s.is_reserved
	FROM watch_members m
	JOIN watches  w ON w.id = m.watch_id
	JOIN listings l ON l.item_id = m.item_id
	LEFT JOIN listing_snapshots s
	       ON s.item_id = l.item_id
	      AND s.ts = (SELECT max(s2.ts) FROM listing_snapshots s2
	                  WHERE s2.item_id = l.item_id)`

// matches is the half of the filter SQL cannot do.
//
// One line, and it is here rather than in the WHERE because SQLite folds case
// for ASCII only. Both the stream and the count go through it, so the number on
// the screen and the number of rows in the file cannot disagree.
func (f ExportFilter) matches(title string) bool {
	q := strings.TrimSpace(f.Query)
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(title), strings.ToLower(q))
}

// where builds the filter's clause and its arguments.
func (f ExportFilter) where() (string, []any) {
	var clauses []string
	var args []any

	if f.WatchID > 0 {
		clauses = append(clauses, "m.watch_id = ?")
		args = append(args, f.WatchID)
	}
	if f.PresentOnly {
		clauses = append(clauses, "m.present = 1")
	}
	if f.Since > 0 {
		clauses = append(clauses, "coalesce(s.ts, 0) >= ?")
		args = append(args, f.Since)
	}
	// A price bound excludes a listing whose price was not read, the same way
	// rules.Filter does: a band cannot include something whose price is
	// unknown, and «Цена договорная» is not «free».
	if f.PriceMin > 0 {
		clauses = append(clauses, "s.price_minor IS NOT NULL AND s.price_minor >= ?")
		args = append(args, f.PriceMin)
	}
	if f.PriceMax > 0 {
		clauses = append(clauses, "s.price_minor IS NOT NULL AND s.price_minor <= ?")
		args = append(args, f.PriceMax)
	}
	// Query is deliberately not here. SQLite's lower() and LIKE both fold ASCII
	// only, so «айфон» would not match «Айфон» — and every title on this site is
	// Cyrillic. It is applied in Go instead, by matches below, where
	// strings.ToLower knows about Cyrillic. Nothing is lost: a substring search
	// uses no index in either language, so the SQL was buying only the illusion
	// of one.
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// ExportRows streams what the filter selects, in the order the site showed it.
//
// Ordered by position and not by item id. The screen carries a «Позиция»
// column, and a table sorted by an invisible key reads as sorted by nothing —
// looking at the rendered page is what found it: positions came out 1, 9, 12,
// 8, 7, 2 down a twelve-row list. Item id is neither position nor age; it was
// documented here as «oldest listing first», which it never guaranteed.
//
// Present rows first, because a listing still in the result set has a position
// that means something and one that has gone does not. Those fall to the end
// in last-seen order, newest first: for something that vanished, when it was
// last seen is the only ordering a person is asking about.
//
// Ties broken by item id so the order is total. An export whose row order
// shifts between two runs of the same filter is an export nobody can diff.
//
// A sequence rather than a slice: the caller writes each row and forgets it,
// and building a slice would hold a hundred thousand listings in memory on a
// machine already running a scraper.
//
// The rows handle is closed when the sequence ends, however it ends — including
// a consumer that stops early, which is what a cancelled download looks like.
func (s *Store) ExportRows(ctx context.Context, f ExportFilter) iter.Seq2[export.Row, error] {
	return func(yield func(export.Row, error) bool) {
		clause, args := f.where()
		rows, err := s.db.QueryContext(ctx,
			exportSelect+clause+" ORDER BY m.watch_id, m.present DESC, "+
				"CASE WHEN m.present THEN m.last_position END, "+
				"m.last_seen_at DESC, l.item_id", args...)
		if err != nil {
			yield(export.Row{}, fmt.Errorf("store: export: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var r export.Row
			var price, discount sql.NullInt64
			var isXL, isReserved sql.NullBool
			if err := rows.Scan(&r.ItemID, &r.ThreadID, &r.WatchID, &r.WatchName,
				&r.Title, &r.URLPath,
				&r.PriceKind, &price, &r.Currency, &discount,
				&r.SellerID, &r.Position,
				&r.FirstSeenAt, &r.LastSeenAt, &r.SnapshotAt, &r.Present,
				&isXL, &isReserved); err != nil {
				yield(export.Row{}, fmt.Errorf("store: export: %w", err))
				return
			}
			// A NULL price is left nil rather than becoming zero. The three
			// price kinds are three different statements and the export writes
			// an empty cell for two of them.
			if price.Valid {
				v := price.Int64
				r.PriceMinor = &v
			}
			if discount.Valid {
				v := discount.Int64
				r.DiscountPct = &v
			}
			if isXL.Valid {
				v := isXL.Bool
				r.IsXL = &v
			}
			if isReserved.Valid {
				v := isReserved.Bool
				r.IsReserved = &v
			}
			if !f.matches(r.Title) {
				continue
			}
			if !yield(r, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(export.Row{}, fmt.Errorf("store: export: %w", err))
		}
	}
}

// CountExportRows is how many rows the same filter would produce.
//
// The screen shows it before anybody downloads anything, because «выгрузить»
// with no idea whether it is forty rows or four hundred thousand is a button
// people press twice.
//
// It walks the same sequence rather than running a count query, and that is not
// laziness: half the filter is applied in Go (see matches), so a SELECT count(*)
// would answer a different question from the one the download answers — and two
// numbers that disagree is a person who thinks their file came out short.
func (s *Store) CountExportRows(ctx context.Context, f ExportFilter) (int, error) {
	n := 0
	for _, err := range s.ExportRows(ctx, f) {
		if err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}
