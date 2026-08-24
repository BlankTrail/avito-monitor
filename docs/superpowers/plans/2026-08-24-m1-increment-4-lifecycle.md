# M1 Increment 4 — Lifecycle and changes

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn stored passes into named changes, and refuse to name one when the evidence does not support it.

**Architecture:** Two pure packages and one producer inside the pass transaction. `internal/track` diffs two readings into `Change` values with an all-pointers discipline, so absence and zero stay different facts. `internal/lifecycle` answers three questions about a pass — is it admissible as evidence, is it comparable with its predecessor, and is this absence explained by displacement — with no clock and no database. `internal/store` then writes the window differences inside `SavePass`, because a set difference against the previous window must not be observed half-written, and the series differences out of band from two snapshots.

**Tech Stack:** Go 1.26, stdlib only. `internal/track` and `internal/lifecycle` import nothing outside `avito`.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md` — §Identity and lifecycle (line 258), §Events and rules (line 526), the `changes` and `watch_members` DDL, increment 4 (line 1505).

**Measurements this plan argues from, and they are not optional reading:**

- `docs/measurements/2026-08-24-identity-decides-the-result-set.md` — **in direct mode the result set is a function of `srv_id`, exactly.** Six passes over two identities: 100% overlap within an identity, 23% across. This adds a gate the spec does not have, and Task 4 is that gate.
- `docs/measurements/2026-08-23-soak.md` — a 429 is a captcha, a 403 heals with time, a soft block is a stuck endpoint. All three are transport verdicts this increment must treat as *not evidence*.

## Global Constraints

- **Go 1.26, no CGO.** `go test -race` does not run on this machine — the race detector needs cgo and there is no gcc on PATH. Run `go test ./...`; `-race` is CI's job.
- **`internal/track` and `internal/lifecycle` are pure.** No `net`, no `net/http`, no `database/sql`, no `time.Now`. A clock is a parameter. Both get a test that proves it, in the shape of `avito`'s `TestPackageReachesNoNetwork`.
- **All numbers in a `Reading` are pointers.** Absence and zero are different facts and the difference decides which kind is emitted. This is the single most load-bearing line in the package: «Цена договорная» and a parse gap both give a nil price, and `price_kind` is what separates them.
- **Money is integer minor units beside a currency, never a float.**
- **`changes.was` and `changes.now` are TEXT.** An integer pair has nowhere to put a title or a moderation reason, and widening the busiest table later is the migration worth avoiding by writing one word differently today.
- **Only the kinds M1 can emit exist in the catalogue.** A rule on a kind nothing emits never fires, and a person whose rule never fires concludes nothing is changing.
- **Comments carry the reason, not the restatement.**

---

## File structure

| file | responsibility |
|---|---|
| `internal/track/kinds.go` | `Kind`, `Unit`, `Kinds()` — the catalogue and nothing else |
| `internal/track/track.go` | `Reading`, `Change`, `Diff`, `PercentChange`, `ErrIdentityMismatch` |
| `internal/track/purity_test.go` | the package imports nothing it should not |
| `internal/lifecycle/evidence.go` | `Admissible` — is this pass evidence that something is gone |
| `internal/lifecycle/compare.go` | `Comparable` — may these two passes be differenced at all |
| `internal/lifecycle/absence.go` | displacement accounting and the two gates |
| `internal/lifecycle/purity_test.go` | the same guard |
| `internal/store/evidential.go` | the stored `evidential` column, computed from `lifecycle` |
| `internal/store/window.go` | the window producer, inside the pass transaction |
| `internal/store/series.go` | the series producer, out of band over two snapshots |
| `internal/store/changes.go` | writing and reading `changes` |
| `cmd/avitomon/changes.go` | `avitomon changes` — what the increment demonstrates |

---

## Task 1: The catalogue

**Files:**
- Create: `internal/track/kinds.go`, `internal/track/kinds_test.go`, `internal/track/purity_test.go`

**Interfaces:**
- Produces: `type Kind string` with the eleven M1 constants; `func Kinds() []Kind`; `type Unit string` with `UnitMinor`, `UnitPercent`, `UnitText`, `UnitCount`, `UnitNone`.

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package track

import "testing"

// TestKindsOffersEverythingThisBuildCanEmitAndNothingElse.
//
// A rule written on a kind nothing can emit never fires, and a person whose
// rule never fires concludes nothing is changing rather than that they picked a
// dead kind. So the catalogue is what the panel offers, and it must equal what
// the producers actually write.
func TestKindsOffersEverythingThisBuildCanEmitAndNothingElse(t *testing.T) {
	want := []Kind{
		// The window producer, inside the pass transaction.
		KindListingAppeared, KindListingReturned, KindListingLeftWindow,
		KindListingVanished, KindListingClosed,
		KindListingReposted, KindRepostRetracted,
		// The series producer, out of band over two snapshots.
		KindPriceChanged, KindPriceAppeared, KindPriceWithdrawn,
		KindDiscountChanged,
		KindTitleChanged, KindPromotionChanged, KindReservedChanged,
		KindListingBumped,
		// The transport producer: the collector observing itself.
		KindExtractionFellBack, KindTransportBlocked, KindCollectorShapeUnknown,
		KindCollectorSilent, KindWorkerPanicked, KindWatchUndersampled,
		KindWatchTooBroad,
	}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() has %d entries, want %d", len(got), len(want))
	}
	seen := map[Kind]bool{}
	for _, k := range got {
		if seen[k] {
			t.Errorf("%q appears twice", k)
		}
		seen[k] = true
	}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("%q is missing from Kinds()", k)
		}
	}
}

// TestEveryKindIsSpelledInKebabCase. The kind is written into a TEXT column, is
// matched by rules a person typed, and appears in a Telegram message. One
// spelling convention, enforced, is cheaper than three that drifted.
func TestEveryKindIsSpelledInKebabCase(t *testing.T) {
	for _, k := range Kinds() {
		s := string(k)
		if s == "" {
			t.Error("an empty kind")
			continue
		}
		for i, r := range s {
			ok := (r >= 'a' && r <= 'z') || r == '-'
			if !ok {
				t.Errorf("%q has %q at %d; kinds are lower-case kebab", k, r, i)
				break
			}
		}
		if s[0] == '-' || s[len(s)-1] == '-' {
			t.Errorf("%q starts or ends with a dash", k)
		}
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/track/ -run Kinds -v
```

Expected: FAIL to build — `undefined: Kind`.

- [x] **Step 3: Write `kinds.go`**

```go
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
// It is written out rather than derived from a map, because a map's order
// changes between runs and this list is what the rule editor's dropdown shows.
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
```

- [x] **Step 4: Write the purity guard**

`internal/track/purity_test.go`, modelled on `avito`'s network guard:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package track_test

import (
	"go/build"
	"strings"
	"testing"
)

// TestPackageIsPure. track diffs two readings and nothing else. An import of
// database/sql would make a change depend on what happened to be stored, and an
// import of time would let two runs over the same two readings disagree.
func TestPackageIsPure(t *testing.T) {
	banned := []string{"net", "net/http", "database/sql", "os", "time"}
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if imp == b || strings.HasPrefix(imp, b+"/") {
				t.Errorf("track imports %q; a change must be a function of its "+
					"two readings and of nothing else", imp)
			}
		}
	}
}
```

`time` is banned deliberately: a reading carries its own `TS`, and a producer that reached for the clock would date a change by when it was computed.

- [x] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/track/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: three PASS.

- [x] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/track/ && git commit -m "$(cat <<'MSG'
feat(track): the closed catalogue of what this build can emit

Twenty kinds across three producers, written out in reading order rather
than derived from a map whose order changes between runs — this list is
what the rule editor's dropdown shows.

The catalogue is closed on purpose. A rule written on a kind nothing can
emit never fires, and a person whose rule never fires concludes nothing
is changing rather than that they picked a dead kind.

The transport kinds are in it because "tell me when my install is being
throttled" is a real rule somebody writes, and it is how they find out
before the data quietly stops arriving.

The package imports neither a clock nor a database, and a test says so. A
reading carries its own timestamp: a change is dated by when it was
observed, not by when it was computed.

MSG
)"
```

---

## Task 2: `Reading`, `Change` and `Diff`

**Files:**
- Create: `internal/track/track.go`, `internal/track/track_test.go`

**Interfaces:**
- Consumes: `Kind`, `Unit` from Task 1.
- Produces:

