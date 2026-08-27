// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// The confirmation sweeper's half of the store.
//
// A windowed disappearance is a conclusion drawn from something not being on
// page one, and it is recorded at confidence 0.3 for exactly that reason. The
// item page is the only source that can say more, and this is what reads it —
// «предположительно пропало» becoming «снято с публикации» is the whole point,
// and without it every removal alert this product ever sends stays a guess.

// Confirmable is one listing worth one item-page request.
type Confirmable struct {
	ItemID  int64
	URLPath string
}

// VanishedNeedingConfirmation is the sweeper's queue: vanished listings, least
// recently attempted first, never-attempted before ever-attempted.
//
// Oldest attempt and not oldest listing. Both orderings put a fresh
// disappearance first — which is right, because the site still has its page —
// but only this one stops an unreadable listing from occupying the head of the
// queue for ever and eating the whole hourly budget.
func (s *Store) VanishedNeedingConfirmation(ctx context.Context, limit int) ([]Confirmable, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_id, url_path
		FROM listings
		WHERE state = 'vanished' AND url_path <> ''
		ORDER BY coalesce(confirmed_at, 0), state_at
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: confirm queue: %w", err)
	}
	defer rows.Close()

	var out []Confirmable
	for rows.Next() {
		var c Confirmable
		if err := rows.Scan(&c.ItemID, &c.URLPath); err != nil {
			return nil, fmt.Errorf("store: confirm queue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkConfirmationAttempted records that a request was spent on this listing,
// whatever came back.
//
// Called even when the page could not be read, which is the point: the request
// is gone either way, and a queue that only remembers successes re-spends it on
// the same listing next hour.
func (s *Store) MarkConfirmationAttempted(ctx context.Context, itemID, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE listings SET confirmed_at = ? WHERE item_id = ?`, at, itemID); err != nil {
		return fmt.Errorf("store: confirm attempt %d: %w", itemID, err)
	}
	return nil
}

// ApplyConfirmation writes what an item page said about a listing we believed
// had vanished.
//
// Three outcomes and they are not symmetric.
//
// The page states the posting ended: state 'closed', source 'item-page',
// confidence 1.0, and the site's own words kept verbatim and uninterpreted in
// state_reason. Only an item page may write 'closed' — a window may conclude
// 'vanished' and never this — and the change it emits carries confidence 1.0,
// so the second message says the guess became a fact.
//
// The page shows the listing alive: the windowed conclusion was wrong. State
// goes back to 'active' and a listing-returned change is written, again at
// confidence 1.0, because an item page saying «here it is» is a fact and not an
// inference. Any repost link built on this listing's death is retracted, for
// the same reason a reappearance in a pass retracts one: it never died.
//
// Neither: nothing is written except the attempt, which the caller records.
func (s *Store) ApplyConfirmation(ctx context.Context, itemID int64,
	page avito.Listing, ok bool, at int64) error {

	if !ok {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: confirm %d: %w", itemID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// The guard is the same one enrichment uses: only a listing we still
	// believe vanished may be confirmed. Between the queue read and this write
	// a pass may have seen it again, and overwriting that would replace a
	// stronger reading with a weaker one.
	var state string
	if err := tx.QueryRowContext(ctx,
		`SELECT state FROM listings WHERE item_id = ?`, itemID).Scan(&state); err != nil {
		return fmt.Errorf("store: confirm %d: %w", itemID, err)
	}
	if state != "vanished" {
		return tx.Commit()
	}

	var watchID sql.NullInt64
	_ = tx.QueryRowContext(ctx,
		`SELECT first_watch_id FROM listings WHERE item_id = ?`, itemID).Scan(&watchID)

	if page.ClosedReason != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE listings
			SET state = 'closed', state_at = ?, state_source = 'item-page',
			    state_reason = ?, state_confidence = 1.0
			WHERE item_id = ?`, at, page.ClosedReason, itemID); err != nil {
			return fmt.Errorf("store: confirm %d: %w", itemID, err)
		}
		return commitWith(tx, insertChange(ctx, tx, ChangeRow{
			Kind: track.KindListingClosed, ItemID: itemID,
			WatchID: watchID.Int64, TS: at,
			Was: page.ClosedReason, HadBefore: true,
			Confidence: 1.0, Producer: "transport",
		}))
	}

	// Alive after all. The windowed guess was wrong, and a correction that only
	// changed a database row would leave somebody holding a «предположительно
	// пропало» that is now false.
	if _, err := tx.ExecContext(ctx, `
		UPDATE listings
		SET state = 'active', state_at = ?, state_source = 'item-page',
		    state_reason = '', state_confidence = 1.0
		WHERE item_id = ?`, at, itemID); err != nil {
		return fmt.Errorf("store: confirm %d: %w", itemID, err)
	}
	if err := insertChange(ctx, tx, ChangeRow{
		Kind: track.KindListingReturned, ItemID: itemID,
		WatchID: watchID.Int64, TS: at, HasNow: true,
		Confidence: 1.0, Producer: "transport",
	}); err != nil {
		return err
	}
	// It never died, so nothing may rest on its death.
	return commitWith(tx, retractLinksFromTx(ctx, tx, watchID.Int64, itemID, at, nil))
}

func commitWith(tx *sql.Tx, err error) error {
	if err != nil {
		return err
	}
	return tx.Commit()
}
