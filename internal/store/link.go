// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/repost"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// linkArrival decides whether this arrival is a seller re-publishing something
// they had already posted, and writes down every decision it reached.
//
// Inside the pass transaction, on purpose. A link changes what the pass
// announces — «выложено заново» instead of «новое объявление» — so deciding it
// afterwards would mean the alert had already gone out.
//
// Returns the predecessor when a link was made, and zero otherwise. Zero is the
// ordinary answer: most arrivals are new listings, and the design is arranged
// so that a doubt produces one rather than a withheld alert.
func linkArrival(ctx context.Context, tx *sql.Tx, s *Store,
	itemID int64, w Watch, now int64) (int64, error) {

	window := int64(w.RepostWindowSec)
	if window <= 0 {
		// The schema's own default. A watch row written before this column was
		// read carries zero, and zero would mean «no listing is ever a
		// successor» — a silent disabling of the whole feature rather than a
		// setting anybody chose.
		window = defaultRepostWindowSec
	}

	arrival, err := s.RepostListing(ctx, tx, itemID)
	if err != nil {
		return 0, err
	}
	candidates, err := s.RepostCandidates(ctx, tx, arrival, window)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// Every candidate is judged and every judgement is written, including the
	// refusals. «Почему мне не сказали про это объявление» has to be
	// answerable for the pairs that were considered and rejected too, and a
	// decision nobody wrote down cannot be explained afterwards.
	//
	// The count passed to the ladder is how many SURVIVED the gates, not how
	// many the key returned: a bucket of eight where seven are refused for
	// co-existing leaves one real candidate, and refusing that one because the
	// bucket was crowded would throw away the answer the gates just produced.
	type judged struct {
		candidate repost.Listing
		gate      repost.GateResult
	}
	var survivors []judged
	var refused []judged
	for _, c := range candidates {
		pinned, err := s.IsPinned(ctx, tx, itemID, c.ItemID)
		if err != nil {
			return 0, err
		}
		g := repost.Gates(arrival, c, pinned, window)
		if g.Passed {
			survivors = append(survivors, judged{c, g})
		} else {
			refused = append(refused, judged{c, g})
		}
	}

	for _, r := range refused {
		if err := writeDecision(ctx, tx, s, arrival, r.candidate,
			repost.Judge(arrival, r.candidate, r.gate, len(survivors)),
			len(survivors), now); err != nil {
			return 0, err
		}
	}

	var linkedTo int64
	for _, sv := range survivors {
		v := repost.Judge(arrival, sv.candidate, sv.gate, len(survivors))
		if err := writeDecision(ctx, tx, s, arrival, sv.candidate, v,
			len(survivors), now); err != nil {
			return 0, err
		}
		if v.State == "linked" {
			linkedTo = sv.candidate.ItemID
		}
	}
	if linkedTo != 0 {
		if err := recomputeThreadTx(ctx, tx, itemID); err != nil {
			return 0, err
		}
	}
	return linkedTo, nil
}

// defaultRepostWindowSec is migration 0001's own default, repeated here for a
// watch row that predates anything reading the column.
const defaultRepostWindowSec = 30 * 24 * 60 * 60

func writeDecision(ctx context.Context, tx *sql.Tx, s *Store,
	arrival, candidate repost.Listing, v repost.Verdict,
	survivors int, now int64) error {

	// Only one row per successor: the schema keys repost_links on it. So the
	// row written last wins, and the loop above writes refusals first and
	// survivors second — a pair that reached a verdict is more worth keeping
	// than one that never got past a gate.
	var decided *int64
	if v.State == "linked" || v.State == "rejected" {
		at := now
		decided = &at
	}
	if err := s.SaveRepostLink(ctx, tx, RepostLinkRow{
		SuccessorID: arrival.ItemID, PredecessorID: candidate.ItemID,
		CreatedAt: now, DecidedAt: decided,
		Basis: string(v.Basis), Score: v.Score, State: v.State,
		DecidedBy: "auto", Candidates: survivors,
		GapSec:   arrival.FirstSeenAt - candidate.LastSeenAt,
		Evidence: v.Evidence,
	}); err != nil {
		return fmt.Errorf("store: link %d: %w", arrival.ItemID, err)
	}
	return nil
}

// RepostOf is the predecessor a listing was linked to, or zero.
//
// Read by the notifier so that rules.Event.RepostOf can be set, which is what
// makes suppress.Decide answer ReasonRepost. That field and that reason have
// existed since increment 5 with nothing to set them.
func (s *Store) RepostOf(ctx context.Context, itemID int64) (int64, error) {
	var predecessor int64
	err := s.db.QueryRowContext(ctx, `
		SELECT predecessor_id FROM repost_links
		WHERE successor_id = ? AND state = 'linked'`, itemID).Scan(&predecessor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: repost of %d: %w", itemID, err)
	}
	return predecessor, nil
}

