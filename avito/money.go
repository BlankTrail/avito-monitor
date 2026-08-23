// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"unicode"
)

// ErrPriceOverflow is returned by RoublesToMinor for a value that cannot be
// expressed in minor units, and for a negative one. Both are refusals rather
// than clamps: a wrong price that still sorts is worse than no price.
var ErrPriceOverflow = errors.New("avito: price out of range")

// Money is an integer number of minor units beside a currency. Never a float:
// a payload's kopecks must never be silently rounded away.
type Money struct {
	Minor    int64
	Currency string
}

// maxRoubles is the largest value that survives multiplication by 100.
const maxRoubles = (1<<63 - 1) / 100

// RoublesToMinor converts Avito's own unit into ours.
//
// This is the boundary the whole price history hangs off. Avito states
// priceDetailed.value in WHOLE ROUBLES. Wildberries — whose store, rules and
// exporters this project lifts — states kopecks. Getting it wrong by a factor
// of a hundred produces numbers that sort correctly and render plausibly, so
// nothing downstream can catch it; it is caught here or not at all.
func RoublesToMinor(roubles int64) (int64, error) {
	if roubles < 0 || roubles > maxRoubles {
		return 0, ErrPriceOverflow
	}
	return roubles * 100, nil
}

// DigitsToMinor reads a rendered price label — "49 490 ₽" — into minor units.
// It is what the mobile and item surfaces use, because neither carries a
// numeric price field; there the rendered label IS the reading.
//
// The walk is unicode.IsDigit and deliberately not unicode.IsNumber. IsNumber
// accepts category No, which includes U+00B2, so "1 200 ₽ за м²" would read as
// 12002 rather than 1200 — and per-square-metre pricing is ordinary on Avito's
// property categories, so the bug would be common and quiet.
func DigitsToMinor(label string) (int64, bool) {
	var roubles int64
	seen := false
	for _, r := range label {
		if !unicode.IsDigit(r) {
			continue
		}
		if roubles > maxRoubles/10 {
			return 0, false
		}
		roubles = roubles*10 + int64(r-'0')
		seen = true
	}
	if !seen {
		return 0, false
	}
	minor, err := RoublesToMinor(roubles)
	if err != nil {
		return 0, false
	}
	return minor, true
}

// DigitsToInt reads the digits of a rendered label as a plain integer: an id,
// a view count. Same unicode.IsDigit rule as the money walk, and the same
// reason for it.
func DigitsToInt(label string) (int64, bool) {
	var n int64
	seen := false
	for _, r := range label {
		if !unicode.IsDigit(r) {
			continue
		}
		if n > (1<<62)/10 {
			return 0, false
		}
		n = n*10 + int64(r-'0')
		seen = true
	}
	return n, seen && n > 0
}
