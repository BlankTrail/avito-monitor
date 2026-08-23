// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"testing"
)

func TestRoublesToMinor(t *testing.T) {
	cases := []struct {
		name    string
		in      int64
		want    int64
		wantErr bool
	}{
		{"the measured listing price", 26490, 2649000, false},
		{"zero is a price, not an absence", 0, 0, false},
		{"one rouble", 1, 100, false},
		{"a plausible flat", 12_500_000, 1_250_000_000, false},
		// The guard is not decoration: int64 kopecks overflow at about
		// 92 quadrillion roubles, and a payload that ever states a price in
		// kopecks would sail past it while looking like a number.
		{"overflow is refused, not wrapped", 1 << 60, 0, true},
		{"a negative price is not a price", -1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RoublesToMinor(c.in)
			if c.wantErr {
				if !errors.Is(err, ErrPriceOverflow) {
					t.Fatalf("RoublesToMinor(%d) error = %v, want ErrPriceOverflow", c.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RoublesToMinor(%d): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("RoublesToMinor(%d) = %d, want %d — Avito states whole "+
					"roubles and this is the only place that fact is encoded", c.in, got, c.want)
			}
		})
	}
}

func TestDigitsToMinor(t *testing.T) {
	cases := []struct {
		name  string
		label string
		want  int64
		ok    bool
	}{
		{"the measured mobile label", "49 490 ₽", 4949000, true},
		{"a non-breaking space is what the site actually sends", "31 490 ₽", 3149000, true},
		// unicode.IsNumber accepts category No, which includes U+00B2. Walking
		// with it turns 1200 into 12002, and per-square-metre pricing is
		// ordinary on Avito's property categories.
		{"the superscript trap", "1 200 ₽ за м²", 120000, true},
		{"a negotiable price yields nothing", "Цена договорная", 0, false},
		{"an empty label yields nothing", "", 0, false},
		{"no digits at all", "₽", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DigitsToMinor(c.label)
			if ok != c.ok {
				t.Fatalf("DigitsToMinor(%q) ok = %v, want %v", c.label, ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("DigitsToMinor(%q) = %d, want %d", c.label, got, c.want)
			}
		})
	}
}
