// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aListing() avito.Listing {
	return avito.Listing{
		ItemID:     7800000001,
		Title:      "iPhone 15, 128 ГБ",
		URLPath:    "/m/x_7800000001",
		PriceKind:  avito.PriceExact,
		Price:      avito.Money{Minor: 4000000, Currency: "RUB"},
		PriceLabel: "40 000 ₽",
	}
}

// TestFingerprintMovesWhenSomethingWorthARowMoves.
//
// A snapshot is written only when the fingerprint moves, plus one anchor per
// period. That is what makes growth sublinear in the age of a watch, and it
// means this function decides what history exists: a field outside it can
// change a hundred times and leave no trace.
func TestFingerprintMovesWhenSomethingWorthARowMoves(t *testing.T) {
	base := Fingerprint(aListing())

	cases := []struct {
		name string
		edit func(*avito.Listing)
		want bool // want a different fingerprint
	}{
		{"price", func(l *avito.Listing) { l.Price.Minor = 3900000 }, true},
		{"price kind", func(l *avito.Listing) { l.PriceKind = avito.PriceNegotiable }, true},
		{"title", func(l *avito.Listing) { l.Title = "iPhone 15, 256 ГБ" }, true},
		{"price label", func(l *avito.Listing) { l.PriceLabel = "40000 руб." }, true},
		// Nothing moved, so nothing is worth a row. Rank is not in this list
		// because avito.Listing has no position field at all: rank belongs to
		// the watch that saw the listing, and a listing pushed down by
		// arrivals has not changed.
		{"nothing", func(l *avito.Listing) {}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := aListing()
			c.edit(&l)
			got := Fingerprint(l) != base
			if got != c.want {
				t.Errorf("fingerprint moved = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFingerprintIsStableAcrossRuns. Two copies of one database must thin
// identically, and a map iteration or a pointer address in the hash input
// would make them diverge.
func TestFingerprintIsStableAcrossRuns(t *testing.T) {
	for i := 0; i < 50; i++ {
		if Fingerprint(aListing()) != Fingerprint(aListing()) {
			t.Fatal("Fingerprint is not deterministic")
		}
	}
}

// TestFingerprintSeparatesFieldsThatCouldRunTogether.
//
// Without a separator, a title ending in "A" beside a label "B" hashes the same
// as a title "AB" beside an empty label. That is not a hypothetical on a site
// whose price label is free text: it is how two different listings would come
// to share a fingerprint and one of them would stop having a history.
func TestFingerprintSeparatesFieldsThatCouldRunTogether(t *testing.T) {
	a := aListing()
	a.Title, a.PriceLabel = "iPhone", " 15"

	b := aListing()
	b.Title, b.PriceLabel = "iPhone 15", ""

	if Fingerprint(a) == Fingerprint(b) {
		t.Error("two different listings share a fingerprint; the field separator " +
			"is not doing its job")
	}
}

// TestAnAbsentPriceDoesNotBorrowTheLastExactOne.
//
// price_kind is three-valued because a null price is ambiguous between the
// seller's own «Цена договорная» and our failure to read one. The fingerprint
// must carry the kind even when there is no number, or a page that stopped
// reporting a price would look unchanged.
func TestAnAbsentPriceDoesNotBorrowTheLastExactOne(t *testing.T) {
	exact := aListing()

	absent := aListing()
	absent.PriceKind = avito.PriceAbsent
	absent.Price = avito.Money{}
	absent.PriceLabel = ""

	negotiable := aListing()
	negotiable.PriceKind = avito.PriceNegotiable
	negotiable.Price = avito.Money{}
	negotiable.PriceLabel = "Цена договорная"

	seen := map[string]string{}
	for name, l := range map[string]avito.Listing{
		"exact": exact, "absent": absent, "negotiable": negotiable,
	} {
		fp := Fingerprint(l)
		if other, dup := seen[fp]; dup {
			t.Errorf("%s and %s share a fingerprint; the three price kinds are "+
				"three different statements", name, other)
		}
		seen[fp] = name
	}
}

// TestAnchorDue. An anchor is what makes "unchanged since March" and "unread
// since March" different sentences. Without one, a listing whose price has not
// moved in six weeks is indistinguishable from a watch that stopped running.
func TestAnchorDue(t *testing.T) {
	const day = 86400
	cases := []struct {
		name        string
		lastTS, now int64
		every       time.Duration
		want        bool
	}{
		{"never written", 0, 1000, 24 * time.Hour, true},
		{"written just now", 1000, 1000 + 60, 24 * time.Hour, false},
		{"a day later", 1000, 1000 + day, 24 * time.Hour, true},
		{"an hour before it is due", 1000, 1000 + day - 3600, 24 * time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AnchorDue(c.lastTS, c.now, c.every); got != c.want {
				t.Errorf("AnchorDue(%d, %d, %v) = %v, want %v",
					c.lastTS, c.now, c.every, got, c.want)
			}
		})
	}
}

// TestDefaultRetentionIsTheThresholdsTheSpecNames.
func TestDefaultRetentionIsTheThresholdsTheSpecNames(t *testing.T) {
	r := DefaultRetention()
	for _, c := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"SnapshotDailyAfter", r.SnapshotDailyAfter, 30 * 24 * time.Hour},
		{"SnapshotWeeklyAfter", r.SnapshotWeeklyAfter, 365 * 24 * time.Hour},
		{"PassHourlyAfter", r.PassHourlyAfter, 7 * 24 * time.Hour},
		{"PassDailyAfter", r.PassDailyAfter, 30 * 24 * time.Hour},
		{"BodyMaxAge", r.BodyMaxAge, 30 * 24 * time.Hour},
		{"AnchorEvery", r.AnchorEvery, 24 * time.Hour},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if r.BodyMaxBytes != 500<<20 {
		t.Errorf("BodyMaxBytes = %d, want 500 MB", r.BodyMaxBytes)
	}
}
