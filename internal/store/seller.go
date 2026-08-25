// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/BlankTrail/avito-monitor/avito"
)

// This file gives sellers its first writer.
//
// The table has existed since migration 0001 and nothing ever wrote a row, so
// listings.seller_id was NULL everywhere — which is why the export's «Продавец»
// column came back empty on all sixty rows of a live run in increment 6C. That
// was visible from outside for a whole increment before anyone looked at the
// cause, and the cause was here.

// upsertSeller writes the seller this listing names and returns its surrogate
// id, or 0 when the listing names none.
//
// Zero rather than an error: thirty of thirty listings from the mobile surface
// identify no seller, so an unnamed seller is the ordinary case and not a
// failure. The caller writes NULL into listings.seller_id for it.
//
// key_quality is written on insert and never changed on conflict. A seller
// first recorded through one spelling keeps the quality it was recorded with:
// the row that exists is the row links were made against, and re-labelling it
// afterwards would change what those links are allowed to conclude.
func upsertSeller(ctx context.Context, tx *sql.Tx, l avito.Listing, at int64) (int64, error) {
	if l.SellerKey == "" {
		return 0, nil
	}
	quality := l.SellerKeyQuality
	if quality == "" {
		// The schema's CHECK admits three values, and this is the one meaning
		// «we are not sure how we got this». A heuristic key is never accepted
		// as repost evidence — exactly the right treatment for a key whose
		// provenance was lost somewhere upstream.
		quality = "heuristic"
	}
	kind := "unknown"
	if l.IsCompany != nil && *l.IsCompany {
		kind = "company"
	}

	// Every field but the key fills a hole rather than overwriting: the name
	// and the profile path come from the second spelling, which is on four rows
	// in ten, and a row read without it must not blank what a row read with it
	// had already recorded.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sellers
		  (seller_key, key_quality, kind, display_name, profile_path,
		   first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(seller_key) DO UPDATE SET
		    kind         = CASE WHEN excluded.kind = 'unknown'
		                        THEN sellers.kind ELSE excluded.kind END,
		    display_name = CASE WHEN excluded.display_name = ''
		                        THEN sellers.display_name ELSE excluded.display_name END,
		    profile_path = CASE WHEN excluded.profile_path = ''
		                        THEN sellers.profile_path ELSE excluded.profile_path END,
		    last_seen_at = max(sellers.last_seen_at, excluded.last_seen_at)`,
		l.SellerKey, quality, kind, l.SellerName, l.SellerProfilePath,
		at, at); err != nil {
		return 0, fmt.Errorf("store: seller: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM sellers WHERE seller_key = ?`, l.SellerKey).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: seller id: %w", err)
	}
	return id, nil
}

// SellerFingerprint is what has to move before a reputation reading is worth a
// row.
//
// The same rule the listing series uses and for the same reason: a row per pass
// would grow at the rate of collection and say nothing new. Rating and review
// summary only — the display name lives on the sellers row, where it is
// corrected in place rather than versioned.
func SellerFingerprint(l avito.Listing) string {
	h := sha256.New()
	if l.SellerRatingHundredths != nil {
		h.Write([]byte(strconv.FormatInt(*l.SellerRatingHundredths, 10)))
	}
	h.Write([]byte{0})
	h.Write([]byte(l.SellerReviewSummary))
	return hex.EncodeToString(h.Sum(nil))
}

// maybeSellerSnapshot writes a reputation row when the fingerprint moved.
//
// No anchor rule here, unlike the listing series. M1 emits no events from this
// table — the spec says so — so «unchanged since March» and «unread since
// March» are not yet different questions anybody asks, and an anchor would
// write rows against a distinction nothing reads. The column exists; the rule
// arrives with the first reader.
func maybeSellerSnapshot(ctx context.Context, tx *sql.Tx, sellerID int64,
	l avito.Listing, at int64) error {

	if sellerID == 0 || l.SellerRatingHundredths == nil {
		return nil
	}
	fp := SellerFingerprint(l)

	var last string
	err := tx.QueryRowContext(ctx, `
		SELECT fingerprint FROM seller_snapshots
		WHERE seller_id = ? ORDER BY ts DESC LIMIT 1`, sellerID).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: seller snapshot: %w", err)
	}
	if err == nil && last == fp {
		return nil
	}

	// DO NOTHING against the (seller_id, ts) unique index: two listings by one
	// seller in one pass carry the same reading at the same moment, and the
	// second must not fail the transaction — which would lose the whole pass
	// over a duplicate that says nothing new.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO seller_snapshots
		  (seller_id, ts, anchor, fingerprint, rating_hundredths, rating_summary)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT (seller_id, ts) DO NOTHING`,
		sellerID, at, fp, *l.SellerRatingHundredths, l.SellerReviewSummary); err != nil {
		return fmt.Errorf("store: seller snapshot: %w", err)
	}
	return nil
}
