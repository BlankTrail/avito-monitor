// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Enrichment is the second half of one decision.
//
// The mobile surface is the cheaper page — one request instead of two — and it
// carries no seller, no category, no location and no coordinates, measured at
// 0 of 30. Desktop is what a new watch is now offered, but a mobile watch is
// still a legitimate choice, and every listing already collected from one is
// still here. So the seller is fetched separately for the rows that lack it.
//
// The rule that keeps this honest lives in cmd/avitomon/enrich.go rather than
// in these functions: enrichment spends only headroom. A pass is the product
// working; enrichment is the product improving, and improving must never take a
// request that working needed.

// EnrichCandidate is one listing worth one request.
type EnrichCandidate struct {
	ItemID  int64
	URLPath string
}

// ListingsNeedingSeller is the queue, newest first.
//
// Newest first and not oldest. A listing collected an hour ago may still be in
// somebody's window and is still worth linking; one from three weeks ago is
// past the repost window anyway. A queue drained oldest-first would spend its
// whole budget on rows that can no longer produce a link.
//
// A row with no url_path is skipped rather than fetched: there is nothing to
// open, and offering it would park a permanently failing item at the head of
// the queue for ever.
func (s *Store) ListingsNeedingSeller(ctx context.Context, limit int) ([]EnrichCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_id, url_path
		FROM listings
		WHERE seller_id IS NULL
		  AND url_path <> ''
		  AND state IN ('active', 'left-window')
		ORDER BY first_seen_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: enrich queue: %w", err)
	}
	defer rows.Close()

	var out []EnrichCandidate
	for rows.Next() {
		var c EnrichCandidate
		if err := rows.Scan(&c.ItemID, &c.URLPath); err != nil {
			return nil, fmt.Errorf("store: enrich queue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApplyEnrichment fills what a later, richer reading of one listing found.
//
// The WHERE clause is the whole safety property. `seller_id IS NULL` means this
// takes effect at most once per listing, whatever calls it and however often —
// so repost_key is written exactly once here and can never be moved afterwards,
// the same guarantee a title edit has.
//
// Writing the key here rather than leaving it empty is a decision worth
// stating. The schema freezes the key so that an EDIT cannot move it, and a
// seller arriving is not an edit — it is the row becoming complete. A key
// frozen empty would mean the enrichment request bought nothing at all.
func (s *Store) ApplyEnrichment(ctx context.Context, itemID int64,
	l avito.Listing, at int64) error {

	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: enrich: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sellerID, err := upsertSeller(ctx, tx, l, at)
	if err != nil {
		return err
	}
	if sellerID == 0 {
		// The richer page identified no seller either. Nothing is written and
		// nothing is marked, so the row stays in the queue — which is right:
		// a later attempt may read a page that does carry one. The caller's
		// per-run limit is what stops this becoming a spin.
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE listings SET
		    seller_id         = ?,
		    micro_category_id = max(micro_category_id, ?),
		    category_id       = max(category_id, ?),
		    category_root_id  = max(category_root_id, ?),
		    location_id       = max(location_id, ?),
		    location_name     = CASE WHEN ? = '' THEN location_name ELSE ? END,
		    geo_address       = CASE WHEN ? = '' THEN geo_address ELSE ? END,
		    lat               = coalesce(lat, ?),
		    lng               = coalesce(lng, ?),
		    coords_precise    = max(coords_precise, ?),
		    description_sha   = CASE WHEN ? = '' THEN description_sha ELSE ? END,
		    image_key         = CASE WHEN ? = '' THEN image_key ELSE ? END,
		    repost_key        = CASE WHEN repost_key = '' THEN ? ELSE repost_key END
		WHERE item_id = ? AND seller_id IS NULL`,
		sellerID,
		l.MicroCategoryID, l.CategoryID, l.CategoryRootID, l.LocationID,
		l.LocationName, l.LocationName,
		l.GeoAddress, l.GeoAddress,
		l.Lat, l.Lng, l.CoordsPrecise,
		avito.DescriptionSHA(l.Description), avito.DescriptionSHA(l.Description),
		l.ImageKey, l.ImageKey,
		avito.RepostKey(l),
		itemID); err != nil {
		return fmt.Errorf("store: enrich %d: %w", itemID, err)
	}
	if err := maybeSellerSnapshot(ctx, tx, sellerID, l, at); err != nil {
		return err
	}
	return tx.Commit()
}

// EnrichmentQueue is the three numbers the panel shows: how many listings are
// waiting for a seller, how many have one, and how many there are.
//
// Three and not one, because «412 waiting» means nothing without «of 12 000» —
// and because the ratio is what says whether enrichment is keeping up or
// falling behind.
func (s *Store) EnrichmentQueue(ctx context.Context) (waiting, keyed, total int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
		  coalesce(sum(CASE WHEN seller_id IS NULL AND url_path <> ''
		                    THEN 1 ELSE 0 END), 0),
		  coalesce(sum(CASE WHEN seller_id IS NOT NULL THEN 1 ELSE 0 END), 0),
		  count(*)
		FROM listings`).Scan(&waiting, &keyed, &total)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("store: enrich queue: %w", err)
	}
	return waiting, keyed, total, nil
}