```go
type Reading struct {
    ItemID     int64
    Surface    string
    TS         int64
    PriceKind  string   // "exact" | "negotiable" | "absent"
    PriceMinor *int64
    Currency   string
    DiscountPct *int64
    Title      *string
    IsXL, IsRemap, IsReserved *bool
    SortTSMs   *int64
}

type Change struct {
    Kind       Kind
    ItemID     int64
    TS         int64
    Field      string
    Was, Now   string
    Unit       Unit
    Currency   string
    HadBefore  bool
    HasNow     bool
    Confidence float64
}

var ErrIdentityMismatch = errors.New("track: the two readings are of different listings")
func Diff(before, after Reading) ([]Change, error)
func PercentChange(was, now int64) (float64, bool)
```

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package track

import (
	"errors"
	"testing"
)

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }
func boolp(v bool) *bool   { return &v }

func reading(ts int64) Reading {
	return Reading{
		ItemID: 7800000001, Surface: "mobile", TS: ts,
		PriceKind: "exact", PriceMinor: i64(4000000), Currency: "RUB",
		Title: str("iPhone 15, 128 ГБ"),
	}
}

func kindsOf(cs []Change) []Kind {
	out := make([]Kind, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Kind)
	}
	return out
}

func TestDiffReportsAPriceMove(t *testing.T) {
	before := reading(1000)
	after := reading(1300)
	after.PriceMinor = i64(3500000)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindPriceChanged {
		t.Fatalf("kinds = %v, want one price-changed", kindsOf(got))
	}
	c := got[0]
	if c.Was != "4000000" || c.Now != "3500000" {
		t.Errorf("was/now = %q/%q, want the minor units", c.Was, c.Now)
	}
	if c.Unit != UnitMinor || c.Currency != "RUB" {
		t.Errorf("unit=%q currency=%q; a number without its unit is not a price",
			c.Unit, c.Currency)
	}
	if c.TS != 1300 {
		t.Errorf("TS = %d, want the LATER reading's — a change is dated by when "+
			"it was observed", c.TS)
	}
}

func TestDiffSaysNothingWhenNothingMoved(t *testing.T) {
	got, err := Diff(reading(1000), reading(1300))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none", kindsOf(got))
	}
}

// TestTheThreePriceKindsAreThreeDifferentStatements is the whole reason
// PriceKind is on the reading and not inferred from a nil.
//
// «Цена договорная» is the seller saying something and is worth an event. A
// parse gap is our defect and is worth none. Both give a nil price, and a diff
// that could not tell them apart would report "this became negotiable" for
// thirty listings the first time Avito withheld the hydration state.
func TestTheThreePriceKindsAreThreeDifferentStatements(t *testing.T) {
	exact := reading(1000)

	negotiable := reading(1300)
	negotiable.PriceKind, negotiable.PriceMinor = "negotiable", nil

	absent := reading(1300)
	absent.PriceKind, absent.PriceMinor = "absent", nil

	t.Run("exact to negotiable is an event", func(t *testing.T) {
		got, err := Diff(exact, negotiable)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != KindPriceWithdrawn {
			t.Fatalf("kinds = %v, want one price-withdrawn", kindsOf(got))
		}
		if got[0].HasNow {
			t.Error("HasNow is true for a withdrawn price")
		}
	})

	t.Run("negotiable to exact is an event", func(t *testing.T) {
		got, err := Diff(negotiable, exact)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Kind != KindPriceAppeared {
			t.Fatalf("kinds = %v, want one price-appeared", kindsOf(got))
		}
	})

	t.Run("anything to absent is silence", func(t *testing.T) {
		got, err := Diff(exact, absent)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("kinds = %v, want none — we stopped reading the price, and "+
				"our own blindness is not the market moving", kindsOf(got))
		}
	})

	t.Run("absent to anything is silence", func(t *testing.T) {
		got, err := Diff(absent, exact)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("kinds = %v, want none — we could not see the old price, so "+
				"there is nothing to compare against", kindsOf(got))
		}
	})
}

// TestDiffRefusesTwoDifferentListings.
func TestDiffRefusesTwoDifferentListings(t *testing.T) {
	other := reading(1300)
	other.ItemID = 999
	if _, err := Diff(reading(1000), other); !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

// TestDiffRefusesTwoSurfaces. A desktop reading and a mobile one are not two
// samples of one series: they carry different fields, and mobile has no
// description at all.
func TestDiffRefusesTwoSurfaces(t *testing.T) {
	other := reading(1300)
	other.Surface = "desktop"
	if _, err := Diff(reading(1000), other); !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("err = %v, want ErrIdentityMismatch", err)
	}
}

func TestDiffReportsATitleEdit(t *testing.T) {
	after := reading(1300)
	after.Title = str("iPhone 15, 256 ГБ")

	got, err := Diff(reading(1000), after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindTitleChanged {
		t.Fatalf("kinds = %v, want one title-changed", kindsOf(got))
	}
	if got[0].Unit != UnitText {
		t.Errorf("unit = %q, want text", got[0].Unit)
	}
}

// TestABumpedSortTimeIsItsOwnKind. sortTimeStamp is a bump time and not a
// creation time, so a jump in it with nothing else moving means the seller
// paid to resurface — which is worth telling somebody about and is emphatically
// not a new listing.
func TestABumpedSortTimeIsItsOwnKind(t *testing.T) {
	before := reading(1000)
	before.SortTSMs = i64(1_700_000_000_000)
	after := reading(1300)
	after.SortTSMs = i64(1_700_000_600_000)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindListingBumped {
		t.Fatalf("kinds = %v, want one listing-bumped", kindsOf(got))
	}
}

// TestAPromotionIsItsOwnKindAndNotAPriceMove. The seller paid for a larger
// card; nothing about what they are selling changed.
func TestAPromotionIsItsOwnKindAndNotAPriceMove(t *testing.T) {
	before := reading(1000)
	before.IsXL = boolp(false)
	after := reading(1300)
	after.IsXL = boolp(true)

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindPromotionChanged {
		t.Fatalf("kinds = %v, want one promotion-changed", kindsOf(got))
	}
}

// TestAnUnreadableBooleanIsSilence. The microdata path cannot see these at all,
// and a nil is a silence rather than a false — otherwise every fall back to
// that path would report thirty listings un-promoting themselves.
func TestAnUnreadableBooleanIsSilence(t *testing.T) {
	before := reading(1000)
	before.IsReserved = boolp(true)
	after := reading(1300) // IsReserved stays nil: we could not read it

	got, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("kinds = %v, want none — we stopped being able to read it, "+
			"which is not the seller changing it", kindsOf(got))
	}
}

// TestPercentChangeRefusesAnAppearance. A rise from nothing is not a
// percentage, and reporting one as infinite or as zero are both lies.
func TestPercentChangeRefusesAnAppearance(t *testing.T) {
	if _, ok := PercentChange(0, 100); ok {
		t.Error("PercentChange from zero reported a percentage")
	}
	pct, ok := PercentChange(4000000, 3500000)
	if !ok {
		t.Fatal("PercentChange refused an ordinary move")
	}
	if pct > -12.4 || pct < -12.6 {
		t.Errorf("PercentChange = %v, want about -12.5", pct)
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/track/ -run 'Diff|Percent|Price|Title|Sort' -v
```

Expected: FAIL to build — `undefined: Reading`.

- [x] **Step 3: Write `track.go`**

The shape is WB_GO's `internal/track/track.go`, with Avito's fields. Keep its all-pointers discipline and its identity check; replace `NmID`/`Dest`/`AppType` with `ItemID`/`Surface`, and the stock and rating fields with the price triple, the title and the bump time.

```go
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
	out = appendSortTime(out, base, before, after)
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
		c.Kind, c.Field, c.Unit, c.Currency = KindPriceWithdrawn, "price", UnitMinor, before.Currency
		c.HadBefore, c.HasNow = true, false
		if before.PriceMinor != nil {
			c.Was = strconv.FormatInt(*before.PriceMinor, 10)
		}
		return append(out, c)

	case before.PriceKind == "negotiable" && after.PriceKind == "exact":
		c := base
		c.Kind, c.Field, c.Unit, c.Currency = KindPriceAppeared, "price", UnitMinor, after.Currency
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
		c.Kind, c.Field, c.Unit, c.Currency = KindPriceChanged, "price", UnitMinor, after.Currency
		c.HadBefore, c.HasNow = true, true
		c.Was = strconv.FormatInt(*before.PriceMinor, 10)
		c.Now = strconv.FormatInt(*after.PriceMinor, 10)
		return append(out, c)
	}
	return out
}
```

Write `appendDiscount`, `appendTitle`, `appendFlags` and `appendBump` in the
same shape: a nil on either side is silence, an equal pair is silence, and a
real move produces one `Change` with its `Unit` set — `UnitPercent`, `UnitText`,
`UnitNone` for the booleans, `UnitSeconds` for the bump.

`appendFlags` covers `IsXL` and `IsRemap` under `KindPromotionChanged` and
`IsReserved` under `KindReservedChanged`. A nil on either side is silence, and
that is load-bearing rather than tidy: the microdata path cannot see any of the
three, so a nil read as false would report thirty listings un-promoting
themselves the first time the extractor fell back.

`PercentChange`:

```go
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
```

- [x] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/track/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test and subtest PASS.

- [x] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/track/ && git commit -m "$(cat <<'MSG'
feat(track): Reading, Change and Diff, with the pointers that matter

Every number on a Reading is a pointer, which is the whole reason the
type exists rather than the diff being written against a store row:
absence and zero are different facts and the difference decides which
kind is emitted.

PriceKind sits beside the pointer rather than being inferred from it,
because on this target a nil price is ambiguous three ways. «Цена
договорная» is the seller saying something and is worth an event; a parse
gap is our defect and is worth none; a real number is neither. A diff
reading only the pointer would report thirty listings becoming
negotiable the first time Avito withheld its hydration state.

So: exact to negotiable is price-withdrawn, negotiable to exact is
price-appeared, and anything involving absent is silence in both
directions. Our own blindness is not the market moving.

Two surfaces are refused as an identity mismatch. They carry different
fields — mobile has no description at all — so a difference between them
is a difference between two extractors, not between two moments.

PercentChange refuses a rise from zero. That is an appearance, and
reporting it as a hundred percent or as infinity is a lie a threshold
rule would act on.

MSG
)"
```

