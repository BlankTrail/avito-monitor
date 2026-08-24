// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

// Comparable reports whether this pass may be differenced against the one
// before it.
//
// It is a separate question from Admissible and both are required. Admissible
// asks whether this pass is a good reading of the market. Comparable asks
// whether it and its predecessor are readings of the SAME market — and on this
// target they can fail to be, which the design did not know until it was
// measured.
//
// Measured 2026-08-24, mobile, date-sorted, direct transport: the result set is
// a function of srv_id. Six passes over two identities gave 100% overlap within
// an identity, every pair and in the same order, and 23% across them; two fresh
// identities shared zero of thirty. The ladder changes the identity by itself —
// it renews on a 403 three waits did not clear and recycles the endpoint on a
// soft block — so this is not a rare configuration mistake but an ordinary
// Tuesday.
//
// A pass that is not comparable is still a perfectly good reading. Its members
// are recorded and its arrivals are real. What it cannot do is confirm that
// anything LEFT, because the thing it would be compared against was a different
// slice of the market.
//
// This is deliberately not folded into Admissible. The two reasons go to
// different places and mean different things to whoever reads them: one says
// "we did not read the market properly", the other says "we read a different
// part of it".
func Comparable(p PassFacts) Verdict {
	if p.PrevSession == "" {
		return Verdict{false, "no previous pass to compare against"}
	}
	if p.Session != p.PrevSession {
		return Verdict{false, "the identity changed since the last pass, and " +
			"this site shows a different result set to a different identity"}
	}
	return Verdict{true, ""}
}
