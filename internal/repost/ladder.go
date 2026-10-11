// SPDX-License-Identifier: AGPL-3.0-or-later

package repost

import (
	"fmt"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"math"
)

// The ladder, and its one rule: a missing signal contributes nothing.
//
// Not «contributes zero to an average» — contributes nothing at all, because an
// average renormalised over the signals that happen to be present lets a
// title-and-price pair reach the same score as a title-price-description-image
// pair. Description and image are on twenty rows in fifty, measured, so that
// renormalisation is a false-positive factory — and a false link silently
// withholds an alert and looks like data rather than a bug.
//
// So the rungs are conjunctions of named facts, and the score exists only to be
// shown to a person. Nothing decides on the score.

// Basis is how a link was arrived at, and it is stored beside the link.
type Basis string

const (
	BasisStrong Basis = "strong"
	BasisWeak   Basis = "weak"
	BasisManual Basis = "manual"
)

// The terms, named once so the tests and the panel cannot drift apart.
const (
	termTitle       = "Заголовок"
	termDescription = "Описание"
	termImage       = "Фотография"
	termCoords      = "Координаты"
	termPrice       = "Цена"
	termCandidates  = "Кандидатов"
	termGate        = "Ворота"
)

// priceTolerance is how far a price may move and still corroborate.
//
// A fifth. A repost at the same moment is usually the same price or a little
// lower — the seller is trying again — and a half-price relist is a different
// item or a different seller's copy. Inclusive at the bound.
const priceTolerance = 0.20

// coordTolerance is how close two address-level coordinates must be.
//
// About a hundred metres in degrees of latitude. Address-level precision is
// what the extractor's coords_precise records, and two readings of one address
// differ in the last places rather than in the third decimal.
const coordTolerance = 0.001

// Term is one fact the ladder looked at, present or not.
type Term struct {
	Name    string
	Present bool
	Detail  string // Russian, shown verbatim
}

// Verdict is what to write down.
type Verdict struct {
	State    string // "linked", "proposed", "considered", "rejected"
	Basis    Basis
	Score    float64
	Evidence []Term
}

// Judge decides what to do with a candidate pair the gates have already seen.
//
// candidates is how many survived the gates for this arrival, and it is a
// first-class input rather than a detail: more than one links nothing. A dealer
// with eight identical live phones is the common case on this site, and the
// asymmetry says a missed link is the product working while a wrong one is a
// withheld alert that looks like data.
func Judge(newer, older Listing, gate GateResult, candidates int) Verdict {
	if !gate.Passed {
		return Verdict{
			State: "rejected",
			Evidence: []Term{{
				Name: termGate, Present: false, Detail: gate.Why,
			}},
		}
	}

	title := newer.Title != "" && newer.Title == older.Title
	description := bothPresent(newer.DescriptionSHA, older.DescriptionSHA) &&
		newer.DescriptionSHA == older.DescriptionSHA
	image := bothPresent(newer.ImageKey, older.ImageKey) &&
		newer.ImageKey == older.ImageKey
	coords := sameAddress(newer, older)
	price := samePrice(newer.PriceMinor, older.PriceMinor)

	ev := []Term{
		{termTitle, title, titleDetail(newer, older, title)},
		{termDescription, description, presence(newer.DescriptionSHA,
			older.DescriptionSHA, description, "текст совпадает",
			"тексты разные", "описание есть не у обоих")},
		{termImage, image, presence(newer.ImageKey, older.ImageKey, image,
			"та же фотография", "фотографии разные",
			"фотографию прочитать не удалось")},
		{termCoords, coords, coordDetail(newer, older, coords)},
		{termPrice, price, priceDetail(newer.PriceMinor, older.PriceMinor, price)},
		{termCandidates, candidates == 1, fmt.Sprintf(
			"подходящих старых объявлений: %d", candidates)},
	}
	if gate.Undetermined {
		ev = append(ev, Term{termGate, false, gate.Why})
	}

	// The score is for reading and never for deciding. One point per fact that
	// held, so a person can sort a list; the rungs below are conjunctions.
	score := 0.0
	for _, t := range ev[:5] {
		if t.Present {
			score++
		}
	}

	v := Verdict{Score: score, Evidence: ev}
	switch {
	// The strong rung: the title, one strong corroborator, exactly one
	// candidate, and a seller both sides agreed on.
	case title && (description || image) && candidates == 1 && !gate.Undetermined:
		v.State, v.Basis = "linked", BasisStrong

	// The weak rung: the title, an address both sides state precisely, a price
	// within a fifth, one candidate, and again a known seller.
	case title && coords && price && candidates == 1 && !gate.Undetermined:
		v.State, v.Basis = "linked", BasisWeak

	// An undetermined seller can reach a proposal and no further. A proposal
	// withholds nothing: the ordinary new-listing alert fires with a line
	// attached, which is the whole point of the ceiling.
	case title:
		v.State = "proposed"

	default:
		v.State = "considered"
	}
	return v
}

// bothPresent is the rule that stops two absences reading as a match.
//
// Two empty description digests are not an identical description and two empty
// image keys are not the same photo. Counted, the mobile surface — which
// carries neither on any row — would look like a perfect match with itself
// every single time.
func bothPresent(a, b string) bool { return a != "" && b != "" }

func sameAddress(a, b Listing) bool {
	if !a.CoordsPrecise || !b.CoordsPrecise {
		// A city centroid is shared by half a district, which is exactly why
		// the extractor records precision at all.
		return false
	}
	if a.Lat == nil || a.Lng == nil || b.Lat == nil || b.Lng == nil {
		return false
	}
	return math.Abs(*a.Lat-*b.Lat) <= coordTolerance &&
		math.Abs(*a.Lng-*b.Lng) <= coordTolerance
}

func samePrice(a, b int64) bool {
	// Zero is our silence and not a number. Treated as one, every pair of
	// unpriced listings would be a perfect price match with each other.
	if a <= 0 || b <= 0 {
		return false
	}
	high, low := a, b
	if low > high {
		high, low = low, high
	}
	return float64(high-low)/float64(high) <= priceTolerance
}

func presence(a, b string, matched bool, yes, no, absent string) string {
	if !bothPresent(a, b) {
		return absent
	}
	if matched {
		return yes
	}
	return no
}

func titleDetail(a, b Listing, matched bool) string {
	if matched {
		return "совпадает дословно: «" + a.Title + "»"
	}
	return "разные: «" + a.Title + "» и «" + b.Title + "»"
}

func coordDetail(a, b Listing, matched bool) string {
	switch {
	case !a.CoordsPrecise || !b.CoordsPrecise:
		return "точный адрес известен не у обоих — центр города не в счёт"
	case matched:
		return "тот же адрес"
	default:
		return "адреса разные"
	}
}

func priceDetail(a, b int64, matched bool) string {
	switch {
	case a <= 0 || b <= 0:
		return "цену прочитать не удалось хотя бы у одного"
	case matched:
		return fmt.Sprintf("отличается меньше чем на %.0f%%: %s и %s ₽",
			priceTolerance*100, ru.Thousands(b/100), ru.Thousands(a/100))
	default:
		return fmt.Sprintf("отличается сильнее чем на %.0f%%: %s и %s ₽",
			priceTolerance*100, ru.Thousands(b/100), ru.Thousands(a/100))
	}
}
