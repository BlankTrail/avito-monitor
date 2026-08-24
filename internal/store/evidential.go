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

// updateBaseline moves what the collapse test compares against.
//
// Only an evidential pass may move it. Letting a blocked one write the baseline
// would set it to whatever the block returned — usually nothing — and the next
// real pass would then look like a recovery rather than a reading, which is the
// collapse guard defeating itself.
func updateBaseline(ctx context.Context, tx *sql.Tx, p Pass, evidential bool) error {
	if !evidential || p.TotalCount == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE watches SET last_total_count = ?, updated_at = ? WHERE id = ?`,
		*p.TotalCount, p.StartedAt, p.WatchID); err != nil {
		return fmt.Errorf("store: baseline: %w", err)
	}
	return nil
}
