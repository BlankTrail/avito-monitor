// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SoftBlockRun counts how many consecutive soft blocks end this watch's
// history, stopping at three because nothing above two is asked about.
//
// The question exists to fire the desktop diagnostic exactly once per episode.
// A run of two means «this just became ambiguous»; a run of three or more means
// the diagnostic has already been paid for and the answer has not changed, so
// spending two more requests would be buying the same sentence twice.
//
// ClassSoftBlock is the one mobile outcome that is genuinely ambiguous — the
// container is not there at all, and that is equally the shape of a block and
// the shape of Avito rewriting the page. ClassEmpty and ClassShape are not
// ambiguous and never come here: one is an answer corroborated by the site's
// own «Ничего не найдено», the other has already proved the container intact.
func (s *Store) SoftBlockRun(ctx context.Context, watchID int64) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT class FROM passes
		WHERE watch_id = ?
		ORDER BY started_at DESC, id DESC
		LIMIT 3`, watchID)
	if err != nil {
		return 0, fmt.Errorf("store: soft-block run for watch %d: %w", watchID, err)
	}
	defer rows.Close()

	run := 0
	for rows.Next() {
		var class string
		if err := rows.Scan(&class); err != nil {
			return 0, err
		}
		if class != "soft-block" {
			break
		}
		run++
	}
	return run, rows.Err()
}
