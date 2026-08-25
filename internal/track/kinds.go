// SPDX-License-Identifier: AGPL-3.0-or-later

// Package track turns two readings of one thing into named changes.
//
// It knows nothing about the database, the network or the clock. A reading
// carries its own timestamp and a change is dated by when it was observed
// rather than by when it was computed, so the same two readings differenced
// twice give the same answer twice.
package track

// Kind is what moved. The catalogue is closed: these are what M1's three
// producers can write, and Kinds() offers exactly them to the rule editor.
type Kind string

const (
	// The window producer. These come from a set difference against the
	// previous window, computed inside the pass transaction.
	KindListingAppeared   Kind = "listing-appeared"
	KindListingReturned   Kind = "listing-returned"
	KindListingLeftWindow Kind = "listing-left-window"
	KindListingVanished   Kind = "listing-vanished"
	KindListingClosed     Kind = "listing-closed"
	KindListingReposted   Kind = "listing-reposted"
	KindRepostRetracted   Kind = "repost-retracted"

	// The series producer. These come from two snapshots of one listing.
	KindPriceChanged    Kind = "price-changed"
	KindPriceAppeared   Kind = "price-appeared"
	KindPriceWithdrawn  Kind = "price-withdrawn"
	KindDiscountChanged Kind = "discount-changed"
	KindTitleChanged    Kind = "title-changed"
	// is_xl and is_remap: the seller paid for a larger card or for the
	// promoted block. Worth telling somebody about, and emphatically not a
	// price move.
	KindPromotionChanged Kind = "promotion-changed"
	KindReservedChanged  Kind = "reserved-changed"
	// sortTimeStamp is a bump time and not a creation time, so a jump in it
	// with nothing else moving is the seller paying to resurface — its own
	// kind rather than a new listing.
	KindListingBumped Kind = "listing-bumped"

	// The transport producer: the collector observing itself rather than
	// comparing two readings. These are changes like any other so that a
	// person can write a rule on them — "tell me when my install is being
	// throttled" is a real thing to want, and it is how they learn before the
	// data quietly stops arriving.
	KindExtractionFellBack    Kind = "extraction-fell-back"
	KindTransportBlocked      Kind = "transport-blocked"
	KindCollectorShapeUnknown Kind = "collector-shape-unknown"
	KindCollectorSilent       Kind = "collector-silent"
	KindWorkerPanicked        Kind = "worker-panicked"
	KindWatchUndersampled     Kind = "watch-undersampled"
	KindWatchTooBroad         Kind = "watch-too-broad"
)

// Kinds is every kind this build can emit, in the order a person reads them.
//
// Written out rather than derived from a map, because a map's order changes
// between runs and this list is what the rule editor's dropdown shows.
func Kinds() []Kind {
	return []Kind{
		KindListingAppeared, KindListingReturned, KindListingLeftWindow,
		KindListingVanished, KindListingClosed,
		KindListingReposted, KindRepostRetracted,

		KindPriceChanged, KindPriceAppeared, KindPriceWithdrawn,
		KindDiscountChanged,
		KindTitleChanged, KindPromotionChanged, KindReservedChanged,
		KindListingBumped,

		KindExtractionFellBack, KindTransportBlocked, KindCollectorShapeUnknown,
		KindCollectorSilent, KindWorkerPanicked, KindWatchUndersampled,
		KindWatchTooBroad,
	}
}

// Unit says how to read Change.Was and Change.Now, which are TEXT because a
// title and a moderation reason have to fit in the same pair of columns as a
// price.
type Unit string

const (
	UnitNone    Unit = ""
	UnitMinor   Unit = "minor"   // integer minor units of Change.Currency
	UnitPercent Unit = "percent" // whole percentage points
	UnitCount   Unit = "count"
	UnitText    Unit = "text"
	UnitSeconds Unit = "seconds"
)

// kindLabels is what a change kind is called on a screen.
//
// Beside Kinds() and not in the panel, because the two are one decision: a kind
// added to the catalogue and forgotten here would appear in the rule editor's
// dropdown as «watch-undersampled», which tells a Russian-speaking person
// nothing at all. A test walks Kinds() and refuses a kind with no label.
//
// These are the noun forms — what the rule watches for — and not the sentences
// internal/telegram builds, which describe one particular change and need the
// change to do it.
var kindLabels = map[Kind]string{
	KindListingAppeared:   "появилось новое объявление",
	KindListingReturned:   "объявление вернулось в выдачу",
	KindListingLeftWindow: "объявление ушло из отслеживаемой части выдачи",
	KindListingVanished:   "объявление пропало",
	KindListingClosed:     "объявление снято с публикации",
	KindListingReposted:   "объявление выложено заново",
	KindRepostRetracted:   "связь между объявлениями отменена",

	KindPriceChanged:     "цена изменилась",
	KindPriceAppeared:    "у объявления появилась цена",
	KindPriceWithdrawn:   "цену убрали",
	KindDiscountChanged:  "скидка изменилась",
	KindTitleChanged:     "заголовок изменён",
	KindPromotionChanged: "продвижение включили или выключили",
	KindReservedChanged:  "бронь появилась или снялась",
	KindListingBumped:    "объявление подняли в выдаче",

	KindExtractionFellBack:    "разбор пошёл по запасному пути",
	KindTransportBlocked:      "сайт перестал отвечать",
	KindCollectorShapeUnknown: "мы перестали понимать разметку",
	KindCollectorSilent:       "наблюдение молчит",
	KindWorkerPanicked:        "сборщик упал",
	KindWatchUndersampled:     "наблюдение опрашивается слишком редко",
	KindWatchTooBroad:         "наблюдение слишком широкое",
}

// Label is what a kind is called on a screen, falling back to the key so a kind
// added without a label renders as something visible rather than as a blank
// option nobody can pick.
func Label(k Kind) string {
	if s, ok := kindLabels[k]; ok {
		return s
	}
	return string(k)
}
