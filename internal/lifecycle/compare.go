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
	if p.Session != p.PrevSession && !sameWindow(p.PrevItems, p.Items) {
		return Verdict{false, "the identity changed since the last pass, and " +
			"this site shows a different result set to a different identity"}
	}
	return Verdict{true, ""}
}

// sameWindow says, from the two readings themselves, whether they are slices of
// the same market.
//
// The rule above came from direct mode, where the result set is a function of
// srv_id. Measured on 11 October 2026 through BlankTrail with a rotating pool,
// date-sorted: consecutive passes on different sessions and different exits
// shared thirty of thirty, in order. Taken as written, the identity rule there
// called every pass incomparable, so a listing could never be confirmed gone on
// any installation with more than one exit.
//
// So the data decides. One market read twice looks like «new arrivals on top,
// then the previous pass in its order»: from the first item the two share, the
// rest of the new pass is almost all the old one. Two different slices share
// items scattered among strangers. Measured direct-mode cross-identity overlap
// was 23% and fresh identities 0%; the bar here is that the shared tail be at
// least four fifths old items, over at least five shared — a few coincidences
// in a busy market cannot clear it.
func sameWindow(prev, cur []int64) bool {
	if len(prev) == 0 || len(cur) == 0 {
		return false
	}
	old := make(map[int64]bool, len(prev))
	for _, id := range prev {
		old[id] = true
	}
	first := -1
	for i, id := range cur {
		if old[id] {
			first = i
			break
		}
	}
	if first < 0 {
		return false
	}
	tail := cur[first:]
	shared := 0
	for _, id := range tail {
		if old[id] {
			shared++
		}
	}
	return shared >= 5 && shared*5 >= len(tail)*4
}