---

## Task 3: `Admissible` — is this pass evidence that something is gone

**Files:**
- Create: `internal/lifecycle/evidence.go`, `internal/lifecycle/evidence_test.go`, `internal/lifecycle/purity_test.go`

**Interfaces:**
- Produces:

```go
type PassFacts struct {
    Class        string
    Surface      string
    Path         string
    Degraded     bool
    IsSeed       bool
    TotalCount   *int64
    LastTotal    *int64
    Session      string
    PrevSession  string
    StartedAt    int64
    PrevEvidenceAt int64
    IntervalSec  int
}

type Verdict struct {
    Admissible bool
    Why        string
}

func Admissible(p PassFacts) Verdict
```

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import "testing"

func admissiblePass() PassFacts {
	return PassFacts{
		Class: "ok", Surface: "mobile", Path: "dom",
		Session: "s1", PrevSession: "s1",
		StartedAt: 10_000, PrevEvidenceAt: 9_700, IntervalSec: 300,
	}
}

func TestAnOrdinaryHealthyPassIsEvidence(t *testing.T) {
	if v := Admissible(admissiblePass()); !v.Admissible {
		t.Errorf("a clean mobile pass was refused: %s", v.Why)
	}
}

// TestEmptyIsEvidence. Offer Ledger's own evidential demanded items_seen > 0,
// so a watch whose result set legitimately emptied could never confirm the
// removal of its last listing. ClassEmpty means the surface's own container was
// present and reported zero, which is a real answer.
func TestEmptyIsEvidence(t *testing.T) {
	p := admissiblePass()
	p.Class = "empty"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("an empty answer was refused: %s — a query that legitimately "+
			"returned nothing is as healthy as one that returned fifty", v.Why)
	}
}

// TestEveryTransportRefusalIsNotEvidence.
func TestEveryTransportRefusalIsNotEvidence(t *testing.T) {
	for _, class := range []string{
		"throttled", "burned", "soft-block", "challenge", "cold", "mangled",
		"shape", "other",
	} {
		p := admissiblePass()
		p.Class = class
		if v := Admissible(p); v.Admissible {
			t.Errorf("class %q was admitted as evidence that something is gone", class)
		}
	}
}

// TestADesktopDomPassIsPresenceEvidenceOnly.
//
// On desktop the primary path is 'state'. loaderData.toggles carries
// runtime_catalog_drop_state_for_bots, so a silent fall back to 'dom' is the
// bot-classification warning itself — the earliest one available, earlier than
// a 429 and much earlier than a captcha.
func TestADesktopDomPassIsPresenceEvidenceOnly(t *testing.T) {
	p := admissiblePass()
	p.Surface, p.Path = "desktop", "dom"
	if v := Admissible(p); v.Admissible {
		t.Error("a desktop pass answered by the DOM fallback was admitted; the " +
			"fall back is the bot warning, not a reading")
	}
}

// TestAMobileDomPassIsEvidence. The mobile document carries no
// data-mfe-state block of any kind — measured 2026-08-23 — so on that surface
// 'dom' is the primary path and nothing about it is a degradation. Reading the
// rule as "state or nothing" would make every mobile pass inadmissible and no
// disappearance could ever be confirmed.
func TestAMobileDomPassIsEvidence(t *testing.T) {
	p := admissiblePass()
	p.Surface, p.Path = "mobile", "dom"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("the mobile primary path was refused: %s", v.Why)
	}
}

func TestADegradedExtractionIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.Degraded = true
	if v := Admissible(p); v.Admissible {
		t.Error("a pass carrying a degradation warning was admitted")
	}
}

func TestASeedIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.IsSeed = true
	if v := Admissible(p); v.Admissible {
		t.Error("a seeding pass was admitted; it has no predecessor to differ from")
	}
}

// TestACollapsedCountIsNotEvidence. A fall to below a quarter of the last
// admissible count is far outside ordinary churn on a page of thirty to fifty
// and well inside what a truncated or partially served page looks like.
func TestACollapsedCountIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.LastTotal, p.TotalCount = ptr(int64(40000)), ptr(int64(300))
	if v := Admissible(p); v.Admissible {
		t.Error("a count that fell from 40000 to 300 was admitted")
	}
}

// TestAMissingCountSkipsTheCollapseTestRatherThanFailingIt. A missing number
// must never become arithmetic on zero.
func TestAMissingCountSkipsTheCollapseTestRatherThanFailingIt(t *testing.T) {
	p := admissiblePass()
	p.LastTotal, p.TotalCount = ptr(int64(40000)), nil
	if v := Admissible(p); !v.Admissible {
		t.Errorf("a pass whose surface reported no count was refused: %s", v.Why)
	}
}

// TestAGapInCollectionIsNotEvidence.
//
// For a laptop this is a weekly event. A machine that slept from Friday to
// Monday wakes up, sees a page one that has turned over completely, and would
// report every listing it was watching as gone — one weekend producing a
// hundred false removals is how a person learns to ignore the notifications.
func TestAGapInCollectionIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.PrevEvidenceAt = p.StartedAt - int64(4*p.IntervalSec)
	if v := Admissible(p); v.Admissible {
		t.Error("a pass more than three intervals after the last evidential one " +
			"was admitted; it is a re-seed, not a reading")
	}
}

func ptr[T any](v T) *T { return &v }
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/lifecycle/ -v
```

Expected: FAIL to build.

- [x] **Step 3: Write `evidence.go`**

Every refusal carries a `Why`, because this value is written into
`passes.degraded_why` and read by a person asking why their watch never reports
anything.

```go
// reseedAfterIntervals is how stale the last evidential pass may be before the
// next one is a re-seed rather than a reading. Three, because two is inside
// ordinary jitter for a laptop that slept through one tick.
const reseedAfterIntervals = 3

// collapseFraction is how far a result count may fall and still be believed. A
// quarter is far outside ordinary churn on a page of thirty to fifty and well
// inside what a truncated page looks like.
const collapseFraction = 4
```

- [x] **Step 4: Write the purity guard**

Copy `internal/track/purity_test.go` into `internal/lifecycle/`, changing the package name and the message. `lifecycle` has the same rule and the same reason.

- [x] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/lifecycle/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS.

- [x] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/lifecycle/ && git commit -m "$(cat <<'MSG'
feat(lifecycle): Admissible — when a pass may be evidence that something left

Six gates, and the two that look like details are the ones that matter.

"Its surface's own primary extractor" is not "the state path". On desktop
the primary path is state and a silent fall back to dom is the
bot-classification warning itself. On mobile there IS no
data-mfe-state block at all, so dom is primary and nothing about it is a
degradation — reading the rule as "state or nothing" would make every
mobile pass inadmissible and no disappearance could ever be confirmed.

A missing result count skips the collapse test rather than failing it. A
missing number must never become arithmetic on zero.

And a gap in collection is not evidence of anything. For a laptop this
is a weekly event: a machine that slept from Friday to Monday would wake
up, see a page one that had turned over completely, and report every
listing it was watching as gone. One weekend of a hundred false removals
is how a person learns to ignore the notifications.

Every refusal carries its reason, because this lands in
passes.degraded_why and is read by somebody asking why their watch never
tells them anything.

MSG
)"
```

