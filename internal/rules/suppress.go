// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"math"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// Lifted from WB_GO's internal/rules/suppress.go, keeping its cheapest-first
// ordering and its record-the-key-either-way rule. Two edits, both below.
//
// This file exists because a monitor that sends forty messages in one pass is
// switched off on the second day. Everything here answers one question — should
// this match actually reach anybody — and the answer is never a silent no.
// Reason is what goes into rule_events.suppressed_by, and it is the only answer
// the product can give to «почему мне не сказали», which is the question a
// person asks after they stop receiving something they were relying on.

// Reason names what stopped a message.
type Reason string

const (
	// ReasonNone means it was not stopped.
	ReasonNone Reason = ""
	// ReasonQuietHours is the do-not-disturb window, and the only one an urgent
	// rule is exempt from.
	ReasonQuietHours Reason = "quiet-hours"
	// ReasonRateLimit is the per-subject floor on how often one rule may speak.
	ReasonRateLimit Reason = "rate-limit"
	// ReasonDuplicate is the same change, again.
	ReasonDuplicate Reason = "duplicate"
	// ReasonThreshold is a move too small to be worth a message.
	ReasonThreshold Reason = "threshold"
	// ReasonSeed is a watch's first pass, or a pass that could not be compared
	// with the one before it. Both produce arrivals that are real and are not
	// news, and increment 4 marks exactly those with backfill.
	ReasonSeed Reason = "seed"
	// ReasonRepost is an appearance that is really the same advertisement
	// posted again under a new number.
	ReasonRepost Reason = "repost"
)

// Decision is what to do about one match.
type Decision struct {
	Send bool
	// Reason is empty when it sends and otherwise names what stopped it.
	Reason Reason
	// DedupKey is recorded either way, because the next decision compares
	// against it — including when this one was itself suppressed. A suppressed
	// firing that recorded nothing would make the one after it a duplicate of
	// nothing.
	DedupKey string
}

// SubjectKind is what a rate limit counts against.
type SubjectKind string

const (
	SubjectListing SubjectKind = "listing"
	SubjectThread  SubjectKind = "thread"
)

// SubjectPair is what a rate limit counts against.
//
// WB_GO's LastFired takes one id. Here it takes a pair, because the limit is
// per thread for a price rule and per listing for a lifecycle one: a repost
// must not reset a price rule's limit, and it must not silence the successor's
// appearance either. One id could not express both.
type SubjectPair struct {
	Kind SubjectKind
	Key  int64
}

// Subject is what this rule counts its rate limit against for this event.
//
// The grouping is DedupKey's, and deliberately so: the two answer the same
// question at two time scales — "is this the same thing again" and "have I
// spoken about this thing recently" — and a rate limit drawn on a different
// grain from the deduplication would let one of them undo the other.
func Subject(ev Event) SubjectPair {
	switch ev.Change.Kind {
	case track.KindPriceChanged, track.KindPriceAppeared,
		track.KindPriceWithdrawn, track.KindDiscountChanged,
		track.KindTitleChanged:
		return SubjectPair{Kind: SubjectThread, Key: ev.ThreadID}
	}
	return SubjectPair{Kind: SubjectListing, Key: ev.Change.ItemID}
}

// QuietHours is a do-not-disturb window, in minutes since local midnight.
//
// Minutes rather than the donor's whole hours because 22:30 is a thing a person
// asks for and 22 is not the same request.
//
// From equal to To means no window at all rather than a full day: "quiet from 9
// to 9" as a synonym for "silence forever" is a foot-gun in a settings screen,
// and somebody who wants silence disables the rule.
//
// From greater than To wraps midnight, which is the ordinary case — quiet from
// 22:00 to 07:00 — so it is supported rather than rejected.
type QuietHours struct {
	FromMin, ToMin int
	// Location is whose evening it is. Nil means UTC, which is nobody's
	// evening; a caller that leaves it nil in production gives its user quiet
	// hours three time zones from where they asked for them.
	Location *time.Location
}

// Active reports whether t falls inside the window.
func (q QuietHours) Active(t time.Time) bool {
	if q.FromMin == q.ToMin {
		return false
	}
	loc := q.Location
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	m := local.Hour()*60 + local.Minute()
	if q.FromMin < q.ToMin {
		return m >= q.FromMin && m < q.ToMin
	}
	// Wrapping midnight: 22:00 to 07:00 means late evening or early morning.
	return m >= q.FromMin || m < q.ToMin
}

