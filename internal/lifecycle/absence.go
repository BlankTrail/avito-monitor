// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import "fmt"

// Member is what a watch believes about one listing it has seen.
//
// LastPosition and WindowSize together are the displacement account, and
// MissingStreak and MissingSince are the two gates. All four live on the
// (watch, listing) pair and never on the listing: a listing belongs to several
// watches, and absence from one is not absence from the listing. One counter
// for both is the shortest path to announcing removals that never happened.
type Member struct {
	ItemID        int64
	LastPosition  int
	WindowSize    int
	MissingStreak int
	// MissingSince is when this watch first failed to see it. Zero means no
	// such moment has been recorded — not 1970, which read as a timestamp
	// would make every first absence instantly confirmable.
	MissingSince int64
}

// AbsenceInput is one member measured against one pass.
type AbsenceInput struct {
	Member Member
	// Arrivals is how many listings in this pass were not in the previous one.
	// It is the whole input to the displacement arithmetic.
	Arrivals      int
	Now           int64
	Confirmations int
	MinSpanSec    int
}

// Absence is why a listing is not here, and whether that is believable.
type Absence struct {
	Displaced bool
	Confirmed bool
	Why       string
}

// Explain says why a listing is not in this pass, and whether that is
// believable as a disappearance.
//
// Page one of a date-sorted query is a window, not a census. A listing leaves
// it because it was removed, because a newer posting pushed it off the bottom,
// or because a paid promotion reshuffled the block — and the response says
// which of the three it was for none of them.
//
// The order is deliberate: displacement is checked first and wins outright. It
// is the stronger statement — we know why the item is not here — where the two
// gates only say how long we have been unsure.
func Explain(in AbsenceInput) Absence {
	// In a date-sorted window of k slots, an item at rank r is pushed out by
	// k − r arrivals. Exact arithmetic over two integers the schema already
	// stores and one it counts per pass; it costs no extra request, and it is
	// the difference between "gone" and "we only look at the first page".
	//
	// A window of zero is not a window. A pass that returned nothing would
	// otherwise make every absence displacement, which is the opposite of what
	// an empty result set means.
	if in.Member.WindowSize > 0 {
		headroom := in.Member.WindowSize - in.Member.LastPosition
		if headroom < 0 {
			headroom = 0
		}
		if in.Arrivals >= headroom {
			return Absence{Displaced: true, Why: fmt.Sprintf(
				"rank %d of %d leaves %d slots of headroom, and %d arrivals is "+
					"enough to push it off",
				in.Member.LastPosition, in.Member.WindowSize, headroom, in.Arrivals)}
		}
	}

	// Both gates, and both are required. Two passes thirty seconds apart is one
	// ranking blip; the span gate is what makes the count mean elapsed time
	// rather than poll count — without it a two-minute watch confirms a
	// disappearance in four minutes and an hourly one takes two hours, on
	// exactly the same evidence.
	countMet := in.Member.MissingStreak >= in.Confirmations
	spanMet := in.Member.MissingSince > 0 &&
		in.Now-in.Member.MissingSince >= int64(in.MinSpanSec)

	switch {
	case countMet && spanMet:
		return Absence{Confirmed: true, Why: fmt.Sprintf(
			"missing from %d passes over %d seconds, with %d arrivals nowhere "+
				"near the %d needed to displace it",
			in.Member.MissingStreak, in.Now-in.Member.MissingSince,
			in.Arrivals, in.Member.WindowSize-in.Member.LastPosition)}
	case countMet:
		return Absence{Why: fmt.Sprintf(
			"missing from %d passes, but only %d seconds have elapsed and the "+
				"watch asks for %d",
			in.Member.MissingStreak, in.Now-in.Member.MissingSince, in.MinSpanSec)}
	case spanMet:
		return Absence{Why: fmt.Sprintf(
			"missing for %d seconds, but from only %d passes and the watch asks "+
				"for %d",
			in.Now-in.Member.MissingSince, in.Member.MissingStreak, in.Confirmations)}
	default:
		return Absence{Why: fmt.Sprintf(
			"missing from %d passes; the watch asks for %d and for %d seconds",
			in.Member.MissingStreak, in.Confirmations, in.MinSpanSec)}
	}
}
