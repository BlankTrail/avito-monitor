// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// This file is what the История screen reads.
//
// Per thread and not per listing, because a listing has incarnations: a seller
// who reposts the same phone produces a new number, and a history drawn per
// listing restarts from nothing every time — which is exactly the history a
// person opened the screen to avoid.

// PricePoint is one reading.
type PricePoint struct {
	TS int64
	// Minor is kopecks, and it is only meaningful when Present is true.
	//
	// Present is false for a reading where the price was read and was not a
	// number — «Цена договорная» — and for one where we could not read it at
	// all. Both are absences and neither is a zero: drawn as zero either is a
	// price crash that never happened.
	Minor   int64
	Present bool
}

// ThreadSeries is one advertisement's history across every number it has worn.
type ThreadSeries struct {
	ThreadID int64
	Title    string
	URLPath  string
	ItemIDs  []int64
	Points   []PricePoint

	// LastPriceMinor and LastSeenAt are filled by ThreadsWithHistory and left
	// zero by ThreadSeries, which has the whole series and needs no summary of
	// it. They exist because the index was a column of bare titles: twelve
	// phones from one shop rendered as «iPhone 14 Pro Max, 256 ГБ, SIM + eSIM»
	// three times over with nothing to tell them apart. Found by opening the
	// page.
	LastPriceMinor *int64
	LastSeenAt     int64
}

// ThreadSeries reads one thread's price history, oldest point first.
//
// Oldest first because internal/chart says its points must rise in TS and does
// not re-sort — a chart that quietly reordered its input would hide the caller's
// bug behind a plausible picture.
func (s *Store) ThreadSeries(ctx context.Context, threadID, since int64) (ThreadSeries, error) {
	out := ThreadSeries{ThreadID: threadID}

	// The head is read without the since cut. The chart may be asked for the
	// last month; the title still has to be there.
	err := s.db.QueryRowContext(ctx, `
		SELECT title, url_path FROM listings
		WHERE thread_id = ?
		ORDER BY first_seen_at DESC, item_id DESC LIMIT 1`, threadID).
		Scan(&out.Title, &out.URLPath)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("store: thread %d: %w", threadID, err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.item_id, s.ts, s.price_kind, s.price_minor
		FROM listing_snapshots s
		JOIN listings l ON l.item_id = s.item_id
		WHERE l.thread_id = ? AND s.ts >= ?
		ORDER BY s.ts, s.id`, threadID, since)
	if err != nil {
		return out, fmt.Errorf("store: thread %d: %w", threadID, err)
	}
	defer rows.Close()

	seenItem := map[int64]bool{}
	for rows.Next() {
		var itemID int64
		var kind string
		var minor sql.NullInt64
		var p PricePoint
		if err := rows.Scan(&itemID, &p.TS, &kind, &minor); err != nil {
			return out, fmt.Errorf("store: thread %d: %w", threadID, err)
		}
		// Present only for a price the site actually stated as a number. The
		// three price kinds are three different statements, and the chart needs
		// exactly this one bit of the difference.
		if kind == "exact" && minor.Valid {
			p.Minor, p.Present = minor.Int64, true
		}
		out.Points = append(out.Points, p)
		if !seenItem[itemID] {
			seenItem[itemID] = true
			out.ItemIDs = append(out.ItemIDs, itemID)
		}
	}
	return out, rows.Err()
}

// ThreadsWithHistory is the list the screen offers before anything is picked.
//
// Heads only — no points. Loading every series to draw a list of names would
// read the whole snapshot table to render a dropdown.
//
// One row per thread and not per listing, which is the whole point: a list with
// the same advertisement twice under two numbers is the repost linking having
// bought nothing.
func (s *Store) ThreadsWithHistory(ctx context.Context, watchID int64, limit int) ([]ThreadSeries, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.thread_id,
		       (SELECT h.title FROM listings h WHERE h.thread_id = l.thread_id
		        ORDER BY h.first_seen_at DESC, h.item_id DESC LIMIT 1),
		       (SELECT h.url_path FROM listings h WHERE h.thread_id = l.thread_id
		        ORDER BY h.first_seen_at DESC, h.item_id DESC LIMIT 1),
		       max(s.ts),
		       -- The price AT that latest snapshot, and nothing else. max()
		       -- over price_minor would answer «the highest price this thread
		       -- ever carried», which on a listing that has come down reads as
		       -- a lie in the column a person is scanning for a bargain.
		       (SELECT CASE WHEN p.price_kind = 'exact' THEN p.price_minor END
		        FROM listing_snapshots p
		        JOIN listings pl ON pl.item_id = p.item_id
		        WHERE pl.thread_id = l.thread_id
		        ORDER BY p.ts DESC LIMIT 1)
		FROM watch_members m
		JOIN listings l ON l.item_id = m.item_id
		JOIN listing_snapshots s ON s.item_id = l.item_id
		WHERE m.watch_id = ?
		GROUP BY l.thread_id
		ORDER BY max(s.ts) DESC
		LIMIT ?`, watchID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: threads with history: %w", err)
	}
	defer rows.Close()

	var out []ThreadSeries
	for rows.Next() {
		var t ThreadSeries
		if err := rows.Scan(&t.ThreadID, &t.Title, &t.URLPath,
			&t.LastSeenAt, &t.LastPriceMinor); err != nil {
			return nil, fmt.Errorf("store: threads with history: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
