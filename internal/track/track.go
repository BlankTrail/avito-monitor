// SPDX-License-Identifier: AGPL-3.0-or-later

package track

import (
	"errors"
	"fmt"
	"strconv"
)

// Reading is one listing, on one surface, at one moment.
//
// Every number is a pointer, and that is the whole reason this type exists
// rather than the diff being written against a store row directly: absence and
// zero are different facts, and the difference decides which kind is emitted.
//
// PriceKind is beside the pointer rather than inferred from it, because on this
// target a nil price is ambiguous three ways. «Цена договорная» is the seller
// saying something and is worth an event; a parse gap is our defect and is
// worth none; and a real number is neither. A diff that read only the pointer
// would report thirty listings "becoming negotiable" the first time Avito
// withheld its hydration state.
type Reading struct {
	ItemID  int64
	Surface string
	// TS is when the reading was taken, whole Unix seconds UTC. It becomes the
	// timestamp of every change this reading produces: a change is dated by
	// when it was observed, not by when it was computed.
	TS int64

	PriceKind   string // "exact" | "negotiable" | "absent"
	PriceMinor  *int64
	Currency    string
	DiscountPct *int64

	Title *string

	// The three promotion-ish booleans the snapshot row carries. Pointers for
	// the same reason as everything else here: the microdata path cannot see
	// any of them, and a false would be a claim where there was a silence.
	IsXL       *bool
	IsRemap    *bool
	IsReserved *bool

	// SortTSMs is Avito's own absolute epoch milliseconds, kept as sent.
	SortTSMs *int64
}

// Change is one thing that moved.
type Change struct {
	Kind   Kind
	ItemID int64
	TS     int64
	Field  string
	// Was and Now are TEXT because a title and a moderation reason have to fit
	// in the same pair of columns as a price.
	Was, Now  string
	Unit      Unit
	Currency  string
	HadBefore bool
	HasNow    bool
	// Confidence is 1.0 for a fact a page stated and 0.3 for a conclusion
	// drawn from something not being in a window. The series producer only
	// ever states facts, so everything here is 1.0; the window producer is
	// where the other value appears.
	Confidence float64
}

// ErrIdentityMismatch is returned when the two readings are not of one thing.
var ErrIdentityMismatch = errors.New("track: the two readings are of different listings")

// Diff is every change between two readings of one listing, in a fixed order.
//
// The order is fixed because these rows are written into a table and read back
// by a person: two runs over the same pair must produce the same sequence, and
// a map iteration would not.
func Diff(before, after Reading) ([]Change, error) {
	if before.ItemID != after.ItemID {
		return nil, fmt.Errorf("%w: listing %d and listing %d",
			ErrIdentityMismatch, before.ItemID, after.ItemID)
	}
	// Two surfaces are not two samples of one series. They carry different
	// fields — mobile has no description at all, measured 2026-08-23 — so a
	// difference between them is a difference between two extractors and not
	// between two moments.
	if before.Surface != after.Surface {
		return nil, fmt.Errorf("%w: surfaces %q and %q",
			ErrIdentityMismatch, before.Surface, after.Surface)
	}

	var out []Change
	base := Change{ItemID: after.ItemID, TS: after.TS, Confidence: 1.0}

	out = appendPrice(out, base, before, after)
	out = appendDiscount(out, base, before, after)
	out = appendTitle(out, base, before, after)
	out = appendFlags(out, base, before, after)
	out = appendBump(out, base, before, after)
	return out, nil
}

// appendPrice is the three-valued half, and it is the one to read carefully.
//
//	exact -> exact           a move, or silence
//	exact -> negotiable      price-withdrawn: the seller said so
//	negotiable -> exact      price-appeared
//	anything -> absent       silence: we stopped reading it, and our own
//	                         blindness is not the market moving
//	absent -> anything       silence: there is nothing to compare against
func appendPrice(out []Change, base Change, before, after Reading) []Change {
	if before.PriceKind == "absent" || after.PriceKind == "absent" {
		return out
	}
	switch {
	case before.PriceKind == "exact" && after.PriceKind == "negotiable":
		c := base
		c.Kind, c.Field = KindPriceWithdrawn, "price"
		c.Unit, c.Currency = UnitMinor, before.Currency
		c.HadBefore, c.HasNow = true, false
		if before.PriceMinor != nil {
			c.Was = strconv.FormatInt(*before.PriceMinor, 10)
		}
		return append(out, c)

	case before.PriceKind == "negotiable" && after.PriceKind == "exact":
		c := base
		c.Kind, c.Field = KindPriceAppeared, "price"
		c.Unit, c.Currency = UnitMinor, after.Currency
		c.HadBefore, c.HasNow = false, true
		if after.PriceMinor != nil {
			c.Now = strconv.FormatInt(*after.PriceMinor, 10)
		}
		return append(out, c)

	case before.PriceKind == "exact" && after.PriceKind == "exact":
		if before.PriceMinor == nil || after.PriceMinor == nil ||
			*before.PriceMinor == *after.PriceMinor {
			return out
		}
		c := base
		c.Kind, c.Field = KindPriceChanged, "price"
		c.Unit, c.Currency = UnitMinor, after.Currency
		c.HadBefore, c.HasNow = true, true
		c.Was = strconv.FormatInt(*before.PriceMinor, 10)
		c.Now = strconv.FormatInt(*after.PriceMinor, 10)
		return append(out, c)
	}
	return out
}

