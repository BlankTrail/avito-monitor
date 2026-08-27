// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/lifecycle"
)

// Pass is one fetch of one page of one watch, as the collector saw it.
type Pass struct {
	WatchID    int64
	StartedAt  int64
	FinishedAt int64
	Page       int
	Surface    avito.Surface
	Class      string
	HTTPStatus int
	Path       string
	Mode       string
	Session    string
	Egress     string
	ElapsedMS  int64
	Bytes      int64
	TotalCount *int64
	// Degraded is the ExtractionReport carrying a warning. It is an INPUT to
	// the verdict.
	Degraded bool

	Error string

	// IsSeed, Evidential and DegradedWhy are OUTPUTS. SavePass computes them
	// from lifecycle and overwrites whatever a caller put here.
	//
	// They are on this struct rather than in a separate one so that insertPass
	// takes a single value, and the doc comment is the guard: increment 3 let
	// a caller pass a floor in by hand, and the column is what every later
	// query about absence reads. A soft block must not become evidence because
	// somebody filled in a field.
	IsSeed      bool
	Evidential  bool
	DegradedWhy string
}

// SaveResult is what the caller needs back: the id to hang changes off, and the
// two counts the panel reports.
type SaveResult struct {
	PassID    int64
	Arrivals  int
	Snapshots int

	// The verdicts this pass was written under, returned so a caller can say
	// why nothing was reported without re-deriving it.
	Facts      lifecycle.PassFacts
	Admissible lifecycle.Verdict
	Comparable lifecycle.Verdict

	// Previous is what the watch believed was present before this pass. The
	// window producer needs it and it is gone by the time the transaction ends.
	Previous map[int64]bool
}

