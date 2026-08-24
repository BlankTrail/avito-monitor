// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// noRenew makes RenewIdentity do nothing while leaving everything else alone.
//
// It exists to separate two things the ladder always does together. On a soft
// block or a 403 the ladder renews the identity AND waits four minutes, and the
// first soak run recovered after exactly that — which says nothing about which
// half did the work. If the wait is what heals, then renewing throws away a
// Qrator clearance that cost nineteen to twenty-three seconds to obtain, in
// order to fix something the clearance was not causing. That is the same
// mistake the remedy table forbids in the other direction, and the table cannot
// be trusted until the two are measured apart.
//
// It lives in the harness rather than behind a production flag on purpose. This
// is an experiment, not a supported mode: a monitor that never renews an
// identity has no answer to an identity that really is burnt.
type noRenew struct{ transport.Leaser }

func (n noRenew) Acquire(ctx context.Context) (transport.Lease, error) {
	l, err := n.Leaser.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return noRenewLease{l}, nil
}

type noRenewLease struct{ transport.Lease }

// RenewIdentity reports success and changes nothing, so the ladder still spends
// its four minutes and still comes back on the identity the site refused.
func (noRenewLease) RenewIdentity(context.Context) error { return nil }

// Do, Session, Label, Profile, Warm, RotateEgress and Release are the embedded
// lease's own. Only the one method under test is replaced.
var (
	_ transport.Leaser = noRenew{}
	_ transport.Lease  = noRenewLease{}
)
