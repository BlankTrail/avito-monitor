// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/repost"
)

// The storage half of repost linking: who the candidates are, what was decided,
// and how a thread is rebuilt when a decision changes.
//
// internal/repost knows no schema and this file knows no ladder. The only thing
// crossing between them is repost.Listing, which is a strict subset of what
// listings holds.

// threadDepthCap bounds the recursive walk that rebuilds a thread.
//
// A repost chain is a seller re-publishing one item over and over; thirty-two
// incarnations is already an implausible year of that. The cap is not there for
// the plausible case — it is there so that a cycle written by a future bug ends
// a query instead of hanging a pass inside its transaction.
const threadDepthCap = 32

// RepostLinkRow is one decision about one pair, kept whatever it was.
//
// Including the refusals. «Почему мне не сказали про это объявление» has to be
// answerable for the pairs that were considered and rejected, not only for the
// ones that were linked, and a decision nobody wrote down cannot be explained
// afterwards.
type RepostLinkRow struct {
	SuccessorID   int64
	PredecessorID int64
	CreatedAt     int64
	DecidedAt     *int64
	Basis         string // "strong", "weak", "manual"
	Score         float64
	State         string // "considered", "proposed", "linked", "rejected", "retracted"
	DecidedBy     string // "auto", "user", "reappeared"
	Candidates    int
	GapSec        int64
	Evidence      []repost.Term
}

const repostLinkColumns = `successor_id, predecessor_id, created_at, decided_at,
	basis, score, state, decided_by, candidates, gap_sec, evidence`

// repostListingColumns is what internal/repost needs and no more.
const repostListingColumns = `l.item_id, coalesce(s.seller_key, ''),
	coalesce(s.key_quality, ''), l.repost_key, l.title_norm,
	l.description_sha, l.image_key,
	coalesce((SELECT sn.price_minor FROM listing_snapshots sn
	          WHERE sn.item_id = l.item_id AND sn.price_minor IS NOT NULL
	          ORDER BY sn.ts DESC LIMIT 1), 0),
	l.lat, l.lng, l.coords_precise, l.category_root_id,
	l.state, l.first_seen_at, l.last_seen_at`

func scanRepostListing(sc scanner) (repost.Listing, error) {
	var l repost.Listing
	var lat, lng sql.NullFloat64
	if err := sc.Scan(&l.ItemID, &l.SellerKey, &l.SellerQuality, &l.RepostKey,
		&l.Title, &l.DescriptionSHA, &l.ImageKey, &l.PriceMinor,
		&lat, &lng, &l.CoordsPrecise, &l.CategoryRootID,
		&l.State, &l.FirstSeenAt, &l.LastSeenAt); err != nil {
		return repost.Listing{}, err
	}
	if lat.Valid {
		v := lat.Float64
		l.Lat = &v
	}
	if lng.Valid {
		v := lng.Float64
		l.Lng = &v
	}
	return l, nil
}