// SavePass writes one pass and everything it implies, in one transaction.
//
// One BEGIN covers the pass row, its pass_items, the listings it saw, a
// listing_snapshots row for each whose fingerprint moved or whose anchor is
// due, and the watch_members set. A partial pass is worse than no pass:
// increment 4 differences consecutive passes, and a pass whose pass_items are
// missing reads as every listing in the watch having vanished at once.
func (s *Store) SavePass(ctx context.Context, p Pass, listings []avito.Listing) (SaveResult, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, fmt.Errorf("store: save pass: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx's Rollback is a no-op

	w, err := watchInTx(ctx, tx, p.WatchID)
	if err != nil {
		return SaveResult{}, err
	}
	res, err := savePassTx(ctx, tx, p, listings, s.Retention(), w, s)
	if err != nil {
		return SaveResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, fmt.Errorf("store: save pass: commit: %w", err)
	}
	return res, nil
}

func savePassTx(ctx context.Context, tx *sql.Tx, p Pass, listings []avito.Listing, r Retention, w Watch, store *Store) (SaveResult, error) {
	var out SaveResult

	// The two verdicts, computed here and never taken from the caller. This
	// column is what every later query about absence reads, so a caller must
	// not be able to mark a soft block as evidence by filling in a field.
	prev, err := readPrevPass(ctx, tx, p.WatchID)
	if err != nil {
		return out, err
	}
	facts := passFacts(p, w, prev)
	adm := lifecycle.Admissible(facts)
	out.Facts, out.Admissible, out.Comparable = facts, adm, lifecycle.Comparable(facts)
	p.IsSeed = facts.IsSeed
	p.Evidential = adm.Admissible
	p.DegradedWhy = adm.Why

	// What this watch believed was present before this pass. Read before the
	// membership update below overwrites it.
	previous, err := presentMembers(ctx, tx, p.WatchID)
	if err != nil {
		return out, err
	}
	for _, l := range listings {
		if !previous[l.ItemID] {
			out.Arrivals++
		}
	}

	passID, err := insertPass(ctx, tx, p, len(listings), out.Arrivals)
	if err != nil {
		return out, err
	}
	out.PassID = passID

	// Rank is the index, one-based. avito.Listing carries no position field on
	// purpose: rank belongs to the watch that saw the listing, not to the
	// listing, and two watches can see the same one at different ranks.
	for i, l := range listings {
		if err := upsertListing(ctx, tx, l, p); err != nil {
			return out, err
		}
		wrote, err := maybeSnapshot(ctx, tx, l, p, r)
		if err != nil {
			return out, err
		}
		if wrote {
			out.Snapshots++
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO pass_items (pass_id, position, item_id) VALUES (?, ?, ?)`,
			passID, i+1, l.ItemID); err != nil {
			return out, fmt.Errorf("store: save pass: pass_items: %w", err)
		}
	}

	if err := updateMembers(ctx, tx, p, listings); err != nil {
		return out, err
	}
	if err := updateBaseline(ctx, tx, p, p.Evidential); err != nil {
		return out, err
	}
	// The window producer runs last and inside this transaction, because a set
	// difference against the previous window must not be observed
	// half-written: a reader that saw the arrivals without the departures
	// would draw the wrong conclusion from a snapshot that looked consistent.
	if err := produceWindow(ctx, tx, p, passID, listings, previous,
		out.Arrivals, adm, out.Comparable, w, store); err != nil {
		return out, err
	}
	out.Previous = previous
	return out, nil
}

// watchInTx reads the watch inside the transaction. The interval and the
// surface are inputs to the verdicts, and reading them outside would let a
// concurrent edit change the rule mid-pass.
func watchInTx(ctx context.Context, tx *sql.Tx, id int64) (Watch, error) {
	w, err := scanWatch(tx.QueryRowContext(ctx,
		`SELECT `+watchColumns+` FROM watches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, ErrNoSuchWatch
	}
	if err != nil {
		return Watch{}, fmt.Errorf("store: watch %d: %w", id, err)
	}
	return w, nil
}

func presentMembers(ctx context.Context, tx *sql.Tx, watchID int64) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT item_id FROM watch_members WHERE watch_id = ? AND present = 1`, watchID)
	if err != nil {
		return nil, fmt.Errorf("store: save pass: previous members: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: save pass: previous members: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

func insertPass(ctx context.Context, tx *sql.Tx, p Pass, itemsSeen, arrivals int) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, http_status,
		   path, mode, session, egress, elapsed_ms, bytes, items_seen,
		   total_count, arrivals, is_seed, evidential, degraded_why, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.WatchID, p.StartedAt, p.FinishedAt, p.Page, string(p.Surface), p.Class,
		p.HTTPStatus, p.Path, p.Mode, p.Session, p.Egress, p.ElapsedMS, p.Bytes,
		itemsSeen, p.TotalCount, arrivals, p.IsSeed, p.Evidential,
		p.DegradedWhy, p.Error)
	if err != nil {
		return 0, fmt.Errorf("store: save pass: passes: %w", err)
	}
	return res.LastInsertId()
}

// upsertListing writes the stable half.
//
// The ON CONFLICT clause names its columns rather than being a blanket update,
// and the omissions are the point. first_seen_at is written once: it is the
// anchor of the whole lifecycle, and refreshing it would make every listing
// look new on every pass. repost_key is written once for the same kind of
// reason — a title edited after a link was made must not move the key that link
// was made against. A blanket update is how both rules get lost in a later
// edit.
func upsertListing(ctx context.Context, tx *sql.Tx, l avito.Listing, p Pass) error {
	// Before the listing, because listings.seller_id references sellers(id).
	sellerID, err := upsertSeller(ctx, tx, l, p.StartedAt)
	if err != nil {
		return err
	}
	var seller any
	if sellerID != 0 {
		seller = sellerID
	}

	// No price here: prices move, and they live in listing_snapshots. What is
	// here is the stable half plus the three derived values the repost ladder
	// reads. Until increment 7A this wrote nine of these columns; the other
	// twenty-one were declared in migration 0001 and filled by nobody.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO listings
		  (item_id, thread_id, url_path, title, title_norm,
		   category_id, category_root_id, micro_category_id,
		   location_id, location_name, geo_address, lat, lng, coords_precise,
		   seller_id, sort_ts_ms, allow_ts_ms,
		   repost_key, description_sha, image_key,
		   first_seen_at, last_seen_at, state, state_at,
		   discovered_seed, first_watch_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, 'active', ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET
		    url_path     = excluded.url_path,
		    title        = excluded.title,
		    title_norm   = excluded.title_norm,
		    last_seen_at = excluded.last_seen_at,
		    -- Everything below fills a hole and never overwrites a fact. A
		    -- later pass that read less than an earlier one must not erase
		    -- what the earlier one read: the mobile surface carries none of
		    -- these, and a listing in two watches is read from both. A blanket
		    -- update would let the cheaper page wipe the richer one's work
		    -- every other pass.
		    location_name = CASE WHEN excluded.location_name = ''
		                         THEN listings.location_name
		                         ELSE excluded.location_name END,
		    geo_address   = CASE WHEN excluded.geo_address = ''
		                         THEN listings.geo_address
		                         ELSE excluded.geo_address END,
		    category_id       = max(listings.category_id, excluded.category_id),
		    category_root_id  = max(listings.category_root_id, excluded.category_root_id),
		    micro_category_id = max(listings.micro_category_id, excluded.micro_category_id),
		    location_id       = max(listings.location_id, excluded.location_id),
		    lat            = coalesce(listings.lat, excluded.lat),
		    lng            = coalesce(listings.lng, excluded.lng),
		    coords_precise = max(listings.coords_precise, excluded.coords_precise),
		    sort_ts_ms     = coalesce(listings.sort_ts_ms, excluded.sort_ts_ms),
		    allow_ts_ms    = coalesce(listings.allow_ts_ms, excluded.allow_ts_ms),
		    seller_id      = coalesce(listings.seller_id, excluded.seller_id),
		    description_sha = CASE WHEN excluded.description_sha = ''
		                           THEN listings.description_sha
		                           ELSE excluded.description_sha END,
		    image_key       = CASE WHEN excluded.image_key = ''
		                           THEN listings.image_key
		                           ELSE excluded.image_key END,
		    -- repost_key is the one exception to «fill a hole», and it is the
		    -- schema's own rule: written once and never updated, so a title
		    -- edited after a link was made cannot move the key that link was
		    -- made against. It fills an empty key and never replaces one.
		    --
		    -- title and title_norm above DO update, and that is not a
		    -- contradiction: they are what a person reads and searches. Only
		    -- the key is frozen, because only the key is what a link points at.
		    repost_key = CASE WHEN listings.repost_key = ''
		                      THEN excluded.repost_key
		                      ELSE listings.repost_key END`,
		// thread_id is the listing's own id until the repost linker merges two
		// listings into one thread. Seeding it to item_id rather than to zero
		// means every listing is already in a thread of one, and linking is
		// then a repoint rather than a backfill.
		l.ItemID, l.ItemID, l.URLPath, l.Title, avito.FoldTitle(l.Title),
		l.CategoryID, l.CategoryRootID, l.MicroCategoryID,
		l.LocationID, l.LocationName, l.GeoAddress, l.Lat, l.Lng, l.CoordsPrecise,
		seller, l.SortTimeMS, l.AllowTimeMS,
		avito.RepostKey(l), avito.DescriptionSHA(l.Description), l.ImageKey,
		// state and state_at are left at 'active' as of this sighting and are
		// not touched on conflict. The lifecycle owns them from increment 4
		// onward — a listing seen in a pass is active by definition, but one
		// since marked vanished must not be quietly revived by an upsert that
		// happened to run. Its return is a change, and changes are computed
		// rather than assumed.
		p.StartedAt, p.StartedAt, p.StartedAt, p.IsSeed, p.WatchID)
	if err != nil {
		return fmt.Errorf("store: save pass: listings: %w", err)
	}
	return maybeSellerSnapshot(ctx, tx, sellerID, l, p.StartedAt)
}