---

## Task 4: `Comparable` — the gate the measurement forced

This task does not come from the spec. It comes from
`docs/measurements/2026-08-24-identity-decides-the-result-set.md`, and without it
every gate in Task 3 is defeated by a fact the design did not know.

**Files:**
- Create: `internal/lifecycle/compare.go`, `internal/lifecycle/compare_test.go`

**Interfaces:**
- Produces: `func Comparable(p PassFacts) Verdict`.

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import (
	"strings"
	"testing"
)

// TestTwoPassesOnOneIdentityAreComparable.
func TestTwoPassesOnOneIdentityAreComparable(t *testing.T) {
	if v := Comparable(admissiblePass()); !v.Admissible {
		t.Errorf("two passes on one session were refused: %s", v.Why)
	}
}

// TestAnIdentityChangeMakesTwoPassesIncomparable.
//
// Measured 2026-08-24: in direct mode the result set is a function of srv_id.
// Six passes over two identities gave 100% overlap within an identity — every
// pair, same order — and 23% across them.
//
// The ladder changes the identity by itself: it renews on a 403 that three
// waits did not clear, and recycles the endpoint on a soft block. Differenced
// against its predecessor, the next pass reads as twenty-three listings
// vanishing and twenty-three arriving, on schedule, for ever.
//
// No confirmation count and no elapsed span can filter this out, because the
// second confirmation is as wrong as the first and arrives on time.
func TestAnIdentityChangeMakesTwoPassesIncomparable(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = "s0"
	v := Comparable(p)
	if v.Admissible {
		t.Fatal("two passes read through different identities were compared")
	}
	if !strings.Contains(v.Why, "identity") && !strings.Contains(v.Why, "session") {
		t.Errorf("Why = %q; it is read by a person and must name the reason", v.Why)
	}
}

// TestNoPredecessorIsIncomparableRatherThanComparable. The first pass of a
// watch has nothing behind it, and treating that as a match would make every
// seeding pass a full set of arrivals AND a full set of departures.
func TestNoPredecessorIsIncomparableRatherThanComparable(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = ""
	if v := Comparable(p); v.Admissible {
		t.Error("a pass with no predecessor was called comparable")
	}
}

// TestComparableIsSeparateFromAdmissible, and both are required.
//
// They answer different questions. Admissible asks whether this pass is a good
// reading; Comparable asks whether it and the one before it are readings of the
// same thing. A pass can be perfect and still be incomparable, which is exactly
// what an identity renewal produces.
func TestComparableIsSeparateFromAdmissible(t *testing.T) {
	p := admissiblePass()
	p.PrevSession = "s0"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("Admissible refused a clean pass because of its predecessor: %s", v.Why)
	}
	if v := Comparable(p); v.Admissible {
		t.Error("Comparable admitted a pass across an identity change")
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/lifecycle/ -run Comparable -v
```

Expected: FAIL to build — `undefined: Comparable`.

- [x] **Step 3: Write `compare.go`**

```go
// Comparable reports whether this pass may be differenced against the one
// before it.
//
// It is a separate question from Admissible and both are required. Admissible
// asks whether this pass is a good reading of the market. Comparable asks
// whether it and its predecessor are readings of the SAME market — and on this
// target they can fail to be, which the design did not know.
//
// Measured 2026-08-24, mobile, date-sorted, direct transport: the result set is
// a function of srv_id. Six passes over two identities gave 100% overlap within
// an identity, every pair, and 23% across them; two fresh identities shared
// zero of thirty. The ladder changes the identity by itself — it renews on a
// 403 three waits did not clear and recycles the endpoint on a soft block — so
// this is not a rare configuration mistake but an ordinary Tuesday.
//
// A pass that is not comparable is still a perfectly good reading. Its members
// are recorded and its arrivals are real. What it cannot do is confirm that
// anything LEFT, because the thing it was compared against was a different
// slice of the market.
func Comparable(p PassFacts) Verdict {
	if p.PrevSession == "" {
		return Verdict{false, "no previous pass to compare against"}
	}
	if p.Session != p.PrevSession {
		return Verdict{false, "the identity changed since the last pass, and " +
			"this site shows a different result set to a different identity"}
	}
	return Verdict{true, ""}
}
```

- [x] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/lifecycle/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [x] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/lifecycle/ && git commit -m "$(cat <<'MSG'
feat(lifecycle): Comparable, the gate a measurement forced into the design

This is not in the spec. It is in
docs/measurements/2026-08-24-identity-decides-the-result-set.md, and
without it every gate in Admissible is defeated by a fact the design did
not know.

Measured: in direct mode Avito's result set is a function of srv_id. Six
passes over two identities gave 100%% overlap within an identity — every
pair, same order — and 23%% across them. Two fresh identities shared zero
of thirty.

The ladder changes the identity by itself: it renews on a 403 that three
waits did not clear, and recycles the endpoint on a soft block. So the
next pass is differenced against a different slice of the market and
reads as twenty-three listings vanishing and twenty-three arriving, on
schedule, for ever. No confirmation count and no elapsed span filters
that out, because the second confirmation is as wrong as the first and
arrives on time.

Admissible and Comparable are separate questions and both are required.
A pass can be a perfect reading and still be incomparable. Its members
are recorded and its arrivals are real; what it cannot do is confirm that
anything left.

MSG
)"
```

---

## Task 5: Displacement accounting and the two absence gates

**Files:**
- Create: `internal/lifecycle/absence.go`, `internal/lifecycle/absence_test.go`

**Interfaces:**
- Produces:

```go
type Member struct {
    ItemID        int64
    LastPosition  int
    WindowSize    int
    MissingStreak int
    MissingSince  int64
}

type AbsenceInput struct {
    Member        Member
    Arrivals      int
    Now           int64
    Confirmations int
    MinSpanSec    int
}

type Absence struct {
    Displaced bool
    Confirmed bool
    Why       string
}

func Explain(in AbsenceInput) Absence
```

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import "testing"

// TestAnAbsenceExplainedByArrivalsIsNotEvidence.
//
// In a date-sorted window of k slots, an item at rank r is pushed out by k − r
// arrivals. That is exact arithmetic over two integers the schema already
// stores and one it counts per pass, it costs no extra request, and it is the
// difference between "gone" and "we only look at the first page".
func TestAnAbsenceExplainedByArrivalsIsNotEvidence(t *testing.T) {
	// Rank 28 of a 30-slot window: three arrivals push it off the bottom.
	in := AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 28, WindowSize: 30},
		Arrivals:      3, Now: 10_000,
		Confirmations: 2, MinSpanSec: 1200,
	}
	got := Explain(in)
	if !got.Displaced {
		t.Errorf("an item at rank 28 of 30 with 3 arrivals was not called "+
			"displaced: %s", got.Why)
	}
	if got.Confirmed {
		t.Error("a displaced item was confirmed gone")
	}
}

// TestAnAbsenceWithHeadroomIsACandidate. Rank 3 of 30 needs 27 arrivals to be
// pushed off, and three does not do it.
func TestAnAbsenceWithHeadroomIsACandidate(t *testing.T) {
	in := AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 3, WindowSize: 30},
		Arrivals:      3, Now: 10_000,
		Confirmations: 2, MinSpanSec: 1200,
	}
	if got := Explain(in); got.Displaced {
		t.Errorf("an item at rank 3 of 30 was called displaced by 3 arrivals: %s",
			got.Why)
	}
}

// TestBothGatesAreRequired.
//
// Two passes thirty seconds apart is one ranking blip. The span gate is what
// makes the count mean elapsed time rather than poll count — without it a watch
// on a two-minute interval confirms a disappearance in four minutes and a watch
// on an hourly one takes two hours, for the same evidence.
func TestBothGatesAreRequired(t *testing.T) {
	base := Member{ItemID: 1, LastPosition: 3, WindowSize: 30}
	cases := []struct {
		name   string
		streak int
		since  int64
		want   bool
	}{
		{"neither gate", 1, 9_900, false},
		{"count only", 2, 9_900, false},
		{"span only", 1, 8_000, false},
		{"both", 2, 8_000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base
			m.MissingStreak, m.MissingSince = c.streak, c.since
			got := Explain(AbsenceInput{
				Member: m, Arrivals: 0, Now: 10_000,
				Confirmations: 2, MinSpanSec: 1200,
			})
			if got.Confirmed != c.want {
				t.Errorf("Confirmed = %v, want %v (%s)", got.Confirmed, c.want, got.Why)
			}
		})
	}
}

