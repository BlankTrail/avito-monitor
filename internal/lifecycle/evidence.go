// SPDX-License-Identifier: AGPL-3.0-or-later

// Package lifecycle answers three questions about a pass, and nothing else.
//
// Is it admissible as evidence that something is gone. Is it comparable with
// the pass before it. Is this particular absence explained by displacement.
//
// It holds no clock and no database. Every input is a value the caller read,
// which is what makes each answer reproducible from the row that produced it —
// "why was I told this was gone" has to stay answerable a month later.
package lifecycle

import "fmt"

// ReseedMarker ends the reason a re-seed gives. The store finds re-seeds by
// it, to restart the gap clock from them.
const ReseedMarker = "this is a re-seed rather than a reading"

// reseedAfterIntervals is how stale the last evidential pass may be before the
// next one is a re-seed rather than a reading.
//
// Three, because two is inside ordinary jitter for a laptop that slept through
// one tick, and refusing there would make an install that is merely slow look
// like one that is broken.
const reseedAfterIntervals = 3

// collapseFraction is how far a result count may fall and still be believed.
//
// A quarter is far outside ordinary churn on a page of thirty to fifty and well
// inside what a truncated or partially served page looks like.
const collapseFraction = 4

// PassFacts is everything the two verdicts need, read from the pass and the
// one before it.
type PassFacts struct {
	Class    string
	Surface  string
	Path     string
	Degraded bool
	IsSeed   bool

	// TotalCount is the site's own count for this pass, and LastTotal is the
	// one from the last admissible pass of this watch. Either may be nil: a
	// surface that reported no number gets the collapse test skipped rather
	// than failed.
	TotalCount *int64
	LastTotal  *int64

	// Session identifies the identity this pass was read through, and
	// PrevSession the previous pass's. They are what Comparable reads.
	Session     string
	PrevSession string
	// Items and PrevItems are this pass and the last one that read anything,
	// in page order. Comparable reads them when the identity changed.
	Items     []int64
	PrevItems []int64

	StartedAt      int64
	PrevEvidenceAt int64
	IntervalSec    int
}

// Verdict is an answer with its reason.
//
// The reason is not decoration. It lands in passes.degraded_why and is read by
// somebody asking why their watch never tells them anything, so a refusal
// without one is a question nobody can answer afterwards.
type Verdict struct {
	Admissible bool
	Why        string
}

// Admissible reports whether this pass may be used as evidence that something
// has LEFT.
//
// Anything it refuses is still presence evidence: a listing seen in such a pass
// is alive, and only the argument from absence is barred.
func Admissible(p PassFacts) Verdict {
	// A pass that met the transport rather than the market says nothing about
	// the market. Every class the soak measured — a 429 that is really a
	// captcha, a 403 that heals with time, a soft block that is a stuck
	// endpoint — is our failure to read and not the market's decision.
	if p.Class != "ok" && p.Class != "empty" {
		return Verdict{false, fmt.Sprintf(
			"class %q is not an answer from the market", p.Class)}
	}

	if !primaryPath(p.Surface, p.Path) {
		return Verdict{false, fmt.Sprintf(
			"the %s surface was answered by %q rather than by its own primary "+
				"extractor, which on desktop is the bot-classification warning",
			p.Surface, p.Path)}
	}

	if p.Degraded {
		return Verdict{false, "the extraction reported a degradation"}
	}

	// A seed has no predecessor, so there is nothing it could have left.
	if p.IsSeed {
		return Verdict{false, "a seeding pass has nothing behind it to differ from"}
	}

	// A gap in collection is not evidence of anything, and for a laptop this is
	// a weekly event. A machine that slept from Friday to Monday would wake up,
	// see a page one that had turned over completely, and report every listing
	// it was watching as gone — one weekend of a hundred false removals is how
	// a person learns to ignore the notifications. The same gate covers a
	// changed network, a proxy restart and a paused install; none of them are
	// evidence about Avito.
	if p.PrevEvidenceAt > 0 && p.IntervalSec > 0 {
		if gap := p.StartedAt - p.PrevEvidenceAt; gap > int64(reseedAfterIntervals*p.IntervalSec) {
			return Verdict{false, fmt.Sprintf(
				"%d seconds since the last admissible pass, over %d intervals: "+
					ReseedMarker,
				gap, reseedAfterIntervals)}
		}
	}

	// A missing number must never become arithmetic on zero, so a surface that
	// reported no count skips this test rather than failing it.
	if p.LastTotal != nil && p.TotalCount != nil && *p.LastTotal > 0 {
		if *p.TotalCount*collapseFraction < *p.LastTotal {
			return Verdict{false, fmt.Sprintf(
				"the result count fell from %d to %d, which is what a truncated "+
					"page looks like", *p.LastTotal, *p.TotalCount)}
		}
	}

	return Verdict{true, ""}
}

// primaryPath reports whether this path is the surface's own primary extractor.
//
// This is the clause to read carefully, and it is written this way because of a
// measurement rather than out of symmetry.
//
// On desktop the primary path is 'state', and a silent fall back to 'dom' is
// exactly the bot-classification warning: loaderData.toggles carries
// runtime_catalog_drop_state_for_bots, so Avito can serve a page with its
// hydration state removed, and the switch is the earliest signal available —
// earlier than a 429 and much earlier than a captcha.
//
// On mobile there IS no data-mfe-state block at all — measured 2026-08-23 — so
// 'dom' is the primary path there and nothing about it is a degradation.
// Reading the rule as "state or nothing" would make every mobile pass
// inadmissible, and no disappearance could ever be confirmed on the surface the
// product collects from by default.
func primaryPath(surface, path string) bool {
	switch surface {
	case "desktop":
		return path == "state"
	case "mobile":
		// Both readers run on this surface and both are required: the
		// __initialData__ parse for the count and the card walk for the
		// listings. Either attribution is the primary path.
		return path == "dom" || path == "state"
	default:
		return false
	}
}