// maybeSnapshot writes a row only when the fingerprint moved or an anchor is
// due, and reports whether it wrote one.
func maybeSnapshot(ctx context.Context, tx *sql.Tx, l avito.Listing, p Pass, r Retention) (bool, error) {
	fp := Fingerprint(l)

	var lastFP string
	var lastTS int64
	err := tx.QueryRowContext(ctx, `
		SELECT fingerprint, ts FROM listing_snapshots
		WHERE item_id = ? AND surface = ?
		ORDER BY ts DESC, id DESC LIMIT 1`,
		l.ItemID, string(p.Surface)).Scan(&lastFP, &lastTS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No row has ever been written for this listing on this surface, and
		// the first reading of anything is a change from nothing.
	case err != nil:
		return false, fmt.Errorf("store: save pass: last snapshot: %w", err)
	}

	changed := fp != lastFP
	anchor := !changed && AnchorDue(lastTS, p.StartedAt, r.AnchorEvery)
	if !changed && !anchor {
		return false, nil
	}

	// A number only when there is one. price_kind carries the rest: the
	// seller's «Цена договорная» and our own failure to read are different
	// statements, and both are NULL here.
	var minor *int64
	if l.PriceKind == avito.PriceExact {
		v := l.Price.Minor
		minor = &v
	}
	currency := l.Price.Currency
	if currency == "" {
		currency = "RUB"
	}
	path := p.Path
	if path == "" {
		// The column's CHECK admits only 'state' and 'dom'. A pass that
		// recorded no path is one whose extraction never ran, and it has no
		// listings to snapshot — but a caller could still hand us one, and a
		// row claiming a path it did not take is worse than a refused insert.
		path = "dom"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, anchor, fingerprint, source, path,
		   price_kind, price_minor, currency, price_label, title)
		VALUES (?, ?, ?, ?, ?, 'search', ?, ?, ?, ?, ?, ?)`,
		l.ItemID, string(p.Surface), p.StartedAt, anchor, fp, path,
		string(l.PriceKind), minor, currency, l.PriceLabel, l.Title); err != nil {
		return false, fmt.Errorf("store: save pass: listing_snapshots: %w", err)
	}
	return true, nil
}

// updateMembers moves the watch's membership set forward.
//
// Present listings reset their streak and record the rank they were seen at;
// absent ones advance theirs. last_position and window_size together are the
// displacement account: in a date-sorted window of k slots, an item at rank r
// is pushed out by k − r arrivals, so an absence explained by arrivals is not
// evidence of anything. Increment 4 does that arithmetic; this writes what it
// needs to do it with.
//
// Rows are never deleted here. A member row is the evidence behind every
// disappearance, and deleting it is how "why was I told this was gone" becomes
// unanswerable a month later.
func updateMembers(ctx context.Context, tx *sql.Tx, p Pass, listings []avito.Listing) error {
	seen := make([]int64, 0, len(listings))
	for i, l := range listings {
		seen = append(seen, l.ItemID)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO watch_members
			  (watch_id, item_id, first_seen_at, last_seen_at, last_position,
			   window_size, missing_streak, missing_since, present)
			VALUES (?, ?, ?, ?, ?, ?, 0, NULL, 1)
			ON CONFLICT(watch_id, item_id) DO UPDATE SET
			    last_seen_at   = excluded.last_seen_at,
			    last_position  = excluded.last_position,
			    window_size    = excluded.window_size,
			    missing_streak = 0,
			    missing_since  = NULL,
			    present        = 1`,
			p.WatchID, l.ItemID, p.StartedAt, p.StartedAt, i+1, len(listings)); err != nil {
			return fmt.Errorf("store: save pass: watch_members: %w", err)
		}
	}

	// Everything else in this watch is one pass more absent — but only if this
	// pass was admissible as evidence. A soft block or a mangled body is our
	// failure to read, not the market's decision to remove, and letting it
	// count is exactly how a monitor announces removals that never happened.
	if !p.Evidential {
		return nil
	}
	// missing_since is set on the first pass that missed the listing and left
	// alone afterwards, because the absence gate needs an elapsed span as well
	// as a count: two confirmations twenty seconds apart are one page of churn,
	// and two twenty minutes apart are evidence.
	// Every absent member, not only the ones that were present last time.
	//
	// The first draft filtered on present = 1, which froze the streak at one:
	// a listing missing from its second pass was already present = 0 and so
	// was never updated again, and absence_confirmations of two could never be
	// reached — no disappearance would ever have been confirmed. Found by
	// increment 4's window tests, which are the first thing to read the counter
	// rather than only write it.
	//
	// missing_since is coalesced rather than overwritten, so it keeps the
	// moment the listing was FIRST missed. That is what the span gate measures,
	// and refreshing it every pass would make the span permanently zero.
	q := `UPDATE watch_members
	      SET missing_streak = missing_streak + 1,
	          missing_since  = coalesce(missing_since, ?),
	          present        = 0
	      WHERE watch_id = ?`
	args := []any{p.StartedAt, p.WatchID}
	if len(seen) > 0 {
		q += ` AND item_id NOT IN (` + placeholders(len(seen)) + `)`
		for _, id := range seen {
			args = append(args, id)
		}
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: save pass: absence: %w", err)
	}
	return nil
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