// RepostListing reads one listing in the shape the linker wants.
func (s *Store) RepostListing(ctx context.Context, tx *sql.Tx, itemID int64) (repost.Listing, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT `+repostListingColumns+`
		FROM listings l LEFT JOIN sellers s ON s.id = l.seller_id
		WHERE l.item_id = ?`, itemID)
	l, err := scanRepostListing(row)
	if err != nil {
		return repost.Listing{}, fmt.Errorf("store: repost listing %d: %w", itemID, err)
	}
	return l, nil
}

// RepostCandidates is every listing that could be what this one succeeded.
//
// One index seek on idx_listings_repost_key and nothing cleverer: the key is
// the candidate bucket and the gates are what narrow it. An empty key finds
// nothing at all, which is the whole mobile-surface population and is the
// correct answer for it — a key over a bare title puts fifty listings in seven
// buckets, measured.
//
// The window is applied here as well as in the gates. Not redundancy: without
// it a key shared by a seller's whole two-year history would be read out of the
// database on every arrival and then thrown away one row at a time.
func (s *Store) RepostCandidates(ctx context.Context, tx *sql.Tx,
	l repost.Listing, windowSec int64) ([]repost.Listing, error) {

	if l.RepostKey == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT `+repostListingColumns+`
		FROM listings l LEFT JOIN sellers s ON s.id = l.seller_id
		WHERE l.repost_key = ? AND l.item_id <> ?
		  AND l.last_seen_at < ?
		  AND l.last_seen_at >= ?
		ORDER BY l.first_seen_at DESC
		LIMIT ?`,
		l.RepostKey, l.ItemID, l.FirstSeenAt, l.FirstSeenAt-windowSec,
		threadDepthCap)
	if err != nil {
		return nil, fmt.Errorf("store: repost candidates: %w", err)
	}
	defer rows.Close()

	var out []repost.Listing
	for rows.Next() {
		c, err := scanRepostListing(rows)
		if err != nil {
			return nil, fmt.Errorf("store: repost candidates: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IsPinned reports whether a person has said these two are different things.
//
// Symmetric, because the pair is. A pin written as (a, b) must refuse (b, a)
// too — otherwise the same correction would have to be made twice, and the
// second time would be after the wrong link came back.
func (s *Store) IsPinned(ctx context.Context, tx *sql.Tx, a, b int64) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM repost_pins
		WHERE (a = ? AND b = ?) OR (a = ? AND b = ?)`,
		a, b, b, a).Scan(&n); err != nil {
		return false, fmt.Errorf("store: pin: %w", err)
	}
	return n > 0, nil
}

// SaveRepostLink writes one decision.
func (s *Store) SaveRepostLink(ctx context.Context, tx *sql.Tx, r RepostLinkRow) error {
	evidence, err := json.Marshal(r.Evidence)
	if err != nil {
		return fmt.Errorf("store: repost link: evidence: %w", err)
	}
	if r.Basis == "" {
		// The schema's CHECK admits three and a considered pair has no basis
		// yet. «weak» is the honest placeholder: it is the lowest of the three,
		// and a row that never becomes a link never has it read.
		r.Basis = "weak"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO repost_links (`+repostLinkColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(successor_id) DO UPDATE SET
		    predecessor_id = excluded.predecessor_id,
		    decided_at     = excluded.decided_at,
		    basis          = excluded.basis,
		    score          = excluded.score,
		    state          = excluded.state,
		    decided_by     = excluded.decided_by,
		    candidates     = excluded.candidates,
		    gap_sec        = excluded.gap_sec,
		    evidence       = excluded.evidence`,
		r.SuccessorID, r.PredecessorID, r.CreatedAt, r.DecidedAt,
		r.Basis, r.Score, r.State, r.DecidedBy, r.Candidates, r.GapSec,
		string(evidence))
	if err != nil {
		return fmt.Errorf("store: repost link %d→%d: %w",
			r.PredecessorID, r.SuccessorID, err)
	}
	return nil
}

// RecomputeThread rebuilds thread_id for the whole chain one listing belongs to.
//
// This is what makes a wrong link repairable rather than permanent — the
// property Offer Ledger's four un-rebuildable denormalisations lacked.
// thread_id is the only written derived value in this schema, and it earns that
// by having this function.
//
// The walk is both directions from the listing, over linked edges only, with a
// depth cap. The oldest member's own id becomes the thread, so a chain has the
// same thread whichever end it is rebuilt from — and splitting one gives both
// halves an id that was already theirs rather than inventing two new ones.
func (s *Store) RecomputeThread(ctx context.Context, itemID int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: thread: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := recomputeThreadTx(ctx, tx, itemID); err != nil {
		return err
	}
	return tx.Commit()
}

func recomputeThreadTx(ctx context.Context, tx *sql.Tx, itemID int64) error {
	// UNION and not UNION ALL: that is what terminates a cycle. A row already
	// in the working set is not added again, so a → b → a runs out of new rows
	// rather than out of patience. The depth column is the second guard, for a
	// chain that is merely absurdly long.
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE chain(item_id, depth) AS (
		    SELECT ?, 0
		  UNION
		    SELECT rl.predecessor_id, c.depth + 1
		    FROM chain c JOIN repost_links rl ON rl.successor_id = c.item_id
		    WHERE rl.state = 'linked' AND c.depth < ?
		  UNION
		    SELECT rl.successor_id, c.depth + 1
		    FROM chain c JOIN repost_links rl ON rl.predecessor_id = c.item_id
		    WHERE rl.state = 'linked' AND c.depth < ?
		)
		SELECT c.item_id, l.first_seen_at
		FROM chain c JOIN listings l ON l.item_id = c.item_id`,
		itemID, threadDepthCap, threadDepthCap)
	if err != nil {
		return fmt.Errorf("store: thread %d: %w", itemID, err)
	}
	type member struct {
		id        int64
		firstSeen int64
	}
	var chain []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.firstSeen); err != nil {
			rows.Close()
			return fmt.Errorf("store: thread %d: %w", itemID, err)
		}
		chain = append(chain, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: thread %d: %w", itemID, err)
	}
	if len(chain) == 0 {
		return nil
	}

	// The oldest, and its own id. Ties broken by the smaller id so the answer
	// does not depend on row order — two listings first seen in the same pass
	// is the ordinary case for a seeding run.
	thread := chain[0]
	for _, m := range chain[1:] {
		if m.firstSeen < thread.firstSeen ||
			(m.firstSeen == thread.firstSeen && m.id < thread.id) {
			thread = m
		}
	}
	for _, m := range chain {
		if _, err := tx.ExecContext(ctx,
			`UPDATE listings SET thread_id = ? WHERE item_id = ?`,
			thread.id, m.id); err != nil {
			return fmt.Errorf("store: thread %d: %w", itemID, err)
		}
	}
	return nil
}

