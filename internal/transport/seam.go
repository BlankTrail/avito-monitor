// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"context"
	"net/http"
)

// Lease is one rested identity, held for the duration of one fetch.
//
// Both modes implement it and the ladder above knows nothing else. The two
// methods that change an identity are named for what they change rather than
// for when they are called, because "when" is the ladder's business — and
// because confusing them on this target is expensive: changing the address
// destroys a Qrator clearance that cost nineteen to twenty-three seconds to
// obtain.
type Lease interface {
	Do(req *http.Request) (*http.Response, error)

	// Session changes exactly when the identity does, and never otherwise. It
	// is what a caller keys a visit on.
	Session() string

	// Label is for a person reading a log line: "blanktrail:20003" or
	// "direct:chrome-131-win#2".
	Label() string

	// Profile says who owns which header for this lease.
	Profile() Profile

	// Warm reports whether this identity has completed a warm-up. Only the
	// transport knows, and the classifier needs it: the measured cold baseline
	// is a 429 carrying wall markers, which means "warm up" before a warm-up
	// and "you are being throttled" after one. Guessing it from the attempt
	// number would make ClassCold either unreachable or permanent.
	Warm() bool

	// RotateEgress replaces the exit address and keeps the identity. Reachable
	// from exactly one branch of the ladder, ClassThrottled, and a test
	// enforces that.
	RotateEgress(ctx context.Context) error

	// RenewIdentity replaces the fingerprint, the cookie jar and the session,
	// keeping the address. It costs a re-solve. The remedy for ClassBurned and
	// ClassSoftBlock, both of which are bound to the identity rather than to
	// the IP.
	RenewIdentity(ctx context.Context) error

	Release()
}

// Leaser hands out rested identities.
type Leaser interface {
	// Acquire blocks until an identity has rested long enough to be used, and
	// returns the one that has rested longest.
	Acquire(ctx context.Context) (Lease, error)
	Mode() Mode
	Caps() Caps
	Close() error
}
