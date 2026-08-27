// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/lifecycle"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// vanishedConfidence is what a conclusion drawn from a window is worth.
//
// 1.0 is for a fact an item page stated. A page one is a window and not a
// census: a listing can leave it because it was removed, because a newer
// posting pushed it off the bottom, or because a promotion reshuffled the
// block, and the response says which for none of them. Displacement removes the
// second of those three by arithmetic; the remaining uncertainty is what this
// number carries into every alert.
const vanishedConfidence = 0.3

// produceWindow writes the membership differences for one pass.
//
// It runs inside the pass transaction, and that placement is the point: a set
// difference against the previous window must not be observed half-written by a
// concurrent reader, who would otherwise see the arrivals without the
// departures and draw the wrong conclusion from a consistent-looking snapshot.
//
// Three gates decide what it may say, and they are separate questions:
//
//   - admissible: is this pass a good reading at all
//   - comparable: are this pass and its predecessor readings of the SAME market
//   - displaced:  is this particular absence explained by arrivals
//
// Arrivals are written whenever the pass was read at all. Departures need all
// three.
func produceWindow(
	ctx context.Context, tx *sql.Tx,
	p Pass, passID int64, listings []avito.Listing,
	previous map[int64]bool, arrivals int,
	adm, cmp lifecycle.Verdict, w Watch, store *Store,
) error {
	// An arrival that cannot be compared with anything is marked backfill. A
	// seed's arrivals are real but are not news; and an incomparable pass's
	// arrivals are a different slice of the market rather than new listings in
	// the one being watched. Both would otherwise fire a rule thirty times.
	backfill := p.IsSeed || !cmp.Admissible

	for _, l := range listings {
		if previous[l.ItemID] {
			continue
		}
		// listing-appeared requires membership now, absence before, AND
		// listings.first_seen_at equal to this pass. Without the third clause a
		// recovered soft block announces thirty new listings that have been
		// there all week. It is the cheapest correctness guard in the design.
		var firstSeen int64
		if err := tx.QueryRowContext(ctx,
			`SELECT first_seen_at FROM listings WHERE item_id = ?`, l.ItemID).
			Scan(&firstSeen); err != nil {
			return fmt.Errorf("store: window: first seen: %w", err)
		}
		kind := track.KindListingReturned
		if firstSeen == p.StartedAt {
			kind = track.KindListingAppeared
		}

		// A listing seen for the first time may be a seller re-publishing
		// something they had already posted. Decided here, inside the pass
		// transaction, because the answer changes what this pass announces —
		// deciding it afterwards would mean the alert had already gone out.
		//
		// Gated on admissibility and not on the backfill flag above, and the
		// difference is the point. Backfill also covers an incomparable pass —
		// one whose identity changed — and lifecycle/compare.go says plainly
		// what that means: «its arrivals are real. What it cannot do is
		// confirm that anything LEFT.» A changed identity is a different slice
		// of the market, which is a fact about departures.
		//
		// What the linker must not run on is a whole page arriving at once: a
		// seed, or a re-seed after a gap in collection. Both are exactly what
		// makes a pass inadmissible, and running the linker over one would
		// turn a weekend the laptop was asleep into a page of reposts.
		var repostOf int64
		if kind == track.KindListingAppeared && !p.IsSeed && adm.Admissible {
			var err error
			if repostOf, err = linkArrival(ctx, tx, store, l.ItemID, w,
				p.StartedAt); err != nil {
				return err
			}
			if repostOf != 0 {
				kind = track.KindListingReposted
			}
		}

		row := ChangeRow{
			Kind: kind, ItemID: l.ItemID, WatchID: p.WatchID,
			PassID: &passID, TS: p.StartedAt,
			Confidence: 1.0, Producer: "window",
			HasNow: true, Backfill: backfill,
		}
		if repostOf != 0 {
			// The predecessor travels in Was, which is the column the notifier
			// already reads for «вместо какого объявления». Confidence stays
			// at 1.0: the link is a conclusion this pass reached and stands
			// behind, and the ladder's own doubt is expressed by not linking
			// rather than by linking weakly.
			row.Was = fmt.Sprint(repostOf)
			row.HadBefore = true
			row.Field = "predecessor"
		}
		if err := insertChange(ctx, tx, row); err != nil {
			return err
		}

		// A listing we had declared gone, seen again. It never died, so every
		// link built on its death is wrong and retracts itself.
		//
		// Only on an evidential pass, the same rule the absence gates keep: a
		// reappearance on a pass we could not read properly is not evidence of
		// anything, and retracting on one would undo a correct link because of
		// a soft block.
		if kind == track.KindListingReturned && p.Evidential {
			if err := retractLinksFrom(ctx, tx, p, passID, l.ItemID); err != nil {
				return err
			}
		}
	}

	if !cmp.Admissible {
		// The identity changed, so the members this watch accumulated under the
		// previous one cannot be argued about at all — the new identity never
		// saw them. Measured 2026-08-24: two identities share 23% of a page.
		//
		// Suppressing one pass is not enough, and that is the trap. From the
		// pass after this one the sessions match again, the streaks resume
		// climbing, and twenty minutes later three quarters of the watch is
		// declared vanished on the word of an identity that was never shown
		// them. So they are retired here instead: absent, explained, and
		// silent.
		//
		// Retired and not reset. Resetting the streak would let the new
		// identity start building a case against listings it has never seen,
		// which is the same false removal one poll later. Emitting nothing is
		// the correct output for "no information" — and if the listing turns up
		// under the new identity it returns as listing-returned, which is true.
		return retireAbsentMembers(ctx, tx, p.WatchID)
	}
	if !adm.Admissible {
		return nil
	}
	return produceDepartures(ctx, tx, p, passID, arrivals, w)
}

