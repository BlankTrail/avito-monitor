// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

// fieldSep separates the fields going into a fingerprint.
//
// Without it a title ending in "A" beside a label " B" hashes the same as a
// title "A B" beside an empty label, and two different listings would share a
// fingerprint — which means one of them silently stops having a history. Unit
// separator, because it is the one byte no field on this site can contain.
const fieldSep = 0x1f

// Fingerprint is what decides whether a reading is worth a row.
//
// listing_snapshots is written only when this moves, plus one anchor row per
// AnchorEvery, which is what makes the file grow sublinearly in the age of a
// watch rather than linearly in the number of passes. It follows that this
// function decides what history exists: a field left out of it can change a
// hundred times and leave no trace anywhere.
//
// The input is built by hand, field by field, in a fixed order. Not a struct
// hash and not JSON: a map iteration or an added field would silently change
// every fingerprint in the database, and every listing in it would then appear
// to have changed on the day of the upgrade.
//
// Position is deliberately absent, and avito.Listing has no field for it.
// Rank belongs to the watch and not to the listing — a listing pushed down by
// arrivals has not changed — and including it would make the series grow with
// churn instead of with events.
func Fingerprint(l avito.Listing) string {
	var b strings.Builder
	write := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
			b.WriteByte(fieldSep)
		}
	}
	// The kind goes in whether or not there is a number, so that a page which
	// stopped reporting a price does not read as unchanged. The three kinds
	// are three different statements: the seller's «Цена договорная», a real
	// number, and our own failure to read one.
	write(string(l.PriceKind))
	if l.PriceKind == avito.PriceExact {
		write(strconv.FormatInt(l.Price.Minor, 10), l.Price.Currency)
	}
	// The site's own rendering, in the fingerprint as well as in the row: a
	// parsed integer that stops agreeing with the string it came from is how a
	// change of unit would be noticed, and it cannot be noticed if the string
	// never moves the fingerprint.
	write(l.PriceLabel)
	write(l.Title)
	write(l.URLPath)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Retention is every threshold this package thins by.
//
// They travel together as a struct rather than as constants because the spec
// says both body bounds are settings and neither may be unset; the rest come
// along so there is one place to look and one thing to persist.
type Retention struct {
	// Snapshot thinning: one row per period, oldest periods coarsest.
	SnapshotDailyAfter  time.Duration
	SnapshotWeeklyAfter time.Duration

	// Pass thinning. Only ok and empty passes are ever thinned — see
	// ThinPasses for why the rule is not "not ok".
	PassHourlyAfter time.Duration
	PassDailyAfter  time.Duration

	// The body archive is bounded twice. An age rule without a size rule is
	// unbounded on a broken install, which is exactly when it grows fastest.
	BodyMaxAge   time.Duration
	BodyMaxBytes int64

	// AnchorEvery is how often a row is written for a listing that has not
	// changed, so that "unchanged since March" and "unread since March" are
	// different sentences in the data.
	AnchorEvery time.Duration
}

func DefaultRetention() Retention {
	return Retention{
		SnapshotDailyAfter:  30 * 24 * time.Hour,
		SnapshotWeeklyAfter: 365 * 24 * time.Hour,
		PassHourlyAfter:     7 * 24 * time.Hour,
		PassDailyAfter:      30 * 24 * time.Hour,
		BodyMaxAge:          30 * 24 * time.Hour,
		BodyMaxBytes:        500 << 20,
		AnchorEvery:         24 * time.Hour,
	}
}

// AnchorDue reports whether this listing is owed an anchor row.
//
// lastTS of zero means no row has ever been written for it, which is due by
// definition — the first reading of anything is a change from nothing.
func AnchorDue(lastTS, now int64, every time.Duration) bool {
	if lastTS == 0 {
		return true
	}
	return now-lastTS >= int64(every/time.Second)
}
