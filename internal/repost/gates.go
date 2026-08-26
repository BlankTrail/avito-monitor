// SPDX-License-Identifier: AGPL-3.0-or-later

// Package repost decides whether one listing is a seller re-publishing
// something they had already posted.
//
// Advisory, gated, and biased towards missing. The asymmetry is the whole
// design and it is never traded away: a missed link announces a repost as a new
// listing, which is the product's headline feature working. A wrong link
// silently withholds an alert and looks like data rather than a bug. When in
// doubt, do not link.
//
// Everything here is a pure function of one candidate pair, so every refusal is
// testable without a database and every refusal can be shown to a person. The
// panel prints these reasons verbatim, because «почему мне не сказали про это
// объявление» is the question this package exists to be able to answer.
package repost

import (
	"fmt"
	"time"
)

// Listing is one side of a candidate pair, as the store reads it.
//
// A struct of its own rather than store's row or avito's Listing: this package
// must not learn a schema, and the fields it needs are a strict subset of both.
type Listing struct {
	ItemID int64

	// SellerKey and SellerQuality travel together, always. A key whose
	// provenance was lost is not a key — see the seller gate.
	SellerKey     string
	SellerQuality string // "slug", "profile", "heuristic", ""

	RepostKey      string
	Title          string // already folded by avito.FoldTitle
	DescriptionSHA string
	ImageKey       string

	// PriceMinor is 0 when no price was read. Zero is not a price, and the
	// ladder must never treat it as one.
	PriceMinor int64

	Lat, Lng      *float64
	CoordsPrecise bool

	CategoryRootID int64

	State       string // "active", "left-window", "vanished", "closed"
	FirstSeenAt int64
	LastSeenAt  int64
}

// Gate names one refusal.
type Gate string

const (
	GatePinned    Gate = "pinned"
	GateCoexisted Gate = "coexisted"
	GateNotEnded  Gate = "not-ended"
	GateWindow    Gate = "window"
	GateCategory  Gate = "category"
	GateSeller    Gate = "seller"
)

// AllGates is every gate, in the order Gates applies them.
//
// Written out rather than derived, because a test walks it to prove every gate
// has Russian wording — and a gate added to the code and forgotten here would
// make that test pass by not looking.
func AllGates() []Gate {
	return []Gate{
		GatePinned, GateCoexisted, GateNotEnded,
		GateWindow, GateCategory, GateSeller,
	}
}

// GateResult is what the gates concluded.
//
// Passed and Undetermined are not opposites. Undetermined means the seller gate
// could not decide — which is a pass with a ceiling on it, not a refusal: the
// ladder may propose such a pair and may never auto-link it.
type GateResult struct {
	Passed       bool
	Undetermined bool
	Refused      Gate
	Why          string // Russian, shown verbatim on the panel
}

func refuse(g Gate, why string) GateResult {
	return GateResult{Refused: g, Why: why}
}

// Gates runs the five hard gates over a candidate pair.
//
// Order matters and it is the spec's: cheapest and most decisive first. The
// seller gate is last because it is the only one with three answers, and
// running it earlier would mean carrying its ceiling through four other
// refusals that would have made it moot.
//
// newer is the arrival, older the listing it might be succeeding. windowSec is
// the watch's own repost window.
func Gates(newer, older Listing, pinned bool, windowSec int64) GateResult {
	// 1. A pair a person has said are not the same thing can never be
	// re-linked, by any future change to the ladder. First, so that no later
	// gate's wording can ever be what a pinned pair is refused with.
	if pinned {
		return refuse(GatePinned,
			"вы уже сказали, что это разные объявления")
	}

	// 2. No observed co-existence. If both were ever alive at once they are two
	// things, not one thing twice — and this reads the listings' own permanent
	// observation interval rather than pass membership, which gets thinned.
	//
	// Strictly after: intervals that merely touch mean both were observed at
	// that second.
	if newer.FirstSeenAt <= older.LastSeenAt {
		return refuse(GateCoexisted, fmt.Sprintf(
			"объявления существовали одновременно: новое впервые увидели %s, "+
				"а старое ещё было на месте %s",
			at(newer.FirstSeenAt), at(older.LastSeenAt)))
	}

	// 3. The predecessor must have ended. «left-window» is too weak to build a
	// merge on: it means we stopped seeing it on page one, which happens to
	// every listing on a busy query within the hour.
	if older.State != "vanished" && older.State != "closed" {
		return refuse(GateNotEnded, fmt.Sprintf(
			"старое объявление не закончилось: его состояние — %s",
			stateWord(older.State)))
	}

	// 4a. Inside the repost window. Inclusive at the bound: a person who set
	// thirty days meant «and thirty days».
	if gap := newer.FirstSeenAt - older.LastSeenAt; gap > windowSec {
		return refuse(GateWindow, fmt.Sprintf(
			"между объявлениями %d дней, а окно перевыкладки — %d",
			gap/86400, windowSec/86400))
	}

	// 4b. The root category. A zero on either side is our silence and not a
	// mismatch: the mobile surface carries no category at all, and refusing on
	// it would reject that whole population for a reason that is ours.
	if older.CategoryRootID != 0 && newer.CategoryRootID != 0 &&
		older.CategoryRootID != newer.CategoryRootID {
		return refuse(GateCategory,
			"объявления в разных разделах сайта")
	}

	// 5. The seller, which is a gate and not a score, and which has three
	// answers rather than two.
	//
	// A heuristic key is never evidence: the schema records key_quality
	// precisely so this line can be written. Inventing an identity that
	// silently merges two people is worse than declining to identify either.
	oldKey, newKey := trusted(older), trusted(newer)
	switch {
	case oldKey == "" || newKey == "":
		return GateResult{
			Passed: true, Undetermined: true,
			Why: "продавец известен не у обоих объявлений: связать можно " +
				"только предположительно, и такое предположение не отменяет " +
				"обычного сообщения о новом объявлении",
		}
	case oldKey != newKey:
		// Not a doubt. A listing copied verbatim by somebody else is a scam
		// clone, and that is precisely the thing a person must be told about.
		return refuse(GateSeller,
			"объявления от разных продавцов — это не перевыкладка, "+
				"а чужая копия")
	}
	return GateResult{Passed: true}
}

// trusted is the seller key this gate is willing to compare, or empty.
func trusted(l Listing) string {
	if l.SellerQuality != "slug" && l.SellerQuality != "profile" {
		return ""
	}
	return l.SellerKey
}

// stateWord is a listing state in the words the panel uses elsewhere.
func stateWord(state string) string {
	switch state {
	case "active":
		return "ещё в выдаче"
	case "left-window":
		return "ушло из отслеживаемой части выдачи"
	case "vanished":
		return "пропало"
	case "closed":
		return "снято с публикации"
	}
	return state
}

// at renders a moment the way the panel does.
//
// UTC and to the minute. The panel shows every other time this way, and a
// refusal that spelled its dates differently from the screen around it would
// read as coming from somewhere else.
func at(ts int64) string {
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04")
}