// Suppressor decides whether one match reaches anybody.
//
// The two history lookups are functions rather than a store, so this whole file
// is testable by writing down two timestamps — and so that the package does not
// have to know what a database is.
type Suppressor struct {
	Quiet QuietHours

	// Now is the clock. A nil Now falls back to time.Now rather than to the
	// zero time, which would put every decision in 1970 and make quiet hours
	// meaningless in a way nothing would report.
	Now func() time.Time

	// LastFired reports when this rule last spoke about this subject.
	LastFired func(ruleID int64, subject SubjectPair) (time.Time, bool)

	// SeenKey reports whether this exact change already produced a decision.
	SeenKey func(ruleID int64, key string) bool
}

// Decide says whether this match should reach anybody.
//
// The order is cheapest-first, and that is not style: SeenKey and LastFired are
// database reads, and a rule whose threshold refuses the change must not pay
// for them. It also decides which reason a person is given when more than one
// applies, and the most useful reason is the most specific — a move below the
// threshold is told that, not "rate limit". The first is a number they chose
// and can change; the second sounds like the product deciding on their behalf.
func (s Suppressor) Decide(r Rule, ev Event) Decision {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	key := DedupKey(r, ev)
	stop := func(reason Reason) Decision {
		return Decision{Send: false, Reason: reason, DedupKey: key}
	}

	// Free, and the most specific answers there are.
	if ev.Change.Backfill {
		return stop(ReasonSeed)
	}
	if isRepost(ev) {
		return stop(ReasonRepost)
	}
	if !significant(r, ev) {
		return stop(ReasonThreshold)
	}

	// From here on it costs a database read.
	if s.SeenKey != nil && s.SeenKey(r.ID, key) {
		return stop(ReasonDuplicate)
	}
	if r.MinInterval > 0 && s.LastFired != nil {
		if last, ok := s.LastFired(r.ID, Subject(ev)); ok &&
			now.Sub(last) < r.MinInterval {
			return stop(ReasonRateLimit)
		}
	}

	// Last, and the only one an urgent rule skips. Checked after the others so
	// that a message stopped for being trivial says so even at night: told
	// "quiet hours", a person waits until morning for something that was never
	// coming.
	if !r.Urgent && s.Quiet.Active(now) {
		return stop(ReasonQuietHours)
	}
	return Decision{Send: true, Reason: ReasonNone, DedupKey: key}
}

// isRepost is an appearance that is really the same advertisement posted again
// under a new number.
//
// Only an appearance. A price move on a reposted listing is news about the
// market and must not be suppressed for the accident of which incarnation
// carried it — which is also why DedupKey groups the price kinds by thread
// rather than dropping them.
//
// This is the path until increment 7 links reposts as they happen; once it
// does, a successor is emitted as listing-reposted and never reaches here as an
// appearance at all. The check stays afterwards for the retrospective case: a
// link established later does not un-send yesterday's message, but it does stop
// the same seller's next repost from being announced as a new find.
func isRepost(ev Event) bool {
	switch ev.Change.Kind {
	case track.KindListingAppeared, track.KindListingReturned:
		return ev.RepostOf != nil
	}
	return false
}

// significant reports whether the move clears the rule's floors.
//
// Both floors have to be cleared, not either: somebody who set both meant "big
// enough in percent AND big enough in money", which is how the two together
// stop a one-percent move on a cheap item and a fifty-kopeck move on an
// expensive one with the same rule.
//
// A change with no percentage — something that appeared, or moved from zero —
// clears the percentage floor rather than failing it. The alternative would
// silently drop every appearance for every rule that set a percentage, and an
// appearance is not a small move; it is a move whose size has no percentage.
func significant(r Rule, ev Event) bool {
	if r.ThresholdPct > 0 {
		if pct, ok := ev.value(FieldPricePct); ok &&
			math.Abs(pct) < float64(r.ThresholdPct) {
			return false
		}
	}
	if r.ThresholdMinor > 0 {
		// Applied only to money. A threshold in kopecks against a position or a
		// bump time would compare two different things and quietly filter out
		// every move of a rule whose kind is not about money.
		if ev.Change.Unit != track.UnitMinor {
			return true
		}
		if delta, ok := ev.value(FieldPriceDelta); ok &&
			math.Abs(delta) < float64(r.ThresholdMinor) {
			return false
		}
	}
	return true
}