// TestAZeroWindowIsNeverDisplacement. A pass that returned nothing has a window
// of zero, and k − r with k of zero would make every absence displacement — the
// exact opposite of what an empty result set means.
func TestAZeroWindowIsNeverDisplacement(t *testing.T) {
	in := AbsenceInput{
		Member:        Member{ItemID: 1, LastPosition: 3, WindowSize: 0},
		Arrivals:      0, Now: 10_000,
		Confirmations: 2, MinSpanSec: 1200,
	}
	if got := Explain(in); got.Displaced {
		t.Error("an absence from an empty result set was called displacement")
	}
}

// TestDisplacementNeverConfirms, whatever the gates say. The arithmetic is the
// stronger statement: we know why it is not here.
func TestDisplacementNeverConfirms(t *testing.T) {
	in := AbsenceInput{
		Member: Member{
			ItemID: 1, LastPosition: 29, WindowSize: 30,
			MissingStreak: 9, MissingSince: 1,
		},
		Arrivals: 5, Now: 10_000,
		Confirmations: 2, MinSpanSec: 1200,
	}
	got := Explain(in)
	if got.Confirmed {
		t.Error("an item explained by displacement was confirmed gone despite " +
			"both gates passing; the arithmetic is the stronger statement")
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/lifecycle/ -run 'Absence|Displac|Gates|Window' -v
```

Expected: FAIL to build.

- [x] **Step 3: Write `absence.go`**

```go
// Explain says why a listing is not in this pass, and whether that is
// believable as a disappearance.
//
// The order is deliberate: displacement is checked first and wins outright. It
// is the stronger statement — we know why the item is not here — and the two
// gates are only about how long we have been unsure.
func Explain(in AbsenceInput) Absence {
	// In a date-sorted window of k slots, an item at rank r is pushed out by
	// k − r arrivals. A window of zero is not a window: a pass that returned
	// nothing would otherwise make every absence displacement, which is the
	// opposite of what an empty result set means.
	if in.Member.WindowSize > 0 {
		headroom := in.Member.WindowSize - in.Member.LastPosition
		if in.Arrivals >= headroom {
			return Absence{Displaced: true, Why: fmt.Sprintf(
				"rank %d of %d, and %d arrivals is enough to push it off",
				in.Member.LastPosition, in.Member.WindowSize, in.Arrivals)}
		}
	}
	...
}
```

- [x] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/lifecycle/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test and subtest PASS.

- [x] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/lifecycle/ && git commit -m "$(cat <<'MSG'
feat(lifecycle): displacement accounting and the two absence gates

Page one of a date-sorted query is a window, not a census. A listing
leaves it because it was removed, because a newer posting pushed it off
the bottom, or because a promotion reshuffled the block — and the
response says which for none of them.

Displacement removes most of the guessing with exact arithmetic over
integers the schema already stores: in a window of k slots an item at
rank r is pushed out by k − r arrivals. It costs no extra request and it
is the difference between "gone" and "we only look at the first page".

Displacement is checked first and wins outright, even against both gates
passing. It is the stronger statement: we know why the item is not here,
where the gates only say how long we have been unsure.

Both gates are required. Two passes thirty seconds apart is one ranking
blip, and the span gate is what makes the count mean elapsed time rather
than poll count — without it a two-minute watch confirms a disappearance
in four minutes and an hourly one takes two hours, on the same evidence.

A window of zero is never displacement. k − r with k of zero would make
every absence explained, which is the opposite of what an empty result
set means.

MSG
)"
```

---

## Task 6: The stored `evidential`, replacing increment 3's floor

**Files:**
- Create: `internal/store/evidential.go`, `internal/store/evidential_test.go`
- Modify: `internal/store/pass.go` — compute it inside the transaction
- Modify: `cmd/avitomon/run.go` — stop computing the floor there

**Interfaces:**
- Consumes: `lifecycle.PassFacts`, `lifecycle.Admissible`, `lifecycle.Comparable`.
- Produces: `func (s *Store) passFacts(ctx, tx, p Pass) (lifecycle.PassFacts, error)`; `Pass` gains `Degraded bool` and loses nothing.

- [x] **Step 1: Write the failing test**

The test writes a sequence of passes and asserts the stored column, because the
value is what every later query reads:

```go
// TestEvidentialIsComputedRatherThanSupplied. Increment 3's run --once passed a
// floor in by hand — a class that answered. The real tests live in lifecycle
// and the store applies them, so a caller cannot mark a soft block as evidence
// by filling in a struct field.
func TestEvidentialIsComputedRatherThanSupplied(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	p := aPass(wid, 1000)
	p.Class = "soft-block"
	p.Evidential = true // a caller asserting something it does not get to decide

	res, err := s.SavePass(ctx, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stored int
	s.DB().QueryRow(`SELECT evidential FROM passes WHERE id = ?`, res.PassID).Scan(&stored)
	if stored != 0 {
		t.Error("a soft block was stored as evidential because the caller said so")
	}
}
```

Plus one that the stored `degraded_why` carries the refusal's reason, and one
that an identity change stores `evidential = 1` but suppresses departures — the
distinction Task 4 exists for.

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run Evidential -v
```

Expected: FAIL — the caller's value is stored as given.

- [x] **Step 3: Compute it inside the transaction**

`savePassTx` reads the watch's previous pass — its session, its evidential
timestamp and its `last_total_count` — builds a `lifecycle.PassFacts`, and
stores `Admissible(...)`'s verdict in `evidential` and its `Why` in
`degraded_why`. `Comparable(...)`'s verdict is returned to the window producer
in Task 7 rather than stored: it is a property of a *pair*, and a column on one
row would be answering a question about two.

- [x] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w . && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS, including increment 3's — `TestANonEvidentialPassNeverAdvancesAnAbsenceCounter` now exercises the real rule rather than a caller's assertion.

- [x] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/ cmd/avitomon/ && git commit -m "$(cat <<'MSG'
feat(store): evidential is computed, not supplied

Increment 3 let run --once pass a floor in by hand — a class that
answered — with a comment saying the real tests were increment 4's.
They are here, they live in lifecycle, and the store applies them inside
the transaction.

A caller can no longer mark a soft block as evidence by filling in a
struct field, which is the whole point: the column is what every later
query about absence reads, and it must mean what it says.

The refusal's reason lands in degraded_why, because somebody will ask
why their watch never tells them anything and the answer should be in
the row rather than in a log nobody kept.

Comparable's verdict is deliberately NOT stored. It is a property of a
pair of passes, and a column on one row would be answering a question
about two.

MSG
)"
```

---

## Task 7: The window producer, inside the pass transaction

The set difference against the previous window, computed where a concurrent
watch cannot observe it half-written. This is the task the increment is
demonstrated by.

**Files:**
- Create: `internal/store/changes.go`, `internal/store/window.go`, `internal/store/window_test.go`
- Modify: `internal/store/pass.go` — call the producer from `savePassTx`

**Interfaces:**
- Consumes: `track.Kind`, `track.Change`, `lifecycle.Admissible`, `lifecycle.Comparable`, `lifecycle.Explain`, and `passFacts` from Task 6.
- Produces:

```go
// In changes.go:
type ChangeRow struct {
    ID         int64
    Kind       track.Kind
    ItemID     int64
    WatchID    int64
    PassID     *int64
    TS         int64
    Field      string
    Was, Now   string
    Unit       track.Unit
    HadBefore  bool
    HasNow     bool
    Confidence float64
    Backfill   bool
    Producer   string // "window" | "series" | "transport"
}

func (s *Store) Changes(ctx context.Context, watchID, since int64, limit int) ([]ChangeRow, error)
func insertChange(ctx context.Context, tx *sql.Tx, c ChangeRow) error
```

- [x] **Step 1: Write the failing test**

Create `internal/store/window_test.go`. Every case is a rule from the spec or a
measurement, and the name says which:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// kindsFor is every change kind this watch produced, in id order.
func kindsFor(t *testing.T, s *Store, watchID int64) []track.Kind {
	t.Helper()
	rows, err := s.Changes(context.Background(), watchID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]track.Kind, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

func countKind(t *testing.T, s *Store, watchID int64, k track.Kind) int {
	t.Helper()
	n := 0
	for _, got := range kindsFor(t, s, watchID) {
		if got == k {
			n++
		}
	}
	return n
}

// TestTheSeedingPassWritesArrivalsMarkedBackfill.
//
// A watch's first pass has nothing behind it. Its members are recorded and its
// arrivals are real, but they are marked backfill so a rule written on
// listing-appeared does not fire thirty times the moment somebody adds a watch.
func TestTheSeedingPassWritesArrivalsMarkedBackfill(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	p := aPass(wid, 1000)
	p.IsSeed = true
	if _, err := s.SavePass(ctx, p, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("the seeding pass produced %d changes, want 3 arrivals", len(rows))
	}
	for _, r := range rows {
		if r.Kind != track.KindListingAppeared {
			t.Errorf("kind = %q, want listing-appeared", r.Kind)
		}
		if !r.Backfill {
			t.Error("a seeding arrival is not marked backfill; a rule on " +
				"listing-appeared would fire thirty times the moment somebody " +
				"adds a watch")
		}
		if r.Producer != "window" {
			t.Errorf("producer = %q, want window", r.Producer)
		}
	}
}

// TestASeedingPassConfirmsNothingGone. It has no predecessor, so there is
// nothing it could have left.
func TestASeedingPassConfirmsNothingGone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	p := aPass(wid, 1000)
	p.IsSeed = true
	if _, err := s.SavePass(ctx, p, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("the seeding pass produced %d vanished rows", n)
	}
}

// TestAListingPushedOffTheBottomProducesNothing.
//
// In a date-sorted window of k slots an item at rank r is pushed out by k − r
// arrivals. This is the case that separates "gone" from "we only look at the
// first page", and it must produce silence rather than a low-confidence guess.
func TestAListingPushedOffTheBottomProducesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// A window of three. Item 3 is at the bottom.
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	// One arrival, and item 3 is gone: exactly the displacement the arithmetic
	// predicts, since 3 − 3 = 0 arrivals of headroom.
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(9, 1, 2)); err != nil {
		t.Fatal(err)
	}

	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("a displaced listing produced %d vanished rows", n)
	}
	if n := countKind(t, s, wid, track.KindListingLeftWindow); n != 1 {
		t.Errorf("left-window rows = %d, want 1 — it did leave the window, and "+
			"that is a different sentence from having been removed", n)
	}
	// And the member row records why, so the panel can say so.
	var displaced int
	if err := s.DB().QueryRow(
		`SELECT displaced FROM watch_members WHERE watch_id = ? AND item_id = 3`,
		wid).Scan(&displaced); err != nil {
		t.Fatal(err)
	}
	if displaced != 1 {
		t.Error("the member was not marked displaced")
	}
}