func appendDiscount(out []Change, base Change, before, after Reading) []Change {
	if before.DiscountPct == nil || after.DiscountPct == nil ||
		*before.DiscountPct == *after.DiscountPct {
		return out
	}
	c := base
	c.Kind, c.Field, c.Unit = KindDiscountChanged, "discount_pct", UnitPercent
	c.HadBefore, c.HasNow = true, true
	c.Was = strconv.FormatInt(*before.DiscountPct, 10)
	c.Now = strconv.FormatInt(*after.DiscountPct, 10)
	return append(out, c)
}

func appendTitle(out []Change, base Change, before, after Reading) []Change {
	if before.Title == nil || after.Title == nil || *before.Title == *after.Title {
		return out
	}
	c := base
	c.Kind, c.Field, c.Unit = KindTitleChanged, "title", UnitText
	c.HadBefore, c.HasNow = true, true
	c.Was, c.Now = *before.Title, *after.Title
	return append(out, c)
}

// appendFlags covers the three booleans a snapshot row carries.
//
// A nil on either side is silence, and that is load-bearing rather than tidy:
// the microdata path cannot see any of the three, so a nil read as false would
// report thirty listings un-promoting themselves the first time the extractor
// fell back.
//
// IsXL and IsRemap are both promotion, and either moving emits one
// promotion-changed. Two kinds for "the seller paid for a bigger card" and "the
// seller paid for the promoted block" would be two rules a person has to
// remember to write.
func appendFlags(out []Change, base Change, before, after Reading) []Change {
	flag := func(out []Change, kind Kind, field string, was, now *bool) []Change {
		if was == nil || now == nil || *was == *now {
			return out
		}
		c := base
		c.Kind, c.Field, c.Unit = kind, field, UnitNone
		c.HadBefore, c.HasNow = true, true
		c.Was, c.Now = strconv.FormatBool(*was), strconv.FormatBool(*now)
		return append(out, c)
	}
	out = flag(out, KindPromotionChanged, "is_xl", before.IsXL, after.IsXL)
	out = flag(out, KindPromotionChanged, "is_remap", before.IsRemap, after.IsRemap)
	out = flag(out, KindReservedChanged, "is_reserved", before.IsReserved, after.IsReserved)
	return out
}

// appendBump reports a resurfacing.
//
// The field is milliseconds because that is how Avito sends it and how the
// column stores it, but the unit is seconds: the number a person sets a rule on
// is "bumped in the last hour", and a threshold in milliseconds is three extra
// zeroes to get wrong.
func appendBump(out []Change, base Change, before, after Reading) []Change {
	if before.SortTSMs == nil || after.SortTSMs == nil ||
		*before.SortTSMs == *after.SortTSMs {
		return out
	}
	c := base
	c.Kind, c.Field, c.Unit = KindListingBumped, "sort_ts_ms", UnitSeconds
	c.HadBefore, c.HasNow = true, true
	c.Was = strconv.FormatInt(*before.SortTSMs/1000, 10)
	c.Now = strconv.FormatInt(*after.SortTSMs/1000, 10)
	return append(out, c)
}

// PercentChange is the move from was to now as a percentage of was.
//
// The bool is false when there is no percentage to report. A rise from zero is
// not a hundred percent and not infinity: it is an appearance, and reporting it
// as either is a lie a threshold rule would then act on.
func PercentChange(was, now int64) (float64, bool) {
	if was == 0 {
		return 0, false
	}
	return float64(now-was) / float64(was) * 100, true
}
