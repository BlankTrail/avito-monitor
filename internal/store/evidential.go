// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/lifecycle"
)

// prevPass is what the previous pass of this watch looked like, as far as the
// two verdicts care.
type prevPass struct {
	session    string
	evidenceAt int64
	lastTotal  *int64
	exists     bool
}

// readPrevPass reads the predecessor inside the transaction, before this pass's
// own row is inserted.
//
// Two different questions, two different rows. The session comes from the
// immediately previous pass whatever its class — Comparable asks whether this
// pass and the one before it saw the same market, and a blocked pass in between
// still changed the identity if the ladder renewed. The evidence timestamp and
// the count baseline come from the last pass we BELIEVED, because those are
// what Admissible measures a gap and a collapse against.
func readPrevPass(ctx context.Context, tx *sql.Tx, watchID int64) (prevPass, error) {
	var p prevPass

	err := tx.QueryRowContext(ctx, `
		SELECT session FROM passes WHERE watch_id = ?
		ORDER BY id DESC LIMIT 1`, watchID).Scan(&p.session)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return p, nil // no predecessor at all: this pass is a seed
	case err != nil:
		return p, fmt.Errorf("store: previous pass: %w", err)
	}
	p.exists = true

	err = tx.QueryRowContext(ctx, `
		SELECT started_at, total_count FROM passes
		WHERE watch_id = ? AND evidential = 1
		ORDER BY id DESC LIMIT 1`, watchID).Scan(&p.evidenceAt, &p.lastTotal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("store: previous evidential pass: %w", err)
	}
	return p, nil
}

// passFacts assembles what lifecycle needs from this pass and the one before.
//
// is_seed is derived rather than trusted. A caller can be wrong about it, and
// the store can see the truth: a watch with no previous pass is being seeded,
// whatever the caller believes.
func passFacts(p Pass, w Watch, prev prevPass) lifecycle.PassFacts {
	return lifecycle.PassFacts{
		Class:          p.Class,
		Surface:        string(p.Surface),
		Path:           p.Path,
		Degraded:       p.Degraded,
		IsSeed:         !prev.exists,
		TotalCount:     p.TotalCount,
		LastTotal:      prev.lastTotal,
		Session:        p.Session,
		PrevSession:    prev.session,
		StartedAt:      p.StartedAt,
		PrevEvidenceAt: prev.evidenceAt,
		IntervalSec:    w.IntervalSec,
	}
}

// markSeeded writes down that the watch now has a baseline.
//
// seeded_at was read in two places and written in none: the panel said «ещё не
// засеяно» about every watch for the life of the install, however many passes
// it had made, and the planner's «page two only while seeded_at IS NULL» — one
// of the three independent refusals of deep pagination — was stuck open for
// ever in the direction that spends requests.
//
// Set from the seeding pass itself, because that pass IS the baseline: after it
// there is something for «новое» to be a difference from. coalesce rather than
// an overwrite, so a watch whose history was pruned back to nothing does not
// silently restart its seeding — SavePass would then derive is_seed = true a
// second time, and the date this watch first had a baseline is not a thing that
// happens twice.
//
// Above the count guard on purpose. A seeding pass that carried no total_count
// is still a seeding pass, and the earlier placement would have left exactly
// those watches unseeded for ever — the rarest case and the hardest to notice.
func markSeeded(ctx context.Context, tx *sql.Tx, p Pass) error {
	if !p.IsSeed {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE watches SET seeded_at = coalesce(seeded_at, ?), updated_at = ?
		WHERE id = ?`, p.StartedAt, p.StartedAt, p.WatchID); err != nil {
		return fmt.Errorf("store: seeded: %w", err)
	}
	return nil
}

// updateBaseline moves what the collapse test compares against.
//
// Only an evidential pass may move it. Letting a blocked one write the baseline
// would set it to whatever the block returned — usually nothing — and the next
// real pass would then look like a recovery rather than a reading, which is the
// collapse guard defeating itself.
func updateBaseline(ctx context.Context, tx *sql.Tx, p Pass, evidential bool) error {
	if p.TotalCount == nil {
		// No number at all. The guard does not fire and the baseline does not
		// move: a missing number must never become arithmetic on zero, which
		// is the same rule the collapse test keeps.
		return nil
	}

	// too_broad is set from any pass that carried a count, evidential or not,
	// and that is deliberately not the baseline's rule. The count is a number
	// the SITE stated; the baseline is a comparison WE make, and only a
	// reading we would argue from may move it. A seeding pass is not evidence
	// about absence and is perfectly good evidence about how many results a
	// query has.
	//
	// Set and never cleared here, and that asymmetry is the design: saying so
	// once is the point, and a person narrows the query with a price band, so
	// the flag goes with the edit.
	if _, err := tx.ExecContext(ctx, `
		UPDATE watches SET too_broad = max(too_broad, ?), updated_at = ?
		WHERE id = ?`,
		*p.TotalCount > tooBroadAbove, p.StartedAt, p.WatchID); err != nil {
		return fmt.Errorf("store: too broad: %w", err)
	}

	if !evidential {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE watches SET last_total_count = ?, updated_at = ? WHERE id = ?`,
		*p.TotalCount, p.StartedAt, p.WatchID); err != nil {
		return fmt.Errorf("store: baseline: %w", err)
	}
	return nil
}

// tooBroadAbove is where a watch stops being a watch and starts being a market.
//
// Four and a half thousand, against Avito's own five-thousand cap on what a
// result set will page through. Above it, page one is a window onto something
// this product cannot see the shape of — and nothing slices automatically,
// because a monitor that multiplies its own request budget without being asked
// is a monitor that earns a 403.
const tooBroadAbove = 4500
