// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"sort"
)

// This file is what the Здоровье сбора screen reads.
//
// Its whole reason is one sentence of the spec: a monitor being flagged, a
// monitor being throttled and a quiet market look identical from everywhere
// else in the product. Every figure here exists to tell those three apart.
//
// A figure with no data says so rather than inventing a number — see
// CardYieldOK, and LastEvidentialAt's zero. That is the spec's own rule about
// saturation applied everywhere: a metric that invents a number where it has no
// data is worse than one that says it has none.

// PathShare is how many passes one extraction path answered.
type PathShare struct {
	Path  string
	Count int
}

// PathShares is the share of passes each path answered since a moment.
//
// The earliest warning there is, on the desktop surface: a silent switch from
// state to dom means the hydration catalogue was withheld, which is this
// install having been classified — earlier than a 429 and much earlier than a
// captcha. On mobile dom is what a healthy pass has always answered, so there
// the signal is card yield instead; the screen says which surface it is looking
// at rather than alarming on both.
func (s *Store) PathShares(ctx context.Context, since int64) ([]PathShare, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, count(*) FROM passes
		WHERE started_at >= ?
		GROUP BY path ORDER BY path`, since)
	if err != nil {
		return nil, fmt.Errorf("store: path shares: %w", err)
	}
	defer rows.Close()

	var out []PathShare
	for rows.Next() {
		var p PathShare
		if err := rows.Scan(&p.Path, &p.Count); err != nil {
			return nil, fmt.Errorf("store: path shares: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClassHour is one bucket of the block picture.
type ClassHour struct {
	Hour  int64
	Class string
	Count int
}

// ClassByHour is the block picture: what the site answered, by class and hour.
//
// By hour rather than by day because throttling arrives and clears inside one,
// and a daily bucket shows an install that was blocked for forty minutes as a
// day that was mostly fine.
func (s *Store) ClassByHour(ctx context.Context, since int64) ([]ClassHour, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at / 3600) * 3600 AS hour, class, count(*)
		FROM passes
		WHERE started_at >= ?
		GROUP BY hour, class
		ORDER BY hour, class`, since)
	if err != nil {
		return nil, fmt.Errorf("store: class by hour: %w", err)
	}
	defer rows.Close()

	var out []ClassHour
	for rows.Next() {
		var c ClassHour
		if err := rows.Scan(&c.Hour, &c.Class, &c.Count); err != nil {
			return nil, fmt.Errorf("store: class by hour: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WatchHealth is one watch's state of collection.
type WatchHealth struct {
	WatchID    int64
	Name       string
	Surface    string
	Passes     int
	Evidential int
	// LastEvidentialAt is zero when there has never been one, and the screen
	// renders that as a word. A watch created five minutes ago and a watch that
	// has been soft-blocked since March are both «no evidential pass», and 1970
	// is not a date either of them has anything to do with.
	LastEvidentialAt int64
	LastPassAt       int64
	// CardYield is the newest ok pass's items_seen over the median of the last
	// ten. CardYieldOK is false until there are ten, because the alarm is not
	// armed before that and a ratio from three passes is a number that means
	// nothing dressed as one that means something.
	CardYield   float64
	CardYieldOK bool
}

// cardYieldWindow is how many ok passes the median is taken over. The spec's
// number, and it is what arms the alarm.
const cardYieldWindow = 10

// WatchHealth is every watch with its collection state, in id order.
//
// Every watch, including one that has never been read: a watch missing from
// this screen is a watch nobody can see is broken.
func (s *Store) WatchHealth(ctx context.Context, since int64) ([]WatchHealth, error) {
	watches, err := s.Watches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WatchHealth, 0, len(watches))
	for _, w := range watches {
		h := WatchHealth{WatchID: w.ID, Name: w.Name, Surface: string(w.Surface)}
		if err := s.db.QueryRowContext(ctx, `
			SELECT count(*),
			       coalesce(sum(evidential), 0),
			       coalesce(max(started_at), 0),
			       coalesce(max(CASE WHEN evidential = 1 THEN started_at END), 0)
			FROM passes WHERE watch_id = ? AND started_at >= ?`,
			w.ID, since).Scan(&h.Passes, &h.Evidential, &h.LastPassAt,
			&h.LastEvidentialAt); err != nil {
			return nil, fmt.Errorf("store: watch health %d: %w", w.ID, err)
		}
		h.CardYield, h.CardYieldOK, err = s.cardYield(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// cardYield is the newest ok pass's items_seen against the median of the last
// ten ok passes.
//
// Only ok passes. A soft block returns no cards at all, and counting its zero
// would drag the median to nothing and disarm the one alarm that would have
// caught it.
//
// The median and not the mean, because one busy pass in ten moves a mean and
// does not move a median — and what this is looking for is a step change in what
// the page carries, not a busy afternoon.
func (s *Store) cardYield(ctx context.Context, watchID int64) (float64, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT items_seen FROM passes
		WHERE watch_id = ? AND class = 'ok'
		ORDER BY id DESC LIMIT ?`, watchID, cardYieldWindow)
	if err != nil {
		return 0, false, fmt.Errorf("store: card yield: %w", err)
	}
	defer rows.Close()

	var seen []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return 0, false, fmt.Errorf("store: card yield: %w", err)
		}
		seen = append(seen, n)
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("store: card yield: %w", err)
	}
	if len(seen) < cardYieldWindow {
		return 0, false, nil
	}

	newest := seen[0]
	window := append([]int(nil), seen...)
	sort.Ints(window)
	median := float64(window[len(window)/2])
	if median == 0 {
		// Every one of the last ten ok passes returned nothing. That is a real
		// state and it is not a ratio: the screen says so in words, and a
		// division here would be by zero.
		return 0, false, nil
	}
	return float64(newest) / median, true, nil
}

// passDurationWindow is how many recent passes a duration verdict rests on.
//
// Five, and fewer than cardYieldWindow's ten on purpose: a watch that cannot
// keep its schedule is a fact about the transport, which does not drift the way
// a result set does, and waiting for ten passes at seven minutes each means
// waiting most of an hour to be told something the second pass already showed.
const passDurationWindow = 5

// WatchPace is how long a watch's passes take against how often it is supposed
// to run.
type WatchPace struct {
	MedianSec int64
	// Enough is false until there are passDurationWindow readings. Below that
	// the median is one slow start being mistaken for a pattern.
	Enough bool
}

// PaceOf is the median duration of a watch's recent successful passes.
//
// Median and not mean, the same choice cardYield makes: one pass that spent
// four minutes waiting out a refusal is the ordinary shape of this data, and a
// mean would let it decide the verdict for the other four.
//
// Only 'ok' passes. A pass that halted returned in milliseconds and says
// nothing about how long reading actually takes.
func (s *Store) PaceOf(ctx context.Context, watchID int64) (WatchPace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT elapsed_ms FROM passes
		WHERE watch_id = ? AND class = 'ok'
		ORDER BY id DESC LIMIT ?`, watchID, passDurationWindow)
	if err != nil {
		return WatchPace{}, fmt.Errorf("store: pace: %w", err)
	}
	defer rows.Close()

	var took []int64
	for rows.Next() {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return WatchPace{}, fmt.Errorf("store: pace: %w", err)
		}
		took = append(took, ms)
	}
	if err := rows.Err(); err != nil {
		return WatchPace{}, fmt.Errorf("store: pace: %w", err)
	}
	if len(took) < passDurationWindow {
		return WatchPace{}, nil
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	return WatchPace{
		MedianSec: took[len(took)/2] / 1000,
		Enough:    true,
	}, nil
}