// TestAListingGoneFromTwoCleanPassesTwentyMinutesApartIsVanishedAtConfidence03.
//
// 0.3 and not 1.0, and the axis is structural: a page one is a window and not a
// census, so a conclusion drawn from something not being in it is weaker than a
// fact an item page stated.
func TestAListingGoneFromTwoCleanPassesTwentyMinutesApartIsVanishedAtConfidence03(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// Rank 1 of three, so displacement can never explain its absence.
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(2, 3)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Fatalf("one confirmation was enough; both gates are required")
	}
	// Twenty minutes and one second after it went missing.
	if _, err := s.SavePass(ctx, aPass(wid, 1300+1201), listingsFor(2, 3)); err != nil {
		t.Fatal(err)
	}

	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ChangeRow
	for i := range rows {
		if rows[i].Kind == track.KindListingVanished {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("no vanished row after two confirmations twenty minutes apart; "+
			"kinds were %v", kindsFor(t, s, wid))
	}
	if found.ItemID != 1 {
		t.Errorf("vanished item = %d, want 1", found.ItemID)
	}
	if found.Confidence != 0.3 {
		t.Errorf("confidence = %v, want 0.3 — a page one is a window and not a "+
			"census", found.Confidence)
	}
}

// TestTwoConsecutiveDroppedStatePassesProduceNoVanishedRowsAtAll.
//
// This is the failure the whole target is shaped around. A soft block is HTTP
// 200 with no container, and differenced as a real reading it fires a removal
// alert for every listing in the watch.
func TestTwoConsecutiveDroppedStatePassesProduceNoVanishedRowsAtAll(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{1300, 1300 + 1201} {
		p := aPass(wid, at)
		p.Class, p.Path = "soft-block", ""
		if _, err := s.SavePass(ctx, p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("two soft blocks produced %d vanished rows; that is a removal "+
			"alert for every listing in the watch, and it is the failure this "+
			"whole design exists to prevent", n)
	}
}

// TestAListingThatFloatedBackIsReturnedAndNotAppeared.
//
// listing-appeared requires membership now, absence before, AND
// listings.first_seen_at equal to this pass. Without that third clause a
// recovered soft block announces thirty new listings that have been there all
// week — the cheapest correctness guard in the design.
func TestAListingThatFloatedBackIsReturnedAndNotAppeared(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1600), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}

	if n := countKind(t, s, wid, track.KindListingReturned); n != 1 {
		t.Errorf("returned rows = %d, want 1", n)
	}
	// Three from the first pass and none since.
	if n := countKind(t, s, wid, track.KindListingAppeared); n != 3 {
		t.Errorf("appeared rows = %d, want 3 — a listing that floated back is "+
			"not news, and first_seen_at is what says so", n)
	}
}

// TestAnIdentityChangeProducesArrivalsButNoDepartures.
//
// Measured 2026-08-24: in direct mode the result set is a function of srv_id —
// 100%% overlap within an identity, 23%% across. The ladder changes the identity
// by itself, on a 403 three waits did not clear and on a soft block. So the
// next pass is a perfectly good reading of a DIFFERENT slice, and differencing
// it against its predecessor would report most of the watch as gone.
//
// No earlier draft of this design would have caught this, because nothing in
// the measured surface said the result set was per-identity.
func TestAnIdentityChangeProducesArrivalsButNoDepartures(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	first := aPass(wid, 1000)
	first.Session = "identity-A"
	if _, err := s.SavePass(ctx, first, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	// A renewal happened, and the site showed this identity a different slice.
	for i, at := range []int64{1300, 1300 + 1201} {
		p := aPass(wid, at)
		p.Session = "identity-B"
		if _, err := s.SavePass(ctx, p, listingsFor(7, 8, 9)); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}

	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("an identity change produced %d vanished rows; the two passes "+
			"are readings of different markets and no confirmation count can "+
			"filter that out", n)
	}
	if n := countKind(t, s, wid, track.KindListingAppeared); n < 3 {
		t.Errorf("appeared rows = %d; the new listings really were seen, and "+
			"incomparable does not mean unread", n)
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Seeding|PushedOff|Vanished|DroppedState|FloatedBack|IdentityChange' -v
```

Expected: FAIL to build — `undefined: ChangeRow`.

- [x] **Step 3: Write `changes.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// ChangeRow is one row of the changes table.
//
// PassID is a pointer with no foreign key, and that is deliberate: retention
// removes old passes, and a change must outlive the reading it rests on rather
// than be cascaded away with it. "Why was I told this was gone" has to stay
// answerable after the evidence has been thinned.
type ChangeRow struct {
	ID         int64
	Kind       track.Kind
	ItemID     int64
	WatchID    int64
	PassID     *int64
	TS         int64
	Field      string
	Was, Now   string
	Unit       track.Unit
	HadBefore  bool
	HasNow     bool
	Confidence float64
	Backfill   bool
	Producer   string
}

func insertChange(ctx context.Context, tx *sql.Tx, c ChangeRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO changes
		  (kind, item_id, watch_id, pass_id, ts, saved_at, field, was, now,
		   unit, had_before, has_now, confidence, backfill, producer)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.Kind), c.ItemID, c.WatchID, c.PassID, c.TS, c.TS,
		c.Field, c.Was, c.Now, string(c.Unit),
		c.HadBefore, c.HasNow, c.Confidence, c.Backfill, c.Producer)
	if err != nil {
		return fmt.Errorf("store: insert change %s: %w", c.Kind, err)
	}
	return nil
}