// retractLinksFrom undoes every link built on a listing's death, because it is
// back and therefore never died.
//
// The spec calls this the automatic half of undo: the same three acts as
// pressing «это не перепубликация» — the link stops being one, the thread
// splits, and the person who was told «выложено заново» is told it was wrong —
// but without a pin. A pin records a person's judgement, and this is not one:
// the site simply showed us the listing again, and a future pair really might
// be a repost.
func retractLinksFrom(ctx context.Context, tx *sql.Tx, p Pass, passID, itemID int64) error {
	return retractLinksFromTx(ctx, tx, p.WatchID, itemID, p.StartedAt, &passID)
}

// retractLinksFromTx is the same act without a pass behind it.
//
// The confirmation sweeper reaches the same conclusion from an item page rather
// than from a window, and an item page is not a pass — so passID is a pointer
// and nil there. The evidence column stays honest either way: the pass version
// points at the pass it read, and the sweeper's points at nothing, because
// there is nothing in `passes` to point at.
func retractLinksFromTx(ctx context.Context, tx *sql.Tx,
	watchID, itemID, at int64, passID *int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT successor_id FROM repost_links
		WHERE predecessor_id = ? AND state = 'linked'`, itemID)
	if err != nil {
		return fmt.Errorf("store: retract %d: %w", itemID, err)
	}
	var successors []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("store: retract %d: %w", itemID, err)
		}
		successors = append(successors, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: retract %d: %w", itemID, err)
	}
	if len(successors) == 0 {
		return nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE repost_links
		SET state = 'retracted', decided_by = 'reappeared', decided_at = ?
		WHERE predecessor_id = ? AND state = 'linked'`,
		at, itemID); err != nil {
		return fmt.Errorf("store: retract %d: %w", itemID, err)
	}

	for _, successor := range successors {
		if err := recomputeThreadTx(ctx, tx, successor); err != nil {
			return err
		}
		// Said out loud. Somebody was told «выложено заново, вместо N», and a
		// correction that only changed a database row would leave them holding
		// a message that is now false.
		if err := insertChange(ctx, tx, ChangeRow{
			Kind: track.KindRepostRetracted, ItemID: successor,
			WatchID: watchID, PassID: passID, TS: at,
			Field: "predecessor", Was: fmt.Sprint(itemID),
			HadBefore: true, Confidence: 1.0, Producer: "window",
		}); err != nil {
			return err
		}
	}
	return recomputeThreadTx(ctx, tx, itemID)
}

// FirstWatchOf is the watch that first saw a listing.
//
// The panel's replay needs a watch to run rules for, and a listing may belong
// to several. The first is the honest pick: it is the one whose seeding pass
// created the row, and it is what listings.first_watch_id has recorded since
// increment 3 for exactly this kind of question.
func (s *Store) FirstWatchOf(ctx context.Context, itemID int64) (int64, error) {
	var watchID sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT first_watch_id FROM listings WHERE item_id = ?`, itemID).
		Scan(&watchID); err != nil {
		return 0, fmt.Errorf("store: first watch of %d: %w", itemID, err)
	}
	if !watchID.Valid || watchID.Int64 == 0 {
		// Fall back to any watch that has it now. A listing whose creating
		// watch was deleted still deserves its correction.
		if err := s.db.QueryRowContext(ctx,
			`SELECT watch_id FROM watch_members WHERE item_id = ? LIMIT 1`, itemID).
			Scan(&watchID); err != nil {
			return 0, fmt.Errorf("store: first watch of %d: %w", itemID, err)
		}
	}
	return watchID.Int64, nil
}

// RecordUserChange writes a change a person caused rather than a pass.
//
// Outside any pass transaction, because there is no pass: the person pressed a
// button. Producer is theirs to set and PassID stays nil, which is what keeps
// «this happened because somebody corrected us» distinguishable in the log from
// «this happened because the site changed».
func (s *Store) RecordUserChange(ctx context.Context, c ChangeRow) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: user change: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertChange(ctx, tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordWorkerPanic writes down that a supervised goroutine crashed.
//
// A changes row and not only a log line, because the log is a file nobody has
// open and the changes table is what the panel reads. track.KindWorkerPanicked
// has existed since increment 1 with nothing writing it; this is its writer.
//
// watch_id is zero: a background goroutine belongs to the installation and not
// to one watch, and attributing a crash to whichever watch happened to be next
// would be a lie in the one row that has to be trustworthy.
func (s *Store) RecordWorkerPanic(ctx context.Context, name, stack string, at int64) error {
	return s.RecordUserChange(ctx, ChangeRow{
		Kind: track.KindWorkerPanicked, TS: at,
		Field: name, Now: stack, HasNow: true,
		Confidence: 1.0, Producer: "supervise",
	})
}
