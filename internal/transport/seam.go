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

	// Egress names the exit address, and it is NOT the label.
	//
	// The pacer's second budget is per address: sixty requests per ten minutes,
	// whatever is leaving through it. It was keyed on Label, and Label is per
	// endpoint — "direct:chrome-133-android#0" through "#3", or
	// "blanktrail:20000" and ":20011". Four sessions sharing one IP therefore
	// got four separate windows and four times the budget they were entitled
	// to, and a soak arm with four jars on one address blew through the address
	// limit exactly as that arithmetic predicts.
	//
	// Endpoints sharing an address MUST return the same string here.
	Egress() string

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
	// keeping the address. It costs a re-solve. It is the fallback for a 403
	// that waiting has not cleared, which the measurement says is rare.
	RenewIdentity(ctx context.Context) error

	// Recycle discards the endpoint itself and builds a new one in its place,
	// keeping the address. It is strictly stronger than RenewIdentity and the
	// difference is why it exists.
	//
	// Measured: a BlankTrail port that had stopped returning data was renewed
	// six times inside one fetch — twenty-seven minutes — and answered with an
	// empty page every time, while its sibling port on the SAME exit address
	// kept serving in under two seconds. RenewIdentity rotates the profile; the
	// port, and whatever the proxy holds for it, survives that. Only closing
	// the port fixes a stuck port.
	//
	// The remedy for ClassSoftBlock.
	Recycle(ctx context.Context) error

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
