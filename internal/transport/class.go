// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"errors"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Class is what one response turned out to be.
type Class string

const (
	ClassOK        Class = "ok"
	ClassEmpty     Class = "empty"      // the site's own answer: nothing matched
	ClassShape     Class = "shape"      // our parser stopped understanding the payload
	ClassSoftBlock Class = "soft-block" // 200, no container of any kind
	ClassCold      Class = "cold"       // an unwarmed identity being told to warm up
	ClassThrottled Class = "throttled"
	ClassBurned    Class = "burned"    // 403: this identity is finished here
	ClassChallenge Class = "challenge" // 439, or a captcha marker anywhere
	ClassMangled   Class = "mangled"   // the bytes are not what they claim to be
	ClassOther     Class = "other"
)

// Remedy is what a class asks the ladder to do about it.
type Remedy string

const (
	RemedyNone          Remedy = "none"
	RemedyRetrySame     Remedy = "retry-same"     // same identity, same address
	RemedyRotateEgress  Remedy = "rotate-egress"  // new address, same identity
	RemedyRenewIdentity Remedy = "renew-identity" // new identity, same address
	RemedyWarmUp        Remedy = "warm-up"        // a root navigation, then one retry
	RemedyRedial        Remedy = "redial"
	RemedyWait          Remedy = "wait" // same identity, same address, just time
)

// Remedy is the single table the ladder reads.
//
// Rotation is reachable from exactly one class, and a test enforces that. A
// Qrator clearance is domain-wide, long-lived, costs nineteen to twenty-three
// seconds to obtain and is destroyed by any upstream change — so rotating on a
// 403 or a 439 throws away something that was working in order to fix something
// rotation cannot fix.
func (c Class) Remedy() Remedy {
	switch c {
	case ClassOK, ClassEmpty:
		return RemedyNone
	case ClassShape:
		// Repeating a request whose payload we have stopped understanding
		// spends the budget on our own bug. Archive the body instead.
		return RemedyNone
	case ClassBurned:
		// Measured: a 403 heals with time and the identity is not what was
		// refused. An arm whose RenewIdentity was a no-op recovered from four
		// consecutive 403s on one unchanged srv_id; an arm that did renew was
		// no faster. Eighteen of eighteen cleared by waiting, none failed to.
		//
		// Renewing would discard a Qrator clearance worth nineteen to
		// twenty-three seconds to fix something the identity was not causing.
		return RemedyWait
	case ClassSoftBlock:
		// NOT measured. A 200 with no container is a different signal from a
		// 403 and keeps the remedy it was given until someone runs the arm that
		// separates them, the way direct-norenew separated the 403.
		return RemedyRenewIdentity
	case ClassCold:
		return RemedyWarmUp
	case ClassThrottled:
		return RemedyRotateEgress
	case ClassChallenge:
		// The ladder decides between repeating on the same identity and
		// halting, and it decides from Caps.SolvesChallenges. This table does
		// not know which mode is running.
		return RemedyRetrySame
	case ClassMangled:
		return RemedyRedial
	default:
		return RemedyNone
	}
}

// Input is everything the classifier is given. The extraction is part of it
// rather than an afterthought: on this target an empty query, a dropped
// hydration state and a payload we have stopped understanding are all HTTP 200
// with no listings, and only the parse tells them apart.
type Input struct {
	Status int
	Doc    avito.Document
	Page   avito.Page
	// ExtractErr is what avito.Extract returned. avito.ErrNoExtractor means no
	// rule recognised the document, which is NOT the same as an empty result.
	ExtractErr error
	// Warm says whether this identity has ever completed a warm-up. The same
	// 429-with-a-wall means two different things either side of that line, and
	// only the caller knows which side it is on.
	Warm bool
}

// Verdict is the class and the evidence for it.
type Verdict struct {
	Class  Class
	Wall   avito.WallHit
	Detail string
}

// captchaLabels are the wall labels that mean an interactive challenge rather
// than a refusal. The distinction is the whole of mode (b)'s boundary: a
// refusal can be waited out, and a challenge cannot be passed at all without a
// browser.
var captchaLabels = map[string]bool{
	avito.LabelSlider:  true,
	avito.LabelGeetest: true,
}

// Classify turns one response plus its extraction into one verdict.
func Classify(in Input) Verdict {
	wall, hasWall := in.Doc.Wall()

	// A captcha marker at any status is a challenge, and 439 is Avito's own
	// challenge status — it appears in no RFC.
	if in.Status == 439 || (hasWall && captchaLabels[wall.Label]) {
		return Verdict{ClassChallenge, wall, "captcha marker or 439"}
	}

	switch in.Status {
	case 429:
		// The measured cold baseline is a 429 whose body carries a wall marker,
		// on an identity that has never warmed up. It is the state the whole
		// warm-jar design exists to leave, not a wall to stop at.
		if hasWall && !in.Warm {
			return Verdict{ClassCold, wall, "unwarmed identity refused; warm up and retry"}
		}
		return Verdict{ClassThrottled, wall, "throttled"}
	case 403:
		return Verdict{ClassBurned, wall, "this identity is finished on this address"}
	}

	if in.ExtractErr != nil {
		switch {
		case errors.Is(in.ExtractErr, avito.ErrNotUTF8),
			errors.Is(in.ExtractErr, avito.ErrEmptyBody),
			errors.Is(in.ExtractErr, avito.ErrNoCyrillic):
			// Almost always a body that arrived still compressed. Left
			// unrecognised it reads downstream as "the query matched nothing",
			// which is the single most expensive silent failure in this system.
			return Verdict{ClassMangled, wall, in.ExtractErr.Error()}
		case errors.Is(in.ExtractErr, avito.ErrNoExtractor):
			if hasWall {
				return Verdict{ClassSoftBlock, wall, "no container, and a wall marker"}
			}
			return Verdict{ClassSoftBlock, wall, "no result container for this surface"}
		}
	}

	if in.Status >= 200 && in.Status < 300 {
		switch {
		case in.Page.Ok():
			return Verdict{ClassOK, wall, ""}
		case in.Page.EmptyDeclared,
			in.Page.TotalCount != nil && *in.Page.TotalCount == 0:
			// The container was present and reported zero, corroborated by the
			// site's own wording. That is an answer, and answering it with a
			// remedy would re-pay a twenty-second solve on a quiet query.
			return Verdict{ClassEmpty, wall, "the site reported no matches"}
		case in.Page.Kind == avito.EnvelopeRedirect, in.Page.Kind == avito.EnvelopeItem:
			return Verdict{ClassOK, wall, ""}
		case in.Page.Kind == avito.EnvelopeCatalog:
			// The container said there were results and we read none. Ours.
			return Verdict{ClassShape, wall, "container reports results, extractors read none"}
		}
		return Verdict{ClassSoftBlock, wall, "2xx with nothing recognisable in it"}
	}
	return Verdict{ClassOther, wall, "unhandled status"}
}