// Changes reads a watch's changes in the order they were written.
//
// Ordered by id and not by ts: two changes can share a second, and a person
// reading a list wants the order they happened in rather than whichever order
// SQLite found convenient.
func (s *Store) Changes(ctx context.Context, watchID, since int64, limit int) ([]ChangeRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, item_id, watch_id, pass_id, ts, field, was, now,
		       unit, had_before, has_now, confidence, backfill, producer
		FROM changes
		WHERE watch_id = ? AND ts >= ?
		ORDER BY id LIMIT ?`, watchID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("store: changes: %w", err)
	}
	defer rows.Close()

	var out []ChangeRow
	for rows.Next() {
		var c ChangeRow
		var kind, unit string
		if err := rows.Scan(&c.ID, &kind, &c.ItemID, &c.WatchID, &c.PassID,
			&c.TS, &c.Field, &c.Was, &c.Now, &unit, &c.HadBefore, &c.HasNow,
			&c.Confidence, &c.Backfill, &c.Producer); err != nil {
			return nil, fmt.Errorf("store: changes: %w", err)
		}
		c.Kind, c.Unit = track.Kind(kind), track.Unit(unit)
		out = append(out, c)
	}
	return out, rows.Err()
}
```

- [x] **Step 4: Write `window.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/lifecycle"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// vanishedConfidence is what a conclusion drawn from a window is worth.
//
// 1.0 is for a fact an item page stated. A page one is a window and not a
// census: a listing can leave it because it was removed, because a newer
// posting pushed it off the bottom, or because a promotion reshuffled the
// block, and the response says which for none of them. Displacement removes
// the second of those three by arithmetic; the remaining uncertainty is what
// this number carries into every alert.
const vanishedConfidence = 0.3

// produceWindow writes the membership differences for one pass.
//
// It runs inside the pass transaction, and that placement is the point: a set
// difference against the previous window must not be observed half-written by a
// concurrent watch, and a reader that saw the arrivals without the departures
// would draw the wrong conclusion from a consistent-looking snapshot.
//
// Three gates decide what it may say, and they are separate questions:
//
//   - admissible: is this pass a good reading at all
//   - comparable: are this pass and its predecessor readings of the SAME market
//   - displaced:  is this particular absence explained by arrivals
//
// Arrivals are written whenever the pass was read at all. Departures need all
// three.
func produceWindow(
	ctx context.Context, tx *sql.Tx,
	p Pass, passID int64, listings []avito.Listing,
	previous map[int64]bool, arrivals int,
	facts lifecycle.PassFacts, w Watch,
) error {
	adm := lifecycle.Admissible(facts)
	cmp := lifecycle.Comparable(facts)

	// --- arrivals and returns ---
	//
	// listing-appeared requires membership now, absence before, AND
	// listings.first_seen_at equal to this pass. Without the third clause a
	// recovered soft block announces thirty new listings that have been there
	// all week. It is the cheapest correctness guard in the design.
	for _, l := range listings {
		if previous[l.ItemID] {
			continue
		}
		var firstSeen int64
		if err := tx.QueryRowContext(ctx,
			`SELECT first_seen_at FROM listings WHERE item_id = ?`, l.ItemID).
			Scan(&firstSeen); err != nil {
			return fmt.Errorf("store: window: first seen: %w", err)
		}
		kind := track.KindListingReturned
		if firstSeen == p.StartedAt {
			kind = track.KindListingAppeared
		}
		if err := insertChange(ctx, tx, ChangeRow{
			Kind: kind, ItemID: l.ItemID, WatchID: p.WatchID,
			PassID: &passID, TS: p.StartedAt,
			Confidence: 1.0, Producer: "window",
			HasNow: true,
			// A seed's arrivals are real and are marked so a rule on
			// listing-appeared does not fire thirty times the moment somebody
			// adds a watch. An incomparable pass is marked for the same
			// reason: its arrivals are a different slice, not news.
			Backfill: p.IsSeed || !cmp.Admissible,
		}); err != nil {
			return err
		}
	}

	// --- departures ---
	if !adm.Admissible || !cmp.Admissible {
		return nil
	}
	return produceDepartures(ctx, tx, p, passID, arrivals, w)
}
```

`produceDepartures` reads the watch's absent members, calls `lifecycle.Explain`
for each, writes `displaced` back to the member row, and emits
`listing-left-window` for a displaced one and `listing-vanished` at
`vanishedConfidence` for a confirmed one. It writes `listings.state` and
`state_at` only for the confirmed case, and only with `state_source = 'search'`
— the column exists so that a window conclusion can never write `closed`.

- [x] **Step 5: Call it from `savePassTx`**

After `updateMembers`, and inside the same transaction. `savePassTx` already
holds `previous`, `out.Arrivals` and `passID`; it needs the `Watch` row and the
`lifecycle.PassFacts` Task 6 built, both of which it can read in the same
transaction.

- [x] **Step 6: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS, increment 3's included.

- [x] **Step 7: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/ && git commit -m "$(cat <<'MSG'
feat(store): the window producer, inside the pass transaction

The set difference against the previous window, written where a
concurrent watch cannot observe it half-written — a reader that saw the
arrivals without the departures would draw the wrong conclusion from a
snapshot that looked consistent.

Three gates decide what it may say, and they are separate questions.
Admissible: is this pass a good reading. Comparable: are this pass and
its predecessor readings of the same market. Displaced: is this
particular absence explained by arrivals. Arrivals are written whenever
the pass was read at all; departures need all three.

listing-appeared requires membership now, absence before, AND
first_seen_at equal to this pass. Without that third clause a recovered
soft block announces thirty new listings that have been there all week.

A vanished row carries confidence 0.3, not 1.0. A page one is a window
and not a census: a listing leaves it because it was removed, because
something pushed it off the bottom, or because a promotion reshuffled
the block. Displacement removes the second by arithmetic; this number
carries the rest of the doubt into every alert.

MSG
)"
```

---

## Task 8: The series producer and the saturation metric

**Files:**
- Create: `internal/store/series.go`, `internal/store/series_test.go`

**Interfaces:**
- Consumes: `track.Reading`, `track.Diff`.
- Produces: `func (s *Store) ProduceSeries(ctx context.Context) (int, error)`; `func (s *Store) Saturation(ctx context.Context, watchID int64, passes int) (float64, bool, error)`.

- [x] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// TestTheSeriesProducerTurnsTwoSnapshotsIntoOnePriceChange.
//
// It runs out of band rather than inside the pass transaction, because a price
// move is a property of two snapshots and not of one window, and holding the
// write lock while diffing every listing on the page would make the pass as
// slow as the market is busy.
func TestTheSeriesProducerTurnsTwoSnapshotsIntoOnePriceChange(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1)); err != nil {
		t.Fatal(err)
	}
	moved := listingsFor(1)
	moved[0].Price.Minor = 3500000
	moved[0].PriceLabel = "35 000 ₽"
	if _, err := s.SavePass(ctx, aPass(wid, 1300), moved); err != nil {
		t.Fatal(err)
	}

	n, err := s.ProduceSeries(ctx)
	if err != nil {
		t.Fatalf("ProduceSeries: %v", err)
	}
	if n != 1 {
		t.Fatalf("ProduceSeries wrote %d changes, want 1", n)
	}
	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ChangeRow
	for i := range rows {
		if rows[i].Kind == track.KindPriceChanged {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatal("no price-changed row")
	}
	if found.Was != "4000000" || found.Now != "3500000" {
		t.Errorf("was/now = %q/%q, want the minor units", found.Was, found.Now)
	}
	if found.Producer != "series" {
		t.Errorf("producer = %q, want series", found.Producer)
	}
}

// TestProduceSeriesIsIdempotent. It runs on a schedule, and a second run over
// the same snapshots must not write the same change twice.
func TestProduceSeriesIsIdempotent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1))
	moved := listingsFor(1)
	moved[0].Price.Minor = 3500000
	s.SavePass(ctx, aPass(wid, 1300), moved)

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := s.ProduceSeries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a second run wrote %d changes; the producer must remember "+
			"where it got to", n)
	}
}

// TestASnapshotPairSpanningAnAbsentReadingProducesNothing. Our own blindness is
// not the market moving, and track.Diff already refuses — this asserts the
// store does not work around it.
func TestASnapshotPairSpanningAnAbsentReadingProducesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1))

	gap := listingsFor(1)
	gap[0].PriceKind = avito.PriceAbsent
	gap[0].Price = avito.Money{}
	gap[0].PriceLabel = ""
	s.SavePass(ctx, aPass(wid, 1300), gap)

	if _, err := s.ProduceSeries(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range kindsFor(t, s, wid) {
		if k == track.KindPriceWithdrawn || k == track.KindPriceChanged {
			t.Errorf("a reading we failed to parse produced %q", k)
		}
	}
}

// TestSaturationSaysWhetherTheIntervalIsShortEnough.
//
// items_on_page / arrivals. If a watch of thirty slots takes thirty new
// listings every pass, the window turned over completely between polls and the
// watch is blind to whatever passed through in between — it is not measuring
// the market, it is sampling it.
func TestSaturationSaysWhetherTheIntervalIsShortEnough(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// Three passes, each turning over completely: saturation 1.0.
	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))
	s.SavePass(ctx, aPass(wid, 1300), listingsFor(4, 5, 6))
	s.SavePass(ctx, aPass(wid, 1600), listingsFor(7, 8, 9))

	ratio, ok, err := s.Saturation(ctx, wid, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Saturation reported no answer with three passes to read")
	}
	if ratio < 0.99 {
		t.Errorf("saturation = %v, want about 1.0 — every pass turned over "+
			"completely", ratio)
	}
}

// TestSaturationRefusesToAnswerWithoutEnoughPasses. A ratio over one pass is
// noise, and a number a person acts on must not be one.
func TestSaturationRefusesToAnswerWithoutEnoughPasses(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))

	if _, ok, err := s.Saturation(ctx, wid, 3); err != nil || ok {
		t.Errorf("Saturation answered from one pass: ok=%v err=%v", ok, err)
	}
}
```