// PinPair records that a person said these two are not the same thing, splits
// whatever chain the link had made, and marks the link rejected.
//
// One transaction, because a pin without the split is a correction that did not
// take and a split without the pin is one that comes back on the next pass.
func (s *Store) PinPair(ctx context.Context, a, b, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: pin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repost_pins (a, b, pinned_at) VALUES (?, ?, ?)
		ON CONFLICT (a, b) DO NOTHING`, a, b, at); err != nil {
		return fmt.Errorf("store: pin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE repost_links
		SET state = 'rejected', decided_by = 'user', decided_at = ?
		WHERE (successor_id = ? AND predecessor_id = ?)
		   OR (successor_id = ? AND predecessor_id = ?)`,
		at, a, b, b, a); err != nil {
		return fmt.Errorf("store: pin: %w", err)
	}
	// Both halves, because the edge is gone and each side now belongs to
	// whatever remains of its own chain.
	for _, id := range []int64{a, b} {
		if err := recomputeThreadTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RepostLinks is what the Перепубликации screen reads, newest first.
func (s *Store) RepostLinks(ctx context.Context, limit int) ([]RepostLinkRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+repostLinkColumns+` FROM repost_links
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: repost links: %w", err)
	}
	defer rows.Close()

	var out []RepostLinkRow
	for rows.Next() {
		var r RepostLinkRow
		var evidence string
		if err := rows.Scan(&r.SuccessorID, &r.PredecessorID, &r.CreatedAt,
			&r.DecidedAt, &r.Basis, &r.Score, &r.State, &r.DecidedBy,
			&r.Candidates, &r.GapSec, &evidence); err != nil {
			return nil, fmt.Errorf("store: repost links: %w", err)
		}
		// A row whose evidence will not parse is returned with none rather
		// than failing the screen: the decision is still worth showing, and
		// the alternative is a page that answers nothing because one row from
		// an older release has a shape this build does not know.
		_ = json.Unmarshal([]byte(evidence), &r.Evidence)
		out = append(out, r)
	}
	return out, rows.Err()
}
