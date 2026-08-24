// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify drains the outbox.
//
// Lifted from WB_GO's internal/notify, with its two load-bearing decisions
// intact and named where they live: due_at is an absolute moment, and
// rule_event_id is nullable and cleared rather than cascaded.
package notify

import "time"

// backoffs are what a failed send waits before the next attempt.
//
// They stop growing at the last entry rather than doubling for ever: an hour is
// long enough that a retry costs nothing, and a queue whose head waits eight
// days is a queue that has effectively lost the message without saying so.
var backoffs = []time.Duration{
	30 * time.Second, 2 * time.Minute, 10 * time.Minute,
	30 * time.Minute, time.Hour,
}

// maxAttempts is when a row is given up on. A row that retries for ever is a
// queue that never drains, and the panel would show a growing number nobody can
// act on.
const maxAttempts = 12

func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(backoffs) {
		attempts = len(backoffs)
	}
	return backoffs[attempts-1]
}