- [x] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Series|Saturation' -v
```

Expected: FAIL to build — `undefined: ProduceSeries`.

- [x] **Step 3: Write `series.go`**

The producer walks `listing_snapshots` in id order from a watermark, groups
consecutive rows by `(item_id, surface)`, builds a `track.Reading` from each and
calls `track.Diff` on consecutive pairs. The watermark lives in `settings` under
its own key, so a second run writes nothing.

```go
// seriesWatermarkKey is how far ProduceSeries has read.
//
// It is a stored number rather than a "changes I have already written" query,
// because the second would have to scan the busiest table in the schema on
// every run, and would still be wrong for a snapshot pair that produced no
// change at all.
const seriesWatermarkKey = "series.watermark"

// ProduceSeries writes the price and title history that two snapshots imply.
//
// Out of band, and not inside the pass transaction, for two reasons. A price
// move is a property of two snapshots rather than of one window; and holding
// the single write lock while diffing every listing on a page would make the
// pass as slow as the market is busy, on a target where the pass has a budget.
func (s *Store) ProduceSeries(ctx context.Context) (int, error) { ... }
```

`Saturation` is `sum(items_seen) / sum(arrivals)` over the last N passes of a
watch, and returns `ok == false` when fewer than N admissible passes exist.

- [x] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [x] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/ && git commit -m "$(cat <<'MSG'
feat(store): the series producer, and the metric that says the interval is wrong

Two snapshots of one listing become one price-changed, out of band. Not
inside the pass transaction, for two reasons: a price move is a property
of two snapshots rather than of one window, and holding the single write
lock while diffing every listing on a page would make the pass as slow as
the market is busy — on a target where the pass has a budget.

The watermark is a stored number rather than a "what have I already
written" query. The query would scan the busiest table in the schema on
every run and would still be wrong for a snapshot pair that produced no
change at all.

Saturation is items_on_page / arrivals. If a watch of thirty slots takes
thirty new listings every pass, the window turned over completely between
polls and the watch is blind to whatever went through in between: it is
sampling the market rather than measuring it. It refuses to answer from
fewer than N passes, because a ratio over one pass is noise and this is a
number a person acts on.

MSG
)"
```

---

## Task 9: `avitomon changes`, and the increment's demonstrable behaviour

**Files:**
- Create: `cmd/avitomon/changes.go`
- Modify: `cmd/avitomon/main.go` — route `changes`

**Interfaces:**
- Consumes: `store.Changes`, `store.ProduceSeries`.

- [x] **Step 1: Write `changes.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func changesCmd(args []string) error {
	fs := flag.NewFlagSet("changes", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	watch := fs.Int64("watch", 1, "which watch")
	since := fs.Duration("since", 24*time.Hour, "how far back to read")
	limit := fs.Int("limit", 200, "how many to print")
	series := fs.Bool("series", false, "run the out-of-band series producer first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	if *series {
		n, err := s.ProduceSeries(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("series producer wrote %d changes\n\n", n)
	}

	rows, err := s.Changes(ctx, *watch, time.Now().Add(-*since).Unix(), *limit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no changes")
		return nil
	}
	for _, c := range rows {
		mark := ""
		if c.Backfill {
			mark = " [backfill]"
		}
		move := ""
		if c.Was != "" || c.Now != "" {
			move = fmt.Sprintf("  %s → %s", dash(c.Was), dash(c.Now))
		}
		fmt.Printf("%s  %-20s %-12d conf %.1f  %-8s%s%s\n",
			time.Unix(c.TS, 0).Format("2006-01-02 15:04"),
			c.Kind, c.ItemID, c.Confidence, c.Producer, move, mark)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
```

Add `case "changes": err = changesCmd(os.Args[2:])` to the router and a line to
`usage()`.

- [x] **Step 2: Run the scripted sequence**

```bash
cd S:/Projects/Avito_Go && go build ./... && go vet ./... && \
  go test ./internal/store/ ./internal/track/ ./internal/lifecycle/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS. This is the increment's first demonstrable — *a
scripted sequence of fixture pages produces exactly the right `changes` rows*.

- [x] **Step 3: Run it against the live site, twice, and read the output**

```bash
cd S:/Projects/Avito_Go && D=/tmp/inc4.db && rm -f "$D"* && \
  go run ./cmd/avitomon watch add -db "$D" "https://m.avito.ru/all?q=iphone+15&s=104" && \
  go run ./cmd/avitomon transport -db "$D" set direct && \
  go run ./cmd/avitomon run --once -db "$D" && \
  go run ./cmd/avitomon changes -db "$D" | head -5
```

Expected: thirty `listing-appeared` rows, all marked `[backfill]` — the first
pass of a watch is a seed.

Then make the watch due again and run a second pass:

```bash
cd S:/Projects/Avito_Go && python -c "
import sqlite3; c=sqlite3.connect('/tmp/inc4.db'); c.execute('UPDATE watches SET next_due_at=0'); c.commit()" && \
  go run ./cmd/avitomon run --once -db /tmp/inc4.db && \
  go run ./cmd/avitomon changes -db /tmp/inc4.db -since 1h | tail -5
```

Expected: **nothing new.** The same identity sees the same thirty listings, so
there are no arrivals and no departures. That is increment 3's volume argument
seen from the other side — as changes rather than as row counts — and if
anything appears here, either the identity did not persist or the window
producer is differencing something it should not.

- [x] **Step 4: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd/avitomon/ && git commit -m "$(cat <<'MSG'
feat(cmd): avitomon changes, and the second pass that says nothing

One line per change: when, what kind, which listing, the confidence, the
producer, the move, and whether it was backfill.

The demonstrable is the quiet one. Two live passes on one persisted
identity produce thirty backfilled arrivals and then nothing at all —
increment 3's volume argument seen from the other side, as changes
rather than as row counts. If anything appears after the second pass,
either the identity did not persist or the window producer is
differencing something it should not.

MSG
)"
```

---

## Done when

- `go build ./...` and `go vet ./...` are clean; `go test ./...` passes.
- `internal/track` and `internal/lifecycle` import no clock, no network and no database, proved by their own tests.
- The seeding pass writes arrivals marked `backfill` and confirms no disappearance.
- A listing pushed off the bottom by arrivals produces `listing-left-window` and no `listing-vanished`, and its member row is marked `displaced`.
- A listing gone from two clean passes twenty minutes apart produces `listing-vanished` at confidence 0.3 — and one confirmation alone does not.
- Two consecutive dropped-state passes produce **no** vanished rows.
- A listing that floated back is `listing-returned` and not `listing-appeared`.
- **A pass whose session differs from its predecessor's produces arrivals marked backfill and no departures.**
- `ProduceSeries` run twice writes the same change once.
- `Saturation` refuses to answer from fewer passes than it was asked for.
- `avitomon changes` after two live passes over one identity prints nothing after the first pass's backfilled arrivals.


## What increment 5 inherits

`changes` rows with a kind from a closed catalogue, a confidence, a producer and
a backfill flag — which is exactly the shape `internal/rules` matches against.
Nothing in this increment knows what a rule is, what a notification is, or that
Telegram exists.