// retireAbsentMembers marks this watch's absent members as explained, without
// announcing anything.
//
// displaced is the existing column for "this absence has a reason that is not
// removal", and an identity change is exactly such a reason. No change row is
// written: we did not observe a departure, we lost the ability to observe.
func retireAbsentMembers(ctx context.Context, tx *sql.Tx, watchID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE watch_members
		SET displaced = 1, missing_streak = 0, missing_since = NULL
		WHERE watch_id = ? AND present = 0`, watchID)
	if err != nil {
		return fmt.Errorf("store: window: retire members: %w", err)
	}
	return nil
}

// produceDepartures walks the members this pass did not see.
func produceDepartures(
	ctx context.Context, tx *sql.Tx,
	p Pass, passID int64, arrivals int, w Watch,
) error {
	// Only members that are currently absent, and only those whose listing is
	// not already known to be gone. The state check is what stops a vanished
	// listing being announced again on every later pass: the gates keep
	// passing, and a producer without it would send the same alert every tick
	// until the person turned notifications off.
	// displaced = 0 as well as present = 0, because the displaced flag is what
	// makes left-window a one-time announcement. Without it the same member is
	// re-explained on every later pass and the change is written again each
	// time — five rows for one departure, which is five messages.
	rows, err := tx.QueryContext(ctx, `
		SELECT m.item_id, m.last_position, m.window_size, m.missing_streak,
		       coalesce(m.missing_since, 0)
		FROM watch_members m
		JOIN listings l ON l.item_id = m.item_id
		WHERE m.watch_id = ? AND m.present = 0 AND m.displaced = 0
		  AND l.state = 'active'`, p.WatchID)
	if err != nil {
		return fmt.Errorf("store: window: absent members: %w", err)
	}
	var absent []lifecycle.Member
	for rows.Next() {
		var m lifecycle.Member
		if err := rows.Scan(&m.ItemID, &m.LastPosition, &m.WindowSize,
			&m.MissingStreak, &m.MissingSince); err != nil {
			rows.Close()
			return fmt.Errorf("store: window: absent members: %w", err)
		}
		absent = append(absent, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: window: absent members: %w", err)
	}

	for _, m := range absent {
		verdict := lifecycle.Explain(lifecycle.AbsenceInput{
			Member: m, Arrivals: arrivals, Now: p.StartedAt,
			Confirmations: w.AbsenceConfirmations,
			MinSpanSec:    w.AbsenceMinSpanSec,
		})

		switch {
		case verdict.Displaced:
			if err := markDisplaced(ctx, tx, p.WatchID, m.ItemID); err != nil {
				return err
			}
			// It did leave the window, and that is a different sentence from
			// having been removed. Emitted once, on the pass that worked it
			// out — markDisplaced is what makes it once.
			if err := insertChange(ctx, tx, ChangeRow{
				Kind: track.KindListingLeftWindow, ItemID: m.ItemID,
				WatchID: p.WatchID, PassID: &passID, TS: p.StartedAt,
				Field: "position", Was: fmt.Sprint(m.LastPosition),
				Unit: track.UnitCount, HadBefore: true,
				Confidence: 1.0, Producer: "window",
			}); err != nil {
				return err
			}

		case verdict.Confirmed:
			// A window conclusion may write 'vanished' and never 'closed'.
			// state_source is the column that keeps those apart: only an item
			// page or a person may say a listing was closed.
			if _, err := tx.ExecContext(ctx, `
				UPDATE listings
				SET state = 'vanished', state_at = ?, state_source = 'search',
				    state_reason = ?, state_confidence = ?
				WHERE item_id = ?`,
				p.StartedAt, verdict.Why, vanishedConfidence, m.ItemID); err != nil {
				return fmt.Errorf("store: window: listing state: %w", err)
			}
			if err := insertChange(ctx, tx, ChangeRow{
				Kind: track.KindListingVanished, ItemID: m.ItemID,
				WatchID: p.WatchID, PassID: &passID, TS: p.StartedAt,
				HadBefore: true, HasNow: false,
				Confidence: vanishedConfidence, Producer: "window",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// markDisplaced records that this absence is explained, and stops the streak
// advancing on it.
//
// The streak is reset rather than frozen: an absence explained by arrivals is
// not evidence of anything, and leaving the count where it was would let three
// displacements and one real absence add up to a confirmation.
func markDisplaced(ctx context.Context, tx *sql.Tx, watchID, itemID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE watch_members
		SET displaced = 1, missing_streak = 0, missing_since = NULL
		WHERE watch_id = ? AND item_id = ?`, watchID, itemID)
	if err != nil {
		return fmt.Errorf("store: window: mark displaced: %w", err)
	}
	return nil
}
