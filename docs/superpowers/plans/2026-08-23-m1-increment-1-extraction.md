# M1 Increment 1 — Extraction, entirely offline

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn a captured Avito page into a `Page` of typed listings, on four surfaces, with no network and no regular expressions, and prove it survives the markup changes Avito actually makes.

**Architecture:** One package, `avito/`, that imports no `net` and no `net/http` and takes its clock as a parameter — enforced by a test in the package itself, so every test in it is offline by construction. Four named extraction rules run over `golang.org/x/net/html`: `desktop.catalog.v1` reads the `data-mfe-state` JSON, `desktop.microdata.v1` reads the same page's server-rendered cards as an independent second opinion, `mobile.cards.v1` reads `window.__initialData__` plus the `item-wrapper(<id>)` cards, and `item.markers.v1` reads an item page's `data-marker` handles. Every extraction records **which rule answered** in an `ExtractionReport`, because a silent switch of path is the earliest warning this install has been classified as a bot.

**Tech Stack:** Go 1.26, no CGO. One direct dependency this increment: `golang.org/x/net` (for `html`). Standard library otherwise — `encoding/json`, `unicode`, `strings`, `compress/gzip`.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md` — read the **Extraction** section and the `listings` / `listing_snapshots` DDL before starting. This plan argues from that spec and does not repeat its reasoning.

## Global Constraints

- Module path `github.com/BlankTrail/avito-monitor`. Go directive `1.26.0`.
- **No CGO on any platform.** One machine cross-compiles every release.
- `avito/` must not import `net`, `net/http`, `time.Now`, or any database package. `TestPackageReachesNoNetwork` enforces the first two and fails the suite.
- **No regular expressions over markup, anywhere.** Not `regexp`, not `strings.Split` on a tag. Elements are resolved with `golang.org/x/net/html` and their subtree text is taken; JSON is decoded with `encoding/json`. This is an owner requirement and the reason the package exists in this shape.
- **Never `html.UnescapeString`.** `x/net/html` has already decided: `Text()` returns raw bytes for script data, `TagAttr` returns attribute values already unescaped. Unescaping either corrupts a title carrying a literal `&amp;` and there is no error anywhere when it happens.
- **Every byte kept from the tokenizer is copied.** `Text()` and `TagAttr` slices are invalid after the next `Next()`. The bug reproduces only on documents large enough for the buffer to be reused — that is, only in production.
- **Never a CSS class.** Avito ships CSS modules; every class is a stem plus a build hash. `data-marker`, `data-item-id`, `itemprop` and `itemProp` are the only permitted DOM handles.
- **Money is integer minor units beside a currency, never a float.** Avito states `priceDetailed.value` in **whole roubles**; Wildberries, whose code is being lifted elsewhere in this project, states kopecks. The conversion is one function with an overflow guard.
- `unicode.IsDigit`, never `unicode.IsNumber`. `IsNumber` accepts category No, which includes `²`, so `1 200 ₽ за м²` would read as 12002.
- Licence header on every `.go` file: `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- Commit messages: imperative subject, a body that says *why*.

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | the module. There is no task runner: WB_GO has none either, and its CI is plain `go build`, `go vet`, `go test -race` — nothing for a contributor to install. |
| `avito/doc.go` | package doc: what it refuses to import and why |
| `avito/money.go` | `Money`, `RoublesToMinor`, `DigitsToMinor` |
| `avito/model.go` | `Surface`, `Path`, `EnvelopeKind`, `PriceKind`, `Listing`, `Page` |
| `avito/report.go` | `ExtractionReport`, `Warning`, warning codes |
| `avito/document.go` | `Document`, `Check` — the encoding preconditions |
| `avito/wall.go` | wall/challenge markers and `WallHit` |
| `avito/scripts.go` | tokenizer helpers: find scripts, take subtree text, copy bytes |
| `avito/state_desktop.go` | `data-mfe-state` reader, resolver chain, shape probe |
| `avito/state_mobile.go` | `window.__initialData__` reader and its JS string-literal scan |
| `avito/dom_desktop.go` | `desktop.microdata.v1` |
| `avito/dom_mobile.go` | `mobile.cards.v1` |
| `avito/item.go` | `item.markers.v1` |
| `avito/extract.go` | `Extract` — surface dispatch, path selection, report assembly |
| `avito/testdata/` | fixtures, each `<name>.html.gz` + `<name>.meta.json` + `<name>.want.json` |
| `cmd/avitoparse/main.go` | read a fixture from disk, print `Page` + `ExtractionReport` |
| `cmd/avitocapture/main.go` | fetch one URL through a proxy and freeze it as a fixture |

`cmd/avitocapture` is the one thing here that touches the network, which is why it is a `cmd` and not part of `avito/`. It is a developer tool: it does no fingerprinting, because it fetches through a BlankTrail port that does it. The real transport is increment 2.

---

### Task 1: Module, the no-net guard, and the first fixture on disk

**Files:**
- Create: `go.mod`, `avito/doc.go`, `avito/deps_test.go`
- Create: `avito/testdata/README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: package `avito` at `github.com/BlankTrail/avito-monitor/avito`; `TestPackageReachesNoNetwork`.

- [ ] **Step 1: Write the failing test**

`avito/deps_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPackageReachesNoNetwork keeps every test in this package offline by
// construction rather than by discipline.
//
// The rule is a design decision, not a style preference: an extractor that can
// reach the network grows a test that does, and that test then fails for
// reasons that have nothing to do with the code — a captcha, a ban, somebody
// else's Wi-Fi. Worse, it stops being a test of the parser at all. Enforcing it
// here means the whole suite can run on a train.
func TestPackageReachesNoNetwork(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	banned := []string{"net/http", "net/url", "database/sql"}
	// "net" itself is matched exactly: "net/textproto" and friends are caught
	// by the prefix check below, but a package merely named "netfoo" is not a
	// violation.
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "net" {
			t.Errorf("package avito reaches %q; it must stay offline", dep)
		}
		for _, b := range banned {
			if dep == b {
				t.Errorf("package avito reaches %q; it must stay offline", dep)
			}
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestPackageReachesNoNetwork -v
```

Expected: FAIL — there is no `go.mod` and no package `avito` yet (`go: cannot find main module`).

- [ ] **Step 3: Create the module and the package**

`go.mod`:

```
module github.com/BlankTrail/avito-monitor

go 1.26.0

require golang.org/x/net v0.57.0
```

`avito/doc.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package avito turns a captured Avito page into typed listings.
//
// It imports no net and no net/http, and it takes its clock as a parameter.
// Everything it needs arrives as bytes from a caller who did the fetching, so
// every test in this package is offline by construction — a property a test in
// this package enforces rather than assumes.
//
// Two rules govern the whole package and both exist because breaking them
// fails silently. Nothing here matches a regular expression against markup:
// elements are resolved with golang.org/x/net/html and their subtree text is
// taken, and JSON is decoded with encoding/json. And nothing here calls
// html.UnescapeString: the tokenizer has already decided what is escaped and
// what is not, and a second pass corrupts any title containing a literal
// ampersand without reporting anything.
package avito
```

Then:

```bash
cd S:/Projects/Avito_Go && go mod download golang.org/x/net && go mod tidy
```

- [ ] **Step 4: Run the test and watch it pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -v
```

Expected: PASS (`TestPackageReachesNoNetwork`).

- [ ] **Step 5: Move the captured fixtures into the package**

The five pages already captured live in `docs/measurements/fixtures/`. Copy them where the tests will read them, keeping the gzip:

```bash
cd S:/Projects/Avito_Go && mkdir -p avito/testdata
cp docs/measurements/fixtures/desktop-spa-shell.html.gz      avito/testdata/
cp docs/measurements/fixtures/desktop-canonical-p1.html.gz   avito/testdata/
cp docs/measurements/fixtures/mobile-search-30.html.gz       avito/testdata/
cp docs/measurements/fixtures/mobile-search-empty.html.gz    avito/testdata/
cp docs/measurements/fixtures/desktop-seller.html.gz         avito/testdata/
```

`avito/testdata/README.md`:

```markdown
# Fixtures

Pages captured from avito.ru on 2026-08-23 and gzipped as served. Each has, or
will have, two companions:

- `<name>.meta.json` — the surface, the URL, the capture time, and the **frozen
  clock** the test runs with, so a fixture whose content has a validity window
  cannot make the suite fail on a day nobody changed anything.
- `<name>.want.json` — the whole expected `Page`, including the
  `ExtractionReport`. Pinning only the listings would let the exact regression
  this design exists to catch pass green: the same fifty listings reached by the
  DOM path instead of the state path.

They carry third-party personal data and must be scrubbed before this
repository is published. See `docs/measurements/fixtures/README.md`.
```

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add go.mod avito/ && git commit -m "$(cat <<'MSG'
feat(avito): the module, and a package that cannot reach the network

An extractor that can reach the network grows a test that does, and that test
then fails for reasons that have nothing to do with the code. Both the test and
the make target assert the same property so that a CI run which skips one still
catches it.

The five pages captured on 2026-08-23 move in beside the tests. They are the
evidence the design rests on and they are what every golden test will read.

MSG
)"
```

---

### Task 2: Money, and the rouble-to-kopeck boundary

**Files:**
- Create: `avito/money.go`, `avito/money_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Money struct { Minor int64; Currency string }`; `func RoublesToMinor(roubles int64) (int64, error)`; `func DigitsToMinor(label string) (int64, bool)`; `var ErrPriceOverflow error`.

This is the single highest-consequence function in the codebase. Avito states prices in whole roubles; Wildberries, whose code is lifted elsewhere in this project, states kopecks. A hundredfold error still sorts correctly, still renders as a plausible number, and poisons the whole price history before anyone reads a chart.

- [ ] **Step 1: Write the failing test**

`avito/money_test.go`:

```go
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
		{"a non-breaking space is what the site actually sends", "31\u00a0490 ₽", 3149000, true},
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
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestRoublesToMinor|TestDigitsToMinor' -v
```

Expected: FAIL — `undefined: RoublesToMinor`, `undefined: DigitsToMinor`, `undefined: ErrPriceOverflow`.

- [ ] **Step 3: Write the implementation**

`avito/money.go`:

```go
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
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestRoublesToMinor|TestDigitsToMinor' -v
```

Expected: PASS, all sub-tests.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/money.go avito/money_test.go && git commit -m "$(cat <<'MSG'
feat(avito): money, and the one place the rouble/kopeck boundary lives

Avito states prices in whole roubles. Wildberries, whose code this project
lifts, states kopecks. A hundredfold error sorts correctly and renders
plausibly, so nothing downstream can catch it — it is caught here or not at all.

The digit walk uses unicode.IsDigit and not unicode.IsNumber, which accepts
category No: with IsNumber "1 200 за м²" reads as 12002, and per-square-metre
pricing is ordinary on this site.

MSG
)"
```

---

### Task 3: The domain model and the extraction report

**Files:**
- Create: `avito/model.go`, `avito/report.go`, `avito/model_test.go`

**Interfaces:**
- Consumes: `Money` (Task 2).
- Produces: `Surface`, `Path`, `EnvelopeKind`, `PriceKind` and their constants; `Listing`; `Page` with `Ok()`; `ExtractionReport`; `Warning`; warning code constants.

- [ ] **Step 1: Write the failing test**

`avito/model_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// TestPageOk pins the one assertion no caller may replace with a status check.
// A 200 that carried no listings is the most expensive silent failure in this
// system: it reads as "the query matched nothing" and it is indistinguishable
// from a wall, from a dropped hydration state, and from our own parser having
// gone blind — which is exactly why the classifier is given the extraction and
// not just the body.
func TestPageOk(t *testing.T) {
	cases := []struct {
		name string
		page Page
		want bool
	}{
		{"a catalogue with listings", Page{Kind: EnvelopeCatalog, Listings: []Listing{{ItemID: 1}}}, true},
		{"a catalogue with none is not ok", Page{Kind: EnvelopeCatalog}, false},
		{"an item envelope is never ok", Page{Kind: EnvelopeItem, Listings: []Listing{{ItemID: 1}}}, false},
		{"an unknown envelope is never ok", Page{Kind: EnvelopeUnknown, Listings: []Listing{{ItemID: 1}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.page.Ok(); got != c.want {
				t.Errorf("Ok() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestReportWarn(t *testing.T) {
	var r ExtractionReport
	r.Warn(WarnShapeProbe, "items reached by structure, not by name")
	r.Warn(WarnShapeProbe, "items reached by structure, not by name")
	if len(r.Warnings) != 2 {
		t.Fatalf("Warnings = %d, want 2 — a repeated warning is two facts, "+
			"not one, and the count is what a drift report reads", len(r.Warnings))
	}
	if !r.HasWarning(WarnShapeProbe) {
		t.Error("HasWarning(WarnShapeProbe) = false after two were recorded")
	}
	if r.HasWarning(WarnDegraded) {
		t.Error("HasWarning(WarnDegraded) = true, but none was recorded")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestPageOk|TestReportWarn' -v
```

Expected: FAIL — `undefined: Page`, `undefined: ExtractionReport`.

- [ ] **Step 3: Write the model**

`avito/model.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

// Surface is which of Avito's two front ends a page came from. It is not a
// cosmetic distinction: the desktop page carries a hydration catalogue and an
// absolute publication timestamp, the mobile page carries neither, and every
// rule about admissibility of evidence is written per surface because of it.
type Surface string

const (
	SurfaceDesktop Surface = "desktop"
	SurfaceMobile  Surface = "mobile"
)

// Path is which extractor answered. It is recorded on every snapshot and it is
// required rather than diagnostic: on the desktop surface a silent switch from
// state to dom is the earliest available warning that this install has been
// classified as a bot, earlier than a 429 and much earlier than a captcha.
type Path string

const (
	PathState Path = "state" // a JSON blob embedded in the page
	PathDOM   Path = "dom"   // server-rendered elements
)

// EnvelopeKind is what the document turned out to be.
type EnvelopeKind string

const (
	EnvelopeUnknown  EnvelopeKind = ""
	EnvelopeCatalog  EnvelopeKind = "catalog"  // a search or seller page
	EnvelopeItem     EnvelopeKind = "item"     // one listing's own page
	EnvelopeRedirect EnvelopeKind = "redirect" // the SPA shell, carrying only a canonical URL
)

// PriceKind separates the seller's own statement from our failure to read one.
// Collapsing them into a null price means that the first time Avito withholds
// the hydration state, thirty listings simultaneously "become negotiable".
type PriceKind string

const (
	PriceExact      PriceKind = "exact"
	PriceNegotiable PriceKind = "negotiable" // «Цена договорная» — the seller said so
	PriceAbsent     PriceKind = "absent"     // we did not read one; our silence, not theirs
)

// Listing is one advertisement as one extractor saw it in one document.
// Pointer fields are absent-or-present on purpose: a zero would be a claim.
type Listing struct {
	ItemID          int64
	URLPath         string
	Title           string
	CategoryID      int64
	CategoryRootID  int64
	MicroCategoryID int64
	LocationID      int64
	LocationName    string
	GeoAddress      string
	Lat, Lng        *float64
	CoordsPrecise   bool

	// Absolute epoch milliseconds as the site sent them, kept in their own unit
	// so nothing has to rescale a site-supplied fact. Always nil on the mobile
	// surface: it carries no usable date, and parsing "1 час назад" would give a
	// timestamp precise enough to look trustworthy and wrong enough to order
	// arrivals incorrectly.
	SortTimeMS  *int64
	AllowTimeMS *int64

	PriceKind                 PriceKind
	Price                     Money
	PriceLabel                string // the site's own rendering, verbatim
	PriceWithoutDiscountMinor *int64
	DiscountPct               *int64
	WasLowered                bool

	SellerKey  string // "brand:<slug>" or "" when the row does not identify one
	SellerName string
	IsCompany  *bool

	Description string

	ImagesCount   *int64
	ImageURLs     []string
	IsXL          *bool // an XL promotion placement
	IsReMap       *bool
	IsVerified    *bool
	IsReserved    *bool
	IsMarketplace *bool
	HasVideo      *bool
	HasDelivery   *bool

	// Item pages only. Views are public but cost one request each.
	ViewsTotal *int64
	ViewsToday *int64

	// Item pages only. The site's own words when a posting has ended, kept
	// verbatim and uninterpreted. Its presence is the only thing permitted to
	// call a listing closed.
	ClosedReason string
}

// Page is one document, extracted.
type Page struct {
	Surface  Surface
	Kind     EnvelopeKind
	Listings []Listing

	// TotalCount is the site's own count for the whole result set:
	// catalog.totalCount on desktop, __initialData__.search.count on mobile.
	// Nil means the surface reported none, and a nil must never become
	// arithmetic on zero.
	TotalCount *int64

	// EmptyDeclared is the site saying, in its own words, that nothing matched.
	// It is what separates a real empty answer from a page we failed to read.
	EmptyDeclared bool

	// CanonicalURL is set only on EnvelopeRedirect: the SPA shell's whole
	// useful content.
	CanonicalURL string

	Report ExtractionReport
}

// Ok reports whether this page is a usable catalogue reading. No caller may
// substitute a status check for it.
func (p Page) Ok() bool {
	return p.Kind == EnvelopeCatalog && len(p.Listings) > 0
}
```

`avito/report.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

// WarnCode names a condition worth counting. Codes exist rather than free text
// because the panel aggregates these, and an aggregate over prose is a grep.
type WarnCode string

const (
	// The items array was reached by matching on structure rather than by a
	// named rule. The page's shape has moved, and this is reported before
	// anyone notices the counts are wrong.
	WarnShapeProbe WarnCode = "shape-probe"
	// The surface's primary extractor failed and a fallback answered. On
	// desktop this is the bot-classification tell.
	WarnDegraded WarnCode = "degraded"
	// The state block was found but under a script type other than the one
	// last seen. Cosmetic, recorded so a drift report can say it moved.
	WarnScriptTypeMoved WarnCode = "script-type-moved"
	// A card was refused because its two identifiers disagreed.
	WarnIDMismatch WarnCode = "id-mismatch"
	// The advisory price cross-check disagreed. Never rejects a row; a collapse
	// in the agreement rate is the unit having changed.
	WarnPriceMismatch WarnCode = "price-mismatch"
	// An item named by the page failed extraction. A partial drop, not a
	// failed page.
	WarnItemDropped WarnCode = "item-dropped"
)

// Warning is one occurrence, with enough context to act on.
type Warning struct {
	Code   WarnCode
	Detail string
}

// ExtractionReport is how the extraction went, and it is part of the golden
// fixture rather than a debugging aid. The same fifty listings reached by the
// DOM path instead of the state path is exactly the regression this design
// exists to catch, and a fixture pinning only the items would let it pass.
type ExtractionReport struct {
	Rule       string // "desktop.catalog.v1", "mobile.cards.v1", ...
	Path       Path
	ScriptType string // recorded, never required
	Found      int    // items the document named
	Dropped    int    // items named but not extractable
	Warnings   []Warning
}

// Warn records an occurrence. Repeats are kept: two are two facts, and the
// count is what a drift report reads.
func (r *ExtractionReport) Warn(code WarnCode, detail string) {
	r.Warnings = append(r.Warnings, Warning{Code: code, Detail: detail})
}

// HasWarning reports whether the code occurred at least once.
func (r *ExtractionReport) HasWarning(code WarnCode) bool {
	for _, w := range r.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -v
```

Expected: PASS, including the earlier tasks' tests.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/model.go avito/report.go avito/model_test.go && git commit -m "$(cat <<'MSG'
feat(avito): the domain model, and the report that says which path answered

Page.Ok is the assertion no caller may replace with a status check: a 200 that
carried no listings reads as "nothing matched" and is indistinguishable from a
wall, a withheld hydration state, and our own parser having gone blind.

ExtractionReport is part of the golden fixture rather than a debugging aid.
Fifty listings reached by the DOM path instead of the state path is the exact
regression this design exists to catch, and a fixture pinning only the items
would let it pass green.

PriceKind is three-valued because «Цена договорная» is the seller's statement
and an unread price is our silence. Collapsed into one null, the first page
Avito serves without its state makes thirty listings "become negotiable".

MSG
)"
```

---

### Task 4: `Document.Check` — the encoding preconditions

**Files:**
- Create: `avito/document.go`, `avito/document_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Document struct { Surface Surface; URL string; Body []byte }`; `func (d Document) Check() error`; `var ErrEmptyBody, ErrNotUTF8, ErrNoCyrillic error`.

A body that reaches a reader still compressed produces no markers and no items, and reads downstream as "the query matched nothing". It is the single most expensive silent failure in this system, and it has already cost this codebase's ancestors a debugging session.

- [ ] **Step 1: Write the failing test**

`avito/document_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
)

func TestDocumentCheck(t *testing.T) {
	good := []byte("<html><body><div>Телефоны</div></body></html>")

	// A really-compressed body, because the failure this guards against is a
	// body that arrived compressed and was read anyway. Asserting against a
	// hand-written byte slice would prove nothing.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(good); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := buf.Bytes()

	cases := []struct {
		name string
		body []byte
		want error
	}{
		{"a real page", good, nil},
		{"empty", nil, ErrEmptyBody},
		{"invalid utf-8", []byte{0xff, 0xfe, 0xfd, 0xfc}, ErrNotUTF8},
		{"valid utf-8 with no Cyrillic is not an Avito page", []byte("<html>hello</html>"), ErrNoCyrillic},
		// The one that matters. A gzip stream is usually invalid UTF-8, and
		// when it happens not to be it still has no Cyrillic — so the two
		// checks together catch it either way.
		{"still compressed", compressed, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Document{Body: c.body}.Check()
			if c.name == "still compressed" {
				if err == nil {
					t.Fatal("a compressed body passed Check; it would read " +
						"downstream as 'the query matched nothing'")
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Check() = %v, want %v", err, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestDocumentCheck -v
```

Expected: FAIL — `undefined: Document`.

- [ ] **Step 3: Write the implementation**

`avito/document.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEmptyBody  = errors.New("avito: empty body")
	ErrNotUTF8    = errors.New("avito: body is not valid UTF-8")
	ErrNoCyrillic = errors.New("avito: body carries no Cyrillic")
)

// Document is one fetched page, handed to this package by a caller who did the
// fetching, the decompressing and the decoding.
type Document struct {
	Surface Surface
	URL     string
	Body    []byte
}

// Check enforces the precondition x/net/html's own documentation states and
// does not verify: the input is assumed to be UTF-8.
//
// The Cyrillic test looks like superstition and is not. Avito serves br and
// zstd, and a body that reaches a reader still compressed produces no wall
// markers and no items — so a block reads as a healthy 200 and a parse failure
// reads as an empty market, both silently. A compressed stream is nearly always
// invalid UTF-8, and on the rare occasion it is not, it has no Cyrillic in it.
// Any page this program fetches from a Russian classified site has some.
func (d Document) Check() error {
	if len(d.Body) == 0 {
		return ErrEmptyBody
	}
	if !utf8.Valid(d.Body) {
		return ErrNotUTF8
	}
	for _, r := range string(d.Body) {
		if unicode.Is(unicode.Cyrillic, r) {
			return nil
		}
	}
	return ErrNoCyrillic
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestDocumentCheck -v
```

Expected: PASS, all sub-tests including "still compressed".

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/document.go avito/document_test.go && git commit -m "$(cat <<'MSG'
feat(avito): refuse a body that cannot be what it claims to be

x/net/html's documentation says the input is assumed UTF-8 and does not check.
Avito serves br and zstd, and a body that reaches a reader still compressed
produces no wall markers and no items: a block then reads as a healthy 200 and
a parse failure reads as an empty market, both without an error anywhere.

The Cyrillic test is the second half of that guard, for the rare compressed
stream that happens to be valid UTF-8. The test compresses a real body rather
than asserting against a hand-written slice, because the bug being guarded
against is precisely "we read the wrong bytes and did not notice".

MSG
)"
```

---

### Task 5: Wall and challenge markers

**Files:**
- Create: `avito/wall.go`, `avito/wall_test.go`

**Interfaces:**
- Consumes: `Document` (Task 4).
- Produces: `type WallHit struct { Marker, Label string }`; `func (d Document) Wall() (WallHit, bool)`; `const LabelIPBlock, LabelHardIPBlock, LabelSlider, LabelGeetest, LabelRestricted, LabelWAF, LabelRateLimit`.

- [ ] **Step 1: Write the failing test**

`avito/wall_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

func TestDocumentWall(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantLabel string
		wantHit   bool
	}{
		{"the measured IP block", "<h1>Возникла проблема с IP-адресом</h1>", LabelIPBlock, true},
		{"the hard block", "Попробуйте подключиться к другой сети", LabelHardIPBlock, true},
		{"the slider", "Продолжить для решения", LabelSlider, true},
		{"geetest by vendor name", "<script src='/gcaptcha4.js'>", LabelGeetest, true},
		{"access restricted", "Доступ ограничен", LabelRestricted, true},
		{"the WAF names itself", "powered by QRATOR", LabelWAF, true},
		{"rate limit", "Слишком много запросов", LabelRateLimit, true},
		{"an ordinary results page is not a wall", "<div>Телефоны в Москве</div>", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, ok := Document{Body: []byte(c.body)}.Wall()
			if ok != c.wantHit {
				t.Fatalf("Wall() ok = %v, want %v", ok, c.wantHit)
			}
			if ok && hit.Label != c.wantLabel {
				t.Errorf("Wall() label = %q, want %q", hit.Label, c.wantLabel)
			}
		})
	}
}

// TestWallMarkersAreCaseInsensitive pins the property that cost the Python
// tester a debugging session by its absence: markers are matched against a
// body whose case the site does not guarantee.
func TestWallMarkersAreCaseInsensitive(t *testing.T) {
	if _, ok := (Document{Body: []byte("PROBLEMA... Проблема С IP")}).Wall(); !ok {
		t.Error("a marker differing only in case was missed")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestDocumentWall|TestWallMarkers' -v
```

Expected: FAIL — `undefined: LabelIPBlock`.

- [ ] **Step 3: Write the implementation**

`avito/wall.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "strings"

// Labels for what a wall turned out to be. The transport decides what to do
// about each; this package only says which one it saw.
const (
	LabelIPBlock     = "ip-block"
	LabelHardIPBlock = "hard-ip-block"
	LabelSlider      = "slider"
	LabelGeetest     = "geetest"
	LabelRestricted  = "access-restricted"
	LabelWAF         = "waf"
	LabelRateLimit   = "rate-limit"
)

// WallHit is the marker that matched and what it means.
type WallHit struct {
	Marker string
	Label  string
}

// wallMarkers is ordered most specific first: "подключиться к другой сети" is a
// hard block and also appears on pages that carry the softer IP-block wording,
// so it must be tested before it.
var wallMarkers = []WallHit{
	{"подключиться к другой сети", LabelHardIPBlock},
	{"проблема с ip", LabelIPBlock},
	{"продолжить для решения", LabelSlider},
	{"gcaptcha4", LabelGeetest},
	{"geetest", LabelGeetest},
	{"доступ ограничен", LabelRestricted},
	{"qrator", LabelWAF},
	{"слишком много запросов", LabelRateLimit},
}

// Wall reports whether the body is a challenge or block page rather than
// content. It is deliberately independent of the HTTP status: the measured
// direct baseline is a 429 whose body carries a wall marker, and a challenge
// served as a 200 is ordinary on this target.
func (d Document) Wall() (WallHit, bool) {
	low := strings.ToLower(string(d.Body))
	for _, m := range wallMarkers {
		if strings.Contains(low, m.Marker) {
			return m, true
		}
	}
	return WallHit{}, false
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestDocumentWall|TestWallMarkers' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/wall.go avito/wall_test.go && git commit -m "$(cat <<'MSG'
feat(avito): recognise a wall by its words, not by its status

A challenge served as a 200 is ordinary on this target and the measured direct
baseline is a 429 whose body carries a wall marker, so status and wall are two
independent facts and the classifier in increment 2 needs both.

The list is ordered most specific first: the hard-block wording also appears on
pages carrying the softer one, so testing in the other order would report every
hard block as a soft one.

MSG
)"
```

---

### Task 6: Tokenizer helpers — find scripts, take subtree text, copy every byte

**Files:**
- Create: `avito/scripts.go`, `avito/scripts_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func scriptTextByAttr(body []byte, attr, value string) (text string, scriptType string, found bool)`; `func scriptTextByPrefix(body []byte, prefix string) (string, bool)`; `func subtreeText(z *html.Tokenizer, tag []byte) string`; `func attrOf(z *html.Tokenizer, name string) (string, bool)`.

These are unexported: they are the package's own tools, and every one of them exists to make a specific documented failure impossible.

- [ ] **Step 1: Write the failing test**

`avito/scripts_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strings"
	"testing"
)

func TestScriptTextByAttr(t *testing.T) {
	body := []byte(`<html><head>` +
		`<script src="/x.js"></script>` +
		`<script type="mime/invalid" data-mfe-state="true">{"a":1}</script>` +
		`</head></html>`)

	text, typ, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("the state block was not found by its data attribute")
	}
	if text != `{"a":1}` {
		t.Errorf("text = %q, want %q", text, `{"a":1}`)
	}
	if typ != "mime/invalid" {
		t.Errorf("scriptType = %q, want %q — it is recorded, never required", typ, "mime/invalid")
	}
}

// TestScriptTypeIsNotRequired pins the selection rule. The type is a deliberate
// non-MIME chosen so no browser executes the block; it is presentation trivia
// and the likeliest thing to be renamed. Requiring it would turn a cosmetic
// change into a total parse failure.
func TestScriptTypeIsNotRequired(t *testing.T) {
	body := []byte(`<script type="application/some-new-thing" data-mfe-state="true">{"a":1}</script>`)
	text, typ, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("renaming the script type broke selection; it must not")
	}
	if text != `{"a":1}` {
		t.Errorf("text = %q, want %q", text, `{"a":1}`)
	}
	if typ != "application/some-new-thing" {
		t.Errorf("scriptType = %q; the new name must be recorded so a drift "+
			"report can say it moved", typ)
	}
}

// TestScriptTextIsNeverUnescaped is the property whose absence corrupts a
// seller's title with no error anywhere. x/net/html implements the HTML5
// script-data state: Text() returns the server's exact bytes for a raw tag.
func TestScriptTextIsNeverUnescaped(t *testing.T) {
	body := []byte(`<script data-mfe-state="true">{"title":"Ремонт &amp; отделка &lt;срочно&gt;"}</script>`)
	text, _, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("not found")
	}
	if !strings.Contains(text, `&amp;`) || !strings.Contains(text, `&lt;`) {
		t.Errorf("text = %q; the entities were decoded. A JSON string holding a "+
			"literal &amp; must survive verbatim, or every seller whose title "+
			"contains an ampersand is silently corrupted", text)
	}
}

func TestScriptTextByPrefix(t *testing.T) {
	body := []byte(`<script>var noise = 1;</script>` +
		`<script>window.__initialData__ = "{}";window.other = 2;</script>`)
	text, ok := scriptTextByPrefix(body, "window.__initialData__")
	if !ok {
		t.Fatal("the prefix-selected block was not found")
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "window.__initialData__") {
		t.Errorf("text = %q, want it to start with the assignment", text)
	}
	if strings.Contains(text, "var noise") {
		t.Error("the wrong script was returned")
	}
}

// TestSubtreeTextTakesNestedText pins the mistake this helper exists to
// prevent: taking "the next N characters after the marker", which returns the
// class attribute rather than the text and is exactly the fragility the
// no-regex rule is about.
func TestSubtreeTextTakesNestedText(t *testing.T) {
	body := []byte(`<div data-marker="priceLabelGrid" class="_71fa93 _8416d9">` +
		`<span><b>49</b>&nbsp;490</span> ₽</div><div>NOT THIS</div>`)
	got := textOfMarker(body, "priceLabelGrid")
	if strings.Contains(got, "_71fa93") {
		t.Fatalf("got %q — the class attribute was returned instead of the text", got)
	}
	if strings.Contains(got, "NOT THIS") {
		t.Fatalf("got %q — the walk ran past the closing tag", got)
	}
	if !strings.Contains(got, "49") || !strings.Contains(got, "490") {
		t.Errorf("got %q, want the nested text of the whole element", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestScript|TestSubtree' -v
```

Expected: FAIL — `undefined: scriptTextByAttr`.

- [ ] **Step 3: Write the implementation**

`avito/scripts.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// attrOf returns the named attribute of the tag the tokenizer is sitting on.
// The value is copied: x/net/html documents its slices as invalid after the
// next Next(), and a reused buffer corrupts data only on documents large
// enough to trigger it — which is to say only in production.
func attrOf(z *html.Tokenizer, name string) (string, bool) {
	for {
		k, v, more := z.TagAttr()
		if string(k) == name {
			return string(v), true // string() copies
		}
		if !more {
			return "", false
		}
	}
}

// scriptTextByAttr finds the first <script> carrying attr=value and returns its
// text, together with whatever its type attribute said.
//
// Selection is on the data attribute and never on the type. The type is a
// deliberate non-MIME chosen so no browser executes the block; it is
// presentation trivia and the likeliest thing to be renamed, and requiring it
// would make a cosmetic change read as a total parse failure. It is returned so
// a drift report can say it moved.
//
// The text is returned exactly as the server sent it. Nothing here unescapes:
// x/net/html implements the HTML5 script-data state, so Text() is already the
// raw bytes, and a second pass would rewrite a literal &amp; inside a JSON
// string to & — corrupting a title with no error anywhere.
func scriptTextByAttr(body []byte, attr, value string) (text, scriptType string, found bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", "", false
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" || !hasAttr {
				continue
			}
			var typ string
			match := false
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case attr:
					if string(v) == value {
						match = true
					}
				case "type":
					typ = string(v)
				}
				if !more {
					break
				}
			}
			if !match {
				continue
			}
			if z.Next() != html.TextToken {
				return "", typ, false
			}
			return string(z.Text()), typ, true // string() copies
		}
	}
}

// scriptTextByPrefix finds the first <script> with no src whose text begins
// with the given prefix, and returns that text.
//
// The mobile surface's state block carries no attributes at all, so there is
// nothing to select on but its content. That is weaker than an attribute and it
// is recorded as such by the caller.
func scriptTextByPrefix(body []byte, prefix string) (string, bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			hasSrc := false
			if hasAttr {
				for {
					k, _, more := z.TagAttr()
					if string(k) == "src" {
						hasSrc = true
					}
					if !more {
						break
					}
				}
			}
			if hasSrc {
				continue
			}
			if z.Next() != html.TextToken {
				continue
			}
			text := string(z.Text()) // string() copies
			if strings.HasPrefix(strings.TrimSpace(text), prefix) {
				return text, true
			}
		}
	}
}

// textOfMarker returns the subtree text of the first element carrying
// data-marker=name, with runs of whitespace collapsed.
//
// It walks the element's own depth rather than reading a fixed number of bytes
// after the marker. Reading ahead N characters returns the class attribute,
// which sits between the marker and the text — that is not a hypothetical, it
// is the mistake that was made while measuring this site, and it is exactly the
// fragility the no-regex rule exists to prevent.
func textOfMarker(body []byte, name string) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			tag, hasAttr := z.TagName()
			if !hasAttr {
				continue
			}
			marker, ok := attrOf(z, "data-marker")
			if !ok || marker != name {
				continue
			}
			return subtreeText(z, append([]byte(nil), tag...))
		}
	}
}

// voidElements never have a closing tag, so they must not change the depth the
// subtree walk is counting.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// subtreeText collects the text of the element the tokenizer has just entered,
// including every nested element, stopping at that element's own closing tag.
func subtreeText(z *html.Tokenizer, tag []byte) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		switch z.Next() {
		case html.ErrorToken:
			depth = 0
		case html.StartTagToken:
			name, _ := z.TagName()
			if !voidElements[string(name)] {
				if bytes.Equal(name, tag) {
					depth++
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if bytes.Equal(name, tag) {
				depth--
			}
		case html.TextToken:
			b.Write(z.Text())
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestScript|TestSubtree' -v && go vet ./avito/
```

Expected: PASS on all five; `go vet` clean.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/scripts.go avito/scripts_test.go && git commit -m "$(cat <<'MSG'
feat(avito): tokenizer helpers, each one preventing a specific silent failure

Selection is on the data attribute and never on the script type: the type is a
deliberate non-MIME so no browser runs the block, it is the likeliest thing to
be renamed, and requiring it would turn a cosmetic change into a total parse
failure. It is returned instead, so a drift report can say it moved.

Script text is returned exactly as served. x/net/html implements the HTML5
script-data state, so a second unescaping pass rewrites a literal &amp; inside
a JSON string and corrupts a seller's title with no error anywhere.

textOfMarker walks the element's own depth. Reading a fixed number of bytes
after the marker returns the class attribute that sits between the marker and
the text — not a hypothesis, but the mistake made while measuring this site.

Every byte kept is copied: the tokenizer's slices are invalid after the next
Next(), and a reused buffer corrupts data only on documents large enough to
trigger it, which is to say only in production.

MSG
)"
```

---

### Task 7: `desktop.catalog.v1` — the hydration state, the resolver chain and the shape probe

**Files:**
- Create: `avito/state_desktop.go`, `avito/state_desktop_test.go`

**Interfaces:**
- Consumes: `Document`, `Page`, `Listing`, `ExtractionReport`, `scriptTextByAttr`, `RoublesToMinor` (Tasks 2–6).
- Produces: `func extractDesktopState(d Document) (Page, bool)`; `const RuleDesktopCatalog = "desktop.catalog.v1"`, `RuleDesktopProbe = "desktop.catalog.probe"`, `RuleDesktopRedirect = "desktop.redirect.v1"`.

- [ ] **Step 1: Write the failing test**

`avito/state_desktop_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// stateDoc wraps a JSON payload in the block shape the site actually serves.
func stateDoc(payload string) Document {
	return Document{
		Surface: SurfaceDesktop,
		Body: []byte(`<html><body><script type="mime/invalid" data-mfe-state="true">` +
			payload + `</script></body></html>`),
	}
}

func TestExtractDesktopStateNamedRule(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{
		"totalCount": 34692,
		"catalog":{"items":[
			{"id":7775028313,"urlPath":"/ekaterinburg/telefony/iphone_15_128_gb_7775028313?context=X",
			 "title":"iPhone 15, 128 ГБ","categoryId":84,"microCategoryId":1104365,
			 "locationId":654070,"sortTimeStamp":1786587948000,
			 "priceDetailed":{"value":26490,"string":"26 490","fullString":"26 490 ₽","wasLowered":false},
			 "coords":{"lat":"56.841773","lng":"60.609485"},
			 "isXl":false,"imagesCount":4}
		]}}}}`))
	if !ok {
		t.Fatal("extractDesktopState returned false on a well-formed catalogue")
	}
	if page.Report.Rule != RuleDesktopCatalog {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleDesktopCatalog)
	}
	if page.Report.Path != PathState {
		t.Errorf("Path = %q, want %q", page.Report.Path, PathState)
	}
	if page.Report.HasWarning(WarnShapeProbe) {
		t.Error("the named rule answered but a shape-probe warning was recorded")
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.URLPath != "/ekaterinburg/telefony/iphone_15_128_gb_7775028313" {
		t.Errorf("URLPath = %q; the ?context= suffix must be dropped, it is a "+
			"per-request token and keeping it would make every poll look like a "+
			"changed listing", l.URLPath)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000 — Avito states whole roubles",
			l.PriceKind, l.Price.Minor)
	}
	if l.PriceLabel != "26 490 ₽" {
		t.Errorf("PriceLabel = %q, want the site's own rendering verbatim", l.PriceLabel)
	}
	if l.SortTimeMS == nil || *l.SortTimeMS != 1786587948000 {
		t.Errorf("SortTimeMS = %v, want 1786587948000 kept in its own unit", l.SortTimeMS)
	}
	if l.Lat == nil || l.Lng == nil {
		t.Fatal("coords were not read; they are on every listing and they are free")
	}
	if page.TotalCount == nil || *page.TotalCount != 34692 {
		t.Errorf("TotalCount = %v, want 34692", page.TotalCount)
	}
}

// TestExtractDesktopStateNegotiable pins the three-valued price. An absent
// priceDetailed is the seller saying so; it is not our failure to read one.
func TestExtractDesktopStateNegotiable(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{"catalog":{"items":[
		{"id":1,"urlPath":"/x_1","title":"Диван",
		 "priceDetailed":{"value":0,"string":"","fullString":"Цена договорная","hasValue":false}}
	]}}}}`))
	if !ok {
		t.Fatal("false on a listing with no price")
	}
	if page.Listings[0].PriceKind != PriceNegotiable {
		t.Errorf("PriceKind = %q, want %q", page.Listings[0].PriceKind, PriceNegotiable)
	}
}

// TestExtractDesktopStateShapeProbe pins the last resort. A payload whose
// catalogue has moved must still be read, and the reading must be flagged,
// because a run where the probe starts answering is a page whose structure
// moved — reported before anyone notices the counts are wrong.
func TestExtractDesktopStateShapeProbe(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{"somethingNew":{"rows":[
		{"id":11,"urlPath":"/a_11","title":"A"},
		{"id":12,"urlPath":"/a_12","title":"B"}
	]}}}}`))
	if !ok {
		t.Fatal("the probe did not find an array that is obviously listings")
	}
	if page.Report.Rule != RuleDesktopProbe {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleDesktopProbe)
	}
	if !page.Report.HasWarning(WarnShapeProbe) {
		t.Error("the probe answered without warning; that is the whole point of it")
	}
	if len(page.Listings) != 2 {
		t.Errorf("Listings = %d, want 2", len(page.Listings))
	}
}

// TestExtractDesktopStateRedirect pins the SPA shell, whose only useful content
// is the canonical URL — the reason a desktop search costs two requests.
func TestExtractDesktopStateRedirect(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(
		`{"loaderData":{"data":{"status":{"code":301},"redirected":true,` +
			`"url":"/all/telefony/apple/iphone_15-ASgB?context=Y"}}}`))
	if !ok {
		t.Fatal("the shell was not recognised")
	}
	if page.Kind != EnvelopeRedirect {
		t.Fatalf("Kind = %q, want %q", page.Kind, EnvelopeRedirect)
	}
	if page.CanonicalURL != "/all/telefony/apple/iphone_15-ASgB?context=Y" {
		t.Errorf("CanonicalURL = %q; the query must be kept here, unlike on a "+
			"listing path — it is what the next fetch needs", page.CanonicalURL)
	}
	if page.Ok() {
		t.Error("a redirect shell reported Ok(); it carries no listings")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractDesktopState -v
```

Expected: FAIL — `undefined: extractDesktopState`.

- [ ] **Step 3: Write the implementation**

`avito/state_desktop.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	RuleDesktopCatalog  = "desktop.catalog.v1"
	RuleDesktopProbe    = "desktop.catalog.probe"
	RuleDesktopRedirect = "desktop.redirect.v1"
)

// mfeStateAttr is what the state block is selected by. Never the script type.
const mfeStateAttr = "data-mfe-state"

// extractDesktopState reads the one data-mfe-state block a desktop page
// carries. It serves the search page, the seller page and the SPA shell alike:
// all three use the same block, which is why one extractor covers them.
func extractDesktopState(d Document) (Page, bool) {
	text, scriptType, found := scriptTextByAttr(d.Body, mfeStateAttr, "true")
	if !found {
		return Page{}, false
	}
	var envelope struct {
		LoaderData struct {
			Data json.RawMessage `json:"data"`
		} `json:"loaderData"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return Page{}, false
	}
	page := Page{Surface: SurfaceDesktop}
	page.Report.Path = PathState
	page.Report.ScriptType = scriptType

	// The shell first: it has no catalogue and its whole content is one URL.
	var redirect struct {
		Redirected bool   `json:"redirected"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(envelope.LoaderData.Data, &redirect); err == nil && redirect.Redirected && redirect.URL != "" {
		page.Kind = EnvelopeRedirect
		page.CanonicalURL = redirect.URL
		page.Report.Rule = RuleDesktopRedirect
		return page, true
	}

	var data struct {
		TotalCount *int64 `json:"totalCount"`
		Catalog    struct {
			Items []json.RawMessage `json:"items"`
		} `json:"catalog"`
	}
	rule := RuleDesktopCatalog
	var raw []json.RawMessage
	if err := json.Unmarshal(envelope.LoaderData.Data, &data); err == nil && len(data.Catalog.Items) > 0 {
		raw = data.Catalog.Items
		page.TotalCount = data.TotalCount
	} else {
		// The named rule found nothing. Fall to the shape probe, which matches
		// on structure rather than on the name "items", and always warns.
		probe := probeForItems(envelope.LoaderData.Data, 0)
		if len(probe) == 0 {
			return Page{}, false
		}
		raw = probe
		rule = RuleDesktopProbe
		page.Report.Warn(WarnShapeProbe,
			"items reached by structure, not by desktop.catalog.v1")
	}

	page.Kind = EnvelopeCatalog
	page.Report.Rule = rule
	page.Report.Found = len(raw)
	for _, item := range raw {
		l, ok := decodeStateListing(item, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, true
}

// maxProbeDepth bounds the structural search. Six is deeper than any envelope
// observed and shallow enough that a pathological payload cannot make the
// probe the expensive part of a poll.
const maxProbeDepth = 6

// probeForItems finds an array of objects a majority of which carry a numeric
// id and a string urlPath. It matches on shape and never on the key's name,
// because the name is the thing that moved.
func probeForItems(raw json.RawMessage, depth int) []json.RawMessage {
	if depth > maxProbeDepth {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		hits := 0
		for _, e := range arr {
			var probe struct {
				ID      *int64  `json:"id"`
				URLPath *string `json:"urlPath"`
			}
			if json.Unmarshal(e, &probe) == nil && probe.ID != nil && probe.URLPath != nil {
				hits++
			}
		}
		if hits*2 > len(arr) {
			return arr
		}
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	for _, v := range obj {
		if got := probeForItems(v, depth+1); got != nil {
			return got
		}
	}
	return nil
}

// stateItem mirrors the fields measured on every listing of a desktop page.
type stateItem struct {
	ID              int64  `json:"id"`
	URLPath         string `json:"urlPath"`
	Title           string `json:"title"`
	CategoryID      int64  `json:"categoryId"`
	MicroCategoryID int64  `json:"microCategoryId"`
	LocationID      int64  `json:"locationId"`
	SortTimeStamp   *int64 `json:"sortTimeStamp"`
	AllowTimeStamp  *int64 `json:"allowTimeStamp"`
	Category        struct {
		RootID int64 `json:"rootId"`
	} `json:"category"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Geo struct {
		FormattedAddress string `json:"formattedAddress"`
	} `json:"geo"`
	Coords struct {
		Lat       string `json:"lat"`
		Lng       string `json:"lng"`
		Precision int    `json:"precision"`
	} `json:"coords"`
	PriceDetailed *struct {
		Value      int64  `json:"value"`
		String     string `json:"string"`
		FullString string `json:"fullString"`
		HasValue   *bool  `json:"hasValue"`
		WasLowered bool   `json:"wasLowered"`
	} `json:"priceDetailed"`
	PriceWithoutDiscount *int64 `json:"priceWithoutDiscount"`
	DiscountPercent      *int64 `json:"discountPercent"`
	ImagesCount          *int64 `json:"imagesCount"`
	IsXL                 *bool  `json:"isXl"`
	IsReMapPromotion     *bool  `json:"isReMapPromotion"`
	IsVerifiedItem       *bool  `json:"isVerifiedItem"`
	IsReserved           *bool  `json:"isReserved"`
	IsMarketplace        *bool  `json:"isMarketplace"`
	HasVideo             *bool  `json:"hasVideo"`
	Contacts             *struct {
		Delivery bool `json:"delivery"`
	} `json:"contacts"`
	UserLogo *struct {
		Link string `json:"link"`
	} `json:"userLogo"`
	IVA *struct {
		DescriptionStep []struct {
			Payload struct {
				Description string `json:"description"`
			} `json:"payload"`
		} `json:"DescriptionStep"`
		SecondLineStep []struct {
			Payload struct {
				Value string `json:"value"`
			} `json:"payload"`
		} `json:"SecondLineStep"`
	} `json:"iva"`
}

func decodeStateListing(raw json.RawMessage, rep *ExtractionReport) (Listing, bool) {
	var it stateItem
	if err := json.Unmarshal(raw, &it); err != nil {
		rep.Warn(WarnItemDropped, "json: "+err.Error())
		return Listing{}, false
	}
	if it.ID == 0 || it.URLPath == "" {
		rep.Warn(WarnItemDropped, "no id or no urlPath")
		return Listing{}, false
	}
	l := Listing{
		ItemID: it.ID,
		// The ?context= suffix is a per-request token. Keeping it would make
		// every poll look like a changed listing.
		URLPath:         trimQuery(it.URLPath),
		Title:           it.Title,
		CategoryID:      it.CategoryID,
		CategoryRootID:  it.Category.RootID,
		MicroCategoryID: it.MicroCategoryID,
		LocationID:      it.LocationID,
		LocationName:    it.Location.Name,
		GeoAddress:      it.Geo.FormattedAddress,
		CoordsPrecise:   it.Coords.Precision == 0,
		SortTimeMS:      it.SortTimeStamp,
		AllowTimeMS:     it.AllowTimeStamp,
		PriceKind:       PriceNegotiable,
		Price:           Money{Currency: "RUB"},
		ImagesCount:     it.ImagesCount,
		IsXL:            it.IsXL,
		IsReMap:         it.IsReMapPromotion,
		IsVerified:      it.IsVerifiedItem,
		IsReserved:      it.IsReserved,
		IsMarketplace:   it.IsMarketplace,
		HasVideo:        it.HasVideo,
	}
	if f, err := strconv.ParseFloat(it.Coords.Lat, 64); err == nil {
		l.Lat = &f
	}
	if f, err := strconv.ParseFloat(it.Coords.Lng, 64); err == nil {
		l.Lng = &f
	}
	if it.Contacts != nil {
		d := it.Contacts.Delivery
		l.HasDelivery = &d
	}
	if it.UserLogo != nil && it.UserLogo.Link != "" {
		if slug := brandSlug(it.UserLogo.Link); slug != "" {
			l.SellerKey = "brand:" + slug
		}
	}
	if it.IVA != nil {
		if len(it.IVA.DescriptionStep) > 0 {
			l.Description = it.IVA.DescriptionStep[0].Payload.Description
		}
		if len(it.IVA.SecondLineStep) > 0 {
			v := it.IVA.SecondLineStep[0].Payload.Value
			if v == "Компания" {
				yes := true
				l.IsCompany = &yes
			}
		}
	}
	if pd := it.PriceDetailed; pd != nil {
		l.PriceLabel = pd.FullString
		l.WasLowered = pd.WasLowered
		hasValue := pd.HasValue == nil || *pd.HasValue
		if hasValue && pd.Value > 0 {
			if minor, err := RoublesToMinor(pd.Value); err == nil {
				l.PriceKind = PriceExact
				l.Price.Minor = minor
				// Advisory only: it counts and never rejects. A collapse in the
				// agreement rate is the unit having changed.
				if d, ok := DigitsToMinor(pd.String); ok && d != minor {
					rep.Warn(WarnPriceMismatch, pd.String)
				}
			}
		}
		l.PriceWithoutDiscountMinor = minorOrNil(it.PriceWithoutDiscount)
		l.DiscountPct = it.DiscountPercent
	}
	return l, true
}

func minorOrNil(roubles *int64) *int64 {
	if roubles == nil {
		return nil
	}
	m, err := RoublesToMinor(*roubles)
	if err != nil {
		return nil
	}
	return &m
}

// trimQuery drops the query and the fragment from a listing path.
func trimQuery(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		return p[:i]
	}
	return p
}

// brandSlug reads the slug out of "/brands/<slug>?...".
func brandSlug(link string) string {
	const prefix = "/brands/"
	if !strings.HasPrefix(link, prefix) {
		return ""
	}
	return trimQuery(strings.TrimPrefix(link, prefix))
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractDesktopState -v && go vet ./avito/
```

Expected: PASS on all four; `go vet` clean.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/state_desktop.go avito/state_desktop_test.go && git commit -m "$(cat <<'MSG'
feat(avito): read the desktop hydration state, and say which rule answered

One block serves the search page, the seller page and the SPA shell, which is
why one extractor covers all three. The shell is recognised first: it has no
catalogue and its whole useful content is the canonical URL, and that is the
reason a desktop search costs two requests.

The shape probe matches on structure — an array of objects a majority of which
carry a numeric id and a string urlPath — rather than on the name "items",
because the name is the thing that moves. It always warns: a run where the
probe starts answering is a page whose structure changed, reported before
anyone notices the counts are wrong.

The ?context= suffix is dropped from every listing path. It is a per-request
token, and keeping it would make every poll look like a changed listing.

The price cross-check counts and never rejects. A row refused for disagreeing
with itself is a row lost; a collapse in the agreement rate is the unit having
changed, and that is what the counter is for.

MSG
)"
```

---

### Task 8: `desktop.microdata.v1` — the independent second opinion

**Files:**
- Create: `avito/dom_desktop.go`, `avito/dom_desktop_test.go`

**Interfaces:**
- Consumes: `Document`, `Page`, `textOfMarker`, `attrOf`, `DigitsToMinor`.
- Produces: `func extractDesktopDOM(d Document) (Page, bool)`; `const RuleDesktopMicrodata = "desktop.microdata.v1"`.

- [ ] **Step 1: Write the failing test**

`avito/dom_desktop_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strings"
	"testing"
)

const desktopCard = `<div data-marker="item" data-item-id="7775028313" ` +
	`class="iva-item-root-Gz1qE" itemtype="http://schema.org/Product">` +
	`<a itemprop="url" href="/ekaterinburg/telefony/iphone_15_128_gb_7775028313?context=X">` +
	`<span data-marker="item-title">iPhone 15, 128 ГБ</span></a>` +
	`<meta itemprop="price" content="26490"/>` +
	`<p data-marker="item-price"><strong>26 490 ₽</strong></p>` +
	`<div data-marker="item-address">Екатеринбург</div>` +
	`</div>`

func TestExtractDesktopDOM(t *testing.T) {
	page, ok := extractDesktopDOM(Document{Surface: SurfaceDesktop,
		Body: []byte("<html><body>" + desktopCard + "</body></html>")})
	if !ok {
		t.Fatal("extractDesktopDOM returned false on a well-formed card")
	}
	if page.Report.Rule != RuleDesktopMicrodata || page.Report.Path != PathDOM {
		t.Errorf("Rule/Path = %q/%q, want %q/%q",
			page.Report.Rule, page.Report.Path, RuleDesktopMicrodata, PathDOM)
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.Title != "iPhone 15, 128 ГБ" {
		t.Errorf("Title = %q", l.Title)
	}
	if l.URLPath != "/ekaterinburg/telefony/iphone_15_128_gb_7775028313" {
		t.Errorf("URLPath = %q, want the query dropped", l.URLPath)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000", l.PriceKind, l.Price.Minor)
	}
}

// TestDesktopDOMIgnoresClasses is the robustness claim, proved rather than
// asserted. Avito ships CSS modules: every class is a stem plus a build hash,
// so a selector on a class breaks on a deploy and returns zero cards — and
// zero cards is exactly what a new-listing monitor reports on a quiet market.
func TestDesktopDOMIgnoresClasses(t *testing.T) {
	rehashed := strings.ReplaceAll(desktopCard, "iva-item-root-Gz1qE", "totally-different-YLctS")
	page, ok := extractDesktopDOM(Document{Surface: SurfaceDesktop, Body: []byte(rehashed)})
	if !ok || len(page.Listings) != 1 {
		t.Fatal("rehashing the class changed the result; no extractor may depend on a class")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractDesktopDOM -v
```

Expected: FAIL — `undefined: extractDesktopDOM`.

- [ ] **Step 3: Write the implementation**

`avito/dom_desktop.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strconv"

	"golang.org/x/net/html"
)

const RuleDesktopMicrodata = "desktop.microdata.v1"

// extractDesktopDOM reads the fifty server-rendered cards a desktop search page
// carries beside its hydration state.
//
// It is an independent second opinion over the same bytes, and it exists
// because loaderData.toggles carries runtime_catalog_drop_state_for_bots: Avito
// can serve a page with its state removed to a client it has classified. A
// parser with one path would go quiet and look healthy.
func extractDesktopDOM(d Document) (Page, bool) {
	page := Page{Surface: SurfaceDesktop, Kind: EnvelopeCatalog}
	page.Report.Rule = RuleDesktopMicrodata
	page.Report.Path = PathDOM

	z := html.NewTokenizer(bytes.NewReader(d.Body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken {
			continue
		}
		tag, hasAttr := z.TagName()
		if !hasAttr {
			continue
		}
		// Attributes must be read in one pass: TagAttr walks a cursor.
		var marker, itemID string
		for {
			k, v, more := z.TagAttr()
			switch string(k) {
			case "data-marker":
				marker = string(v)
			case "data-item-id":
				itemID = string(v)
			}
			if !more {
				break
			}
		}
		if marker != "item" {
			continue
		}
		page.Report.Found++
		inner := subtreeHTML(z, append([]byte(nil), tag...))
		l, ok := decodeDesktopCard(itemID, inner, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, len(page.Listings) > 0
}

// subtreeHTML re-serialises the element the tokenizer has just entered, so the
// card can be walked again for its own markers without re-scanning the page.
func subtreeHTML(z *html.Tokenizer, tag []byte) []byte {
	var buf bytes.Buffer
	depth := 1
	for depth > 0 {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		buf.Write(raw) // Write copies
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			if bytes.Equal(name, tag) && !voidElements[string(name)] {
				depth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if bytes.Equal(name, tag) {
				depth--
			}
		}
	}
	return buf.Bytes()
}

func decodeDesktopCard(itemID string, inner []byte, rep *ExtractionReport) (Listing, bool) {
	id, err := strconv.ParseInt(itemID, 10, 64)
	if err != nil || id == 0 {
		rep.Warn(WarnItemDropped, "data-item-id is not a number: "+itemID)
		return Listing{}, false
	}
	l := Listing{
		ItemID:    id,
		Title:     textOfMarker(inner, "item-title"),
		PriceKind: PriceNegotiable,
		Price:     Money{Currency: "RUB"},
	}
	if href, ok := attrOfItemprop(inner, "url", "href"); ok {
		l.URLPath = trimQuery(href)
	}
	l.LocationName = textOfMarker(inner, "item-address")
	l.PriceLabel = textOfMarker(inner, "item-price")
	// itemprop="price" carries the number without the site's formatting, which
	// is the better source when it is there; the label is the fallback.
	if content, ok := attrOfItemprop(inner, "price", "content"); ok {
		if roubles, err := strconv.ParseInt(content, 10, 64); err == nil {
			if minor, err := RoublesToMinor(roubles); err == nil {
				l.PriceKind = PriceExact
				l.Price.Minor = minor
			}
		}
	}
	if l.PriceKind != PriceExact {
		if minor, ok := DigitsToMinor(l.PriceLabel); ok {
			l.PriceKind = PriceExact
			l.Price.Minor = minor
		}
	}
	if l.URLPath == "" && l.Title == "" {
		rep.Warn(WarnItemDropped, "card carried neither a url nor a title")
		return Listing{}, false
	}
	return l, true
}

// attrOfItemprop finds the first element whose itemprop (either capitalisation
// the site uses) is name, and returns its want attribute.
func attrOfItemprop(body []byte, name, want string) (string, bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return "", false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		if _, hasAttr := z.TagName(); !hasAttr {
			continue
		}
		var prop, got string
		found := false
		for {
			k, v, more := z.TagAttr()
			switch string(k) {
			case "itemprop", "itemProp":
				prop = string(v)
			case want:
				got = string(v)
				found = true
			}
			if !more {
				break
			}
		}
		if prop == name && found {
			return got, true
		}
	}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractDesktopDOM -v && go vet ./avito/
```

Expected: PASS on both.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/dom_desktop.go avito/dom_desktop_test.go && git commit -m "$(cat <<'MSG'
feat(avito): the desktop microdata path, an independent second opinion

loaderData.toggles carries runtime_catalog_drop_state_for_bots: Avito can serve
a page with its hydration state removed to a client it has classified. A parser
with one path would then return zero listings, and zero listings is exactly
what a new-listing monitor reports on a quiet market — it would go silent and
look healthy.

The rehash test proves the claim rather than asserting it. Avito ships CSS
modules, so a selector on a class breaks on an ordinary deploy and returns
nothing, silently. data-marker is different in kind: it exists for Avito's own
end-to-end tests, so renaming one breaks their CI before it breaks ours.

MSG
)"
```

---

### Task 9: `mobile.cards.v1` — `window.__initialData__` and the card list

**Files:**
- Create: `avito/state_mobile.go`, `avito/dom_mobile.go`, `avito/mobile_test.go`

**Interfaces:**
- Consumes: `scriptTextByPrefix`, `textOfMarker`, `subtreeHTML`, `DigitsToMinor`.
- Produces: `func firstJSString(s string) (string, bool)`; `func extractMobile(d Document) (Page, bool)`; `const RuleMobileCards = "mobile.cards.v1"`.

The mobile surface is the default collection surface, so this is the extractor that runs most. It has no hydration catalogue: `dom` here is the primary path and not a degradation.

- [ ] **Step 1: Write the failing test**

`avito/mobile_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// The literal continues with further statements after the string, which is why
// the scan must find the end of the string rather than the end of the script.
const mobileState = `<script>window.__initialData__ = ` +
	`"{\"search\":{\"count\":59959,\"mainCount\":59959}}";window.other = 2;</script>`

const mobileCard = `<div data-marker="item-wrapper(7831893581)" itemtype="http://schema.org/Product">` +
	`<div data-marker="titleLabelGrid">iPhone 15 Pro, 256 ГБ</div>` +
	`<div data-marker="priceLabelGrid">49 490 ₽</div>` +
	`<div data-marker="itemParamsGrid">Новый</div>` +
	`<div data-marker="addressLabel">Республика Татарстан, Казань</div>` +
	`<div data-marker="sellerAndRatingSellerLabel">TorStore Kazan</div>` +
	`<a data-marker="item/link" itemProp="url" data-marker-id="7831893581" ` +
	`href="/kazan/telefony/iphone_15_pro_256_gb_7831893581?context=Z"></a>` +
	`<meta itemProp="description" content="Отличное состояние"/>` +
	`</div>`

func TestFirstJSString(t *testing.T) {
	got, ok := firstJSString(`window.x = "a\"b\\" ; window.y = "second";`)
	if !ok {
		t.Fatal("no string literal found")
	}
	if got != `"a\"b\\"` {
		t.Errorf("got %q, want the first literal with its escapes intact "+
			"— an escaped quote must not end the scan", got)
	}
}

func TestExtractMobile(t *testing.T) {
	page, ok := extractMobile(Document{Surface: SurfaceMobile,
		Body: []byte("<html><body>" + mobileState + mobileCard + "</body></html>")})
	if !ok {
		t.Fatal("extractMobile returned false on a well-formed page")
	}
	if page.Report.Rule != RuleMobileCards || page.Report.Path != PathDOM {
		t.Errorf("Rule/Path = %q/%q, want %q/%q",
			page.Report.Rule, page.Report.Path, RuleMobileCards, PathDOM)
	}
	// dom is the mobile surface's primary path. It is not a degradation and
	// must never be reported as one.
	if page.Report.HasWarning(WarnDegraded) {
		t.Error("a healthy mobile page was marked degraded")
	}
	if page.TotalCount == nil || *page.TotalCount != 59959 {
		t.Fatalf("TotalCount = %v, want 59959 from __initialData__.search.count "+
			"— it is what the too_broad guard reads on this surface", page.TotalCount)
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7831893581 {
		t.Errorf("ItemID = %d, want 7831893581", l.ItemID)
	}
	if l.Title != "iPhone 15 Pro, 256 ГБ" {
		t.Errorf("Title = %q", l.Title)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 4949000 {
		t.Errorf("price = %v %d, want exact 4949000", l.PriceKind, l.Price.Minor)
	}
	if l.SellerName != "TorStore Kazan" {
		t.Errorf("SellerName = %q", l.SellerName)
	}
	if l.Description != "Отличное состояние" {
		t.Errorf("Description = %q", l.Description)
	}
	// The measurement is unambiguous: parsing "1 час назад" would give a
	// timestamp precise enough to look trustworthy and wrong enough to order
	// arrivals incorrectly, so it is not parsed at all.
	if l.SortTimeMS != nil {
		t.Errorf("SortTimeMS = %v, want nil on the mobile surface", *l.SortTimeMS)
	}
	// This surface has no priceDetailed, so there is no second number to
	// disagree with and the advisory counter must stay untouched.
	if page.Report.HasWarning(WarnPriceMismatch) {
		t.Error("a price cross-check was recorded on a surface that cannot do one")
	}
}

// TestExtractMobileEmpty pins the distinction the classifier needs: the site
// saying nothing matched, versus a page we failed to read.
func TestExtractMobileEmpty(t *testing.T) {
	body := `<script>window.__initialData__ = ` +
		`"{\"search\":{\"count\":0,\"emptySearch\":\"Ничего не найдено по запросу\"}}";</script>`
	page, ok := extractMobile(Document{Surface: SurfaceMobile, Body: []byte(body)})
	if !ok {
		t.Fatal("an empty result set must extract successfully; it is an answer")
	}
	if !page.EmptyDeclared {
		t.Error("EmptyDeclared = false; the site said so in its own words")
	}
	if page.TotalCount == nil || *page.TotalCount != 0 {
		t.Errorf("TotalCount = %v, want 0", page.TotalCount)
	}
	if page.Ok() {
		t.Error("Ok() = true on a page with no listings")
	}
}

// TestExtractMobileIDMismatch pins the cross-check. Two identifiers that
// disagree mean the card is not what it says it is.
func TestExtractMobileIDMismatch(t *testing.T) {
	bad := `<div data-marker="item-wrapper(111)">` +
		`<div data-marker="titleLabelGrid">X</div>` +
		`<a data-marker="item/link" data-marker-id="222" href="/x_222"></a></div>`
	page, _ := extractMobile(Document{Surface: SurfaceMobile,
		Body: []byte(mobileState + bad)})
	if len(page.Listings) != 0 {
		t.Errorf("Listings = %d, want 0 — the two ids disagreed", len(page.Listings))
	}
	if !page.Report.HasWarning(WarnIDMismatch) {
		t.Error("no id-mismatch warning was recorded")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestFirstJSString|TestExtractMobile' -v
```

Expected: FAIL — `undefined: firstJSString`, `undefined: extractMobile`.

- [ ] **Step 3: Write the two files**

`avito/state_mobile.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"encoding/json"
	"strings"
)

// initialDataPrefix is what the mobile state block's text begins with. The
// block carries no attributes at all, so its content is the only handle there
// is — weaker than an attribute, and the caller records that it was used.
const initialDataPrefix = "window.__initialData__"

// mobileState is the part of __initialData__ this package reads. The listings
// are NOT in it — measured: urlPath and priceDetailed appear zero times — they
// are the server-rendered cards. What is here is what the classifier needs.
type mobileState struct {
	Search struct {
		Count       *int64 `json:"count"`
		MainCount   *int64 `json:"mainCount"`
		EmptySearch string `json:"emptySearch"`
	} `json:"search"`
}

// firstJSString returns the first double-quoted JavaScript string literal in s,
// with its quotes and escapes intact.
//
// A two-state escape-aware scan, and deliberately neither a regex nor a split
// on the quote: the block continues with further statements after the literal,
// and an escaped quote inside the JSON must not end it.
func firstJSString(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	esc := false
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if esc {
			esc = false
			continue
		}
		switch c {
		case '\\':
			esc = true
		case '"':
			return s[i : j+1], true
		}
	}
	return "", false
}

// readMobileState finds and decodes window.__initialData__. It is a JS string
// literal holding JSON text, so it unmarshals twice.
func readMobileState(body []byte) (mobileState, bool) {
	text, ok := scriptTextByPrefix(body, initialDataPrefix)
	if !ok {
		return mobileState{}, false
	}
	lit, ok := firstJSString(text)
	if !ok {
		return mobileState{}, false
	}
	var inner string
	if err := json.Unmarshal([]byte(lit), &inner); err != nil {
		return mobileState{}, false
	}
	var st mobileState
	if err := json.Unmarshal([]byte(inner), &st); err != nil {
		return mobileState{}, false
	}
	return st, true
}
```

`avito/dom_mobile.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const RuleMobileCards = "mobile.cards.v1"

// negotiableLabel is the seller's own words for a price they will discuss.
const negotiableLabel = "Цена договорная"

// extractMobile reads the default collection surface.
//
// Two readers run and both are required: window.__initialData__ for the count
// and the site's own empty-result wording, and the item-wrapper cards for the
// listings. The state block carries no listings — measured — and the cards
// carry no count, so neither alone is a reading.
func extractMobile(d Document) (Page, bool) {
	page := Page{Surface: SurfaceMobile, Kind: EnvelopeCatalog}
	page.Report.Rule = RuleMobileCards
	page.Report.Path = PathDOM

	st, haveState := readMobileState(d.Body)
	if !haveState {
		// No container at all. The caller's classifier reads this as a soft
		// block; it is not our business to decide that here.
		return Page{}, false
	}
	if st.Search.Count != nil {
		page.TotalCount = st.Search.Count
	} else if st.Search.MainCount != nil {
		page.TotalCount = st.Search.MainCount
	}
	page.EmptyDeclared = st.Search.EmptySearch != ""

	z := html.NewTokenizer(bytes.NewReader(d.Body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken {
			continue
		}
		tag, hasAttr := z.TagName()
		if !hasAttr {
			continue
		}
		marker, ok := attrOf(z, "data-marker")
		if !ok {
			continue
		}
		id, ok := wrapperID(marker)
		if !ok {
			continue
		}
		page.Report.Found++
		inner := subtreeHTML(z, append([]byte(nil), tag...))
		l, ok := decodeMobileCard(id, inner, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, true
}

// wrapperID reads 7831893581 out of "item-wrapper(7831893581)".
func wrapperID(marker string) (int64, bool) {
	const prefix = "item-wrapper("
	if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, ")") {
		return 0, false
	}
	id, err := strconv.ParseInt(marker[len(prefix):len(marker)-1], 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func decodeMobileCard(id int64, inner []byte, rep *ExtractionReport) (Listing, bool) {
	// The id appears twice on this surface. Two identifiers that disagree mean
	// the card is not what it says it is, and a wrong id is worse than a
	// missing one: it attaches this reading to somebody else's history.
	if second, ok := attrOfMarker(inner, "item/link", "data-marker-id"); ok {
		if got, err := strconv.ParseInt(second, 10, 64); err == nil && got != id {
			rep.Warn(WarnIDMismatch,
				strconv.FormatInt(id, 10)+" vs "+strconv.FormatInt(got, 10))
			return Listing{}, false
		}
	}
	l := Listing{
		ItemID:       id,
		Title:        textOfMarker(inner, "titleLabelGrid"),
		LocationName: textOfMarker(inner, "addressLabel"),
		SellerName:   textOfMarker(inner, "sellerAndRatingSellerLabel"),
		PriceLabel:   textOfMarker(inner, "priceLabelGrid"),
		PriceKind:    PriceAbsent,
		Price:        Money{Currency: "RUB"},
		// SortTimeMS stays nil. Measured: without s=104 there is no date on the
		// page at all, and with it every card read "1 час назад" — a resolution
		// of about an hour on a window that turns over inside it. A parsed value
		// would be precise enough to look trustworthy and wrong enough to order
		// arrivals incorrectly.
	}
	if href, ok := attrOfMarker(inner, "item/link", "href"); ok {
		l.URLPath = trimQuery(href)
	}
	if desc, ok := attrOfItemprop(inner, "description", "content"); ok {
		l.Description = desc
	}
	switch {
	case l.PriceLabel == "":
		l.PriceKind = PriceAbsent
	case strings.Contains(l.PriceLabel, negotiableLabel):
		l.PriceKind = PriceNegotiable
	default:
		if minor, ok := DigitsToMinor(l.PriceLabel); ok {
			l.PriceKind = PriceExact
			l.Price.Minor = minor
		} else {
			l.PriceKind = PriceNegotiable
		}
	}
	// No priceDetailed on this surface, so the advisory cross-check does not
	// run and its counter is not touched: a surface that cannot disagree with
	// itself must not be recorded as agreeing.
	if l.URLPath == "" && l.Title == "" {
		rep.Warn(WarnItemDropped, "card carried neither a url nor a title")
		return Listing{}, false
	}
	return l, true
}

// attrOfMarker returns the want attribute of the first element whose
// data-marker is name.
func attrOfMarker(body []byte, name, want string) (string, bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return "", false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		if _, hasAttr := z.TagName(); !hasAttr {
			continue
		}
		var marker, got string
		found := false
		for {
			k, v, more := z.TagAttr()
			switch string(k) {
			case "data-marker":
				marker = string(v)
			case want:
				got = string(v)
				found = true
			}
			if !more {
				break
			}
		}
		if marker == name && found {
			return got, true
		}
	}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'TestFirstJSString|TestExtractMobile' -v && go vet ./avito/
```

Expected: PASS on all four.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/state_mobile.go avito/dom_mobile.go avito/mobile_test.go && git commit -m "$(cat <<'MSG'
feat(avito): the mobile extractor, which is the one that runs most

Mobile is the default collection surface: one request per page instead of two
and half the bytes. It carries no data-mfe-state block, so dom here is the
primary path and not a degradation, and it must never be reported as one.

Two readers, both required. window.__initialData__ gives search.count — which
is what the too_broad guard reads on this surface — and search.emptySearch,
which is what makes a legitimately empty result set distinguishable from a soft
block. The cards give the listings; the state block carries none.

The string literal is read by an escape-aware scan rather than a split on the
quote, because the block continues with further statements after it.

SortTimeMS stays nil, deliberately. Without s=104 the mobile page has no date
at all; with it, all thirty cards read "1 час назад". A value parsed from that
would be precise enough to look trustworthy and wrong enough to order arrivals
incorrectly.

MSG
)"
```

---

### Task 10: `item.markers.v1` — the item page

**Files:**
- Create: `avito/item.go`, `avito/item_test.go`

**Interfaces:**
- Consumes: `textOfMarker` (Task 6), `DigitsToMinor` (Task 2), `negotiableLabel` (Task 9).
- Produces: `func extractItem(d Document) (Page, bool)`; `func DigitsToInt(label string) (int64, bool)`; `const RuleItemMarkers = "item.markers.v1"`.

This is what turns the design's «предположительно» removal guess into a stated fact, so it is not optional and it is not later.

- [ ] **Step 1: Write the failing test**

`avito/item_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

const itemPage = `<html><body>` +
	`<h1 data-marker="item-view/title-info">iPhone 15, 128 ГБ</h1>` +
	`<span data-marker="item-view/item-id">№ 7775028313</span>` +
	`<span data-marker="item-view/item-price">26 490 ₽</span>` +
	`<span data-marker="item-view/item-date">сегодня в 12:20</span>` +
	`<div data-marker="item-view/item-description">Новый, с гарантией</div>` +
	`<span data-marker="item-view/total-views">1 284 просмотра</span>` +
	`<span data-marker="item-view/today-views">42 сегодня</span>` +
	`</body></html>`

func TestExtractItem(t *testing.T) {
	page, ok := extractItem(Document{Surface: SurfaceDesktop, Body: []byte(itemPage)})
	if !ok {
		t.Fatal("extractItem returned false on a well-formed item page")
	}
	if page.Kind != EnvelopeItem {
		t.Errorf("Kind = %q, want %q", page.Kind, EnvelopeItem)
	}
	if page.Report.Rule != RuleItemMarkers {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleItemMarkers)
	}
	if page.Ok() {
		t.Error("Ok() = true on an item envelope; it asserts a catalogue")
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want exactly 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000 — the rendered label is the "+
			"only price source this surface has", l.PriceKind, l.Price.Minor)
	}
	if l.Description != "Новый, с гарантией" {
		t.Errorf("Description = %q", l.Description)
	}
	if l.ViewsTotal == nil || *l.ViewsTotal != 1284 {
		t.Errorf("ViewsTotal = %v, want 1284", l.ViewsTotal)
	}
	if l.ViewsToday == nil || *l.ViewsToday != 42 {
		t.Errorf("ViewsToday = %v, want 42", l.ViewsToday)
	}
	if l.ClosedReason != "" {
		t.Errorf("ClosedReason = %q on a live posting", l.ClosedReason)
	}
}

// TestExtractItemClosed pins the only thing permitted to call a listing closed:
// the site's own words, stored verbatim and uninterpreted.
func TestExtractItemClosed(t *testing.T) {
	closed := `<div data-marker="item-view/item-id">№ 99</div>` +
		`<div data-marker="item-view/closed-warning">Объявление снято с публикации</div>`
	page, ok := extractItem(Document{Surface: SurfaceDesktop, Body: []byte(closed)})
	if !ok {
		t.Fatal("false on a closed posting; a closed posting is a reading")
	}
	if got := page.Listings[0].ClosedReason; got != "Объявление снято с публикации" {
		t.Errorf("ClosedReason = %q, want the site's words verbatim", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractItem -v
```

Expected: FAIL — `undefined: extractItem`.

- [ ] **Step 3: Write the implementation**

`avito/item.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "strings"

const RuleItemMarkers = "item.markers.v1"

// Item-page handles. There is no ld+json and no itemprop="price" on this
// surface — measured — so data-marker is all there is.
const (
	markerItemID      = "item-view/item-id"
	markerItemTitle   = "item-view/title-info"
	markerItemPrice   = "item-view/item-price"
	markerItemDesc    = "item-view/item-description"
	markerItemParams  = "item-view/item-params"
	markerViewsTotal  = "item-view/total-views"
	markerViewsToday  = "item-view/today-views"
	markerItemClosed  = "item-view/closed-warning"
)

// extractItem reads one listing's own page.
//
// It exists in this increment rather than a later one because it is what turns
// a windowed disappearance guess into a stated fact: the confirmation sweeper
// has no other source, and without it every removal alert stays at confidence
// 0.3 for ever.
func extractItem(d Document) (Page, bool) {
	page := Page{Surface: d.Surface, Kind: EnvelopeItem}
	page.Report.Rule = RuleItemMarkers
	page.Report.Path = PathDOM

	id, ok := DigitsToInt(textOfMarker(d.Body, markerItemID))
	if !ok {
		return Page{}, false
	}
	page.Report.Found = 1
	l := Listing{
		ItemID:      id,
		Title:       textOfMarker(d.Body, markerItemTitle),
		Description: textOfMarker(d.Body, markerItemDesc),
		PriceLabel:  textOfMarker(d.Body, markerItemPrice),
		PriceKind:   PriceAbsent,
		Price:       Money{Currency: "RUB"},
		// The site's own words when a posting has ended, kept verbatim and
		// uninterpreted. Its presence is the only thing permitted to write
		// state='closed' with confidence 1.0.
		ClosedReason: textOfMarker(d.Body, markerItemClosed),
	}
	switch {
	case l.PriceLabel == "":
		l.PriceKind = PriceAbsent
	case strings.Contains(l.PriceLabel, negotiableLabel):
		l.PriceKind = PriceNegotiable
	default:
		if minor, ok := DigitsToMinor(l.PriceLabel); ok {
			l.PriceKind = PriceExact
			l.Price.Minor = minor
		} else {
			l.PriceKind = PriceNegotiable
		}
	}
	// No second number to disagree with, so the advisory cross-check does not
	// run here either.
	if v, ok := DigitsToInt(textOfMarker(d.Body, markerViewsTotal)); ok {
		l.ViewsTotal = &v
	}
	if v, ok := DigitsToInt(textOfMarker(d.Body, markerViewsToday)); ok {
		l.ViewsToday = &v
	}
	page.Listings = []Listing{l}
	return page, true
}

// DigitsToInt reads the digits of a rendered label as a plain integer: an id,
// a view count. Same unicode.IsDigit rule as the money walk, and the same
// reason for it.
func DigitsToInt(label string) (int64, bool) {
	var n int64
	seen := false
	for _, r := range label {
		if r < '0' || r > '9' {
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
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtractItem -v && go vet ./avito/
```

Expected: PASS on both.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/item.go avito/item_test.go && git commit -m "$(cat <<'MSG'
feat(avito): read an item page, the only source that can state a fact

An item page has no ld+json and no itemprop="price" — measured — so data-marker
is all there is, and the rendered label IS the price reading.

This lands in increment 1 rather than later because it is what turns a windowed
disappearance guess into a stated fact. Without it the confirmation sweeper has
no source and every removal alert stays at confidence 0.3 for ever.

A closed posting is recognised only from the site's own words, kept verbatim
and uninterpreted. Interpreting them would be this program deciding a listing
is dead on the strength of its own paraphrase.

MSG
)"
```

---

### Task 11: `Extract` — surface dispatch, path selection and the audit sample

**Files:**
- Create: `avito/extract.go`, `avito/extract_test.go`

**Interfaces:**
- Consumes: every extractor above.
- Produces: `type Options struct { Now int64; AuditDOM bool }`; `func Extract(d Document, opt Options) (Page, error)`; `var ErrNoExtractor error`.

- [ ] **Step 1: Write the failing test**

`avito/extract_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"strings"
	"testing"
)

func TestExtractDesktopPrefersState(t *testing.T) {
	body := `<html><body>` +
		`<script data-mfe-state="true">{"loaderData":{"data":{"catalog":{"items":[` +
		`{"id":1,"urlPath":"/a_1","title":"A"}]}}}}</script>` + desktopCard + `</body></html>`
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(body)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathState || page.Report.Rule != RuleDesktopCatalog {
		t.Errorf("Path/Rule = %q/%q, want state/%s — the named state rule must "+
			"win when both paths are available", page.Report.Path, page.Report.Rule,
			RuleDesktopCatalog)
	}
	if page.Report.HasWarning(WarnDegraded) {
		t.Error("a healthy state parse was marked degraded")
	}
}

// TestExtractDesktopFallsBackAndSaysSo is the bot-classification tell. Deleting
// the state block must not fail the page — it must come back on the DOM path,
// with card fidelity, carrying a degradation warning.
func TestExtractDesktopFallsBackAndSaysSo(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceDesktop,
		Body: []byte(`<html><body>` + desktopCard + `</body></html>`)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathDOM {
		t.Fatalf("Path = %q, want dom", page.Report.Path)
	}
	if !page.Report.HasWarning(WarnDegraded) {
		t.Error("the state block was gone and nothing warned; that transition is " +
			"the earliest warning available that this install has been flagged")
	}
	if len(page.Listings) != 1 {
		t.Errorf("Listings = %d, want 1 — fidelity must survive the fallback",
			len(page.Listings))
	}
}

func TestExtractMobileDispatch(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceMobile,
		Body: []byte(mobileState + mobileCard)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Rule != RuleMobileCards {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleMobileCards)
	}
}

func TestExtractItemDispatch(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(itemPage)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Kind != EnvelopeItem {
		t.Errorf("Kind = %q, want %q", page.Kind, EnvelopeItem)
	}
}

func TestExtractRefusesAMangledBody(t *testing.T) {
	_, err := Extract(Document{Surface: SurfaceMobile, Body: []byte{0xff, 0xfe}}, Options{})
	if !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("err = %v, want ErrNotUTF8 — a body that cannot be what it "+
			"claims to be must never reach an extractor", err)
	}
}

func TestExtractNoExtractor(t *testing.T) {
	_, err := Extract(Document{Surface: SurfaceMobile,
		Body: []byte("<html><body>Телефоны и ничего больше</body></html>")}, Options{})
	if !errors.Is(err, ErrNoExtractor) {
		t.Fatalf("err = %v, want ErrNoExtractor", err)
	}
}

// TestExtractAuditRunsBothPaths pins the audit sample. A fallback that only
// runs when the primary fails has its first production execution during an
// incident, with no known-good disagreement baseline.
func TestExtractAuditRunsBothPaths(t *testing.T) {
	body := `<html><body>` +
		`<script data-mfe-state="true">{"loaderData":{"data":{"catalog":{"items":[` +
		`{"id":7775028313,"urlPath":"/a_7775028313","title":"A"}]}}}}</script>` +
		desktopCard + `</body></html>`
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(body)},
		Options{AuditDOM: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathState {
		t.Fatalf("the audit changed which path answered; it must not")
	}
	if page.Audit == nil {
		t.Fatal("Audit = nil with AuditDOM set")
	}
	if page.Audit.Agreed != 1 || page.Audit.OnlyState != 0 || page.Audit.OnlyDOM != 0 {
		t.Errorf("Audit = %+v, want the two paths to agree on one id", *page.Audit)
	}
	if strings.Contains(page.Report.Rule, "microdata") {
		t.Error("the audit's rule leaked into the report")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestExtract -v
```

Expected: FAIL — `undefined: Extract`, `undefined: Options`, `page.Audit undefined`.

- [ ] **Step 3: Add `Audit` to the model, then write `Extract`**

Append to `avito/model.go`, inside the `Page` struct, immediately before `Report ExtractionReport`:

```go
	// Audit is set when both paths were run on purpose rather than because one
	// failed. It is how the fifty-to-fifty agreement a golden fixture asserts
	// gets calibrated on live pages.
	Audit *AuditResult
```

and after the `Page` type:

```go
// AuditResult is the two extraction paths compared on one document.
type AuditResult struct {
	Agreed    int // ids both paths found
	OnlyState int // ids only the state path found
	OnlyDOM   int // ids only the DOM path found
}
```

`avito/extract.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "errors"

// ErrNoExtractor means no rule recognised the document. It is not the same as
// an empty result set, and the caller's classifier must not conflate them: one
// is our failure to read, the other is the site's answer.
var ErrNoExtractor = errors.New("avito: no extractor recognised this document")

// Options is everything Extract needs from the outside world. The clock is a
// parameter because this package must not read one.
type Options struct {
	// Now is Unix seconds. Extraction does not use it today; fixtures carry a
	// frozen value so a page with a validity window cannot make the suite fail
	// on a day nobody changed anything.
	Now int64

	// AuditDOM runs the second path alongside a successful first one and
	// records the comparison. The caller samples it — once every fifty pages,
	// plus the first page of each watch — because a fallback whose first
	// production execution is during an incident has no baseline to be judged
	// against.
	AuditDOM bool
}

// Extract turns one document into one Page.
//
// Order matters and is per surface. On desktop the state block is the primary
// path and a fall back to the DOM is the bot-classification tell, so it is
// recorded as a degradation. On mobile there is no state catalogue at all, so
// the DOM is the primary path and nothing about it is a degradation.
func Extract(d Document, opt Options) (Page, error) {
	if err := d.Check(); err != nil {
		return Page{}, err
	}
	switch d.Surface {
	case SurfaceMobile:
		if page, ok := extractMobile(d); ok {
			return page, nil
		}
		if page, ok := extractItem(d); ok {
			return page, nil
		}
	default:
		if page, ok := extractDesktopState(d); ok {
			if opt.AuditDOM && page.Kind == EnvelopeCatalog {
				page.Audit = auditAgainstDOM(d, page)
			}
			return page, nil
		}
		if page, ok := extractItem(d); ok {
			return page, nil
		}
		if page, ok := extractDesktopDOM(d); ok {
			page.Report.Warn(WarnDegraded,
				"the desktop state block was absent; the DOM path answered")
			return page, nil
		}
	}
	return Page{}, ErrNoExtractor
}

// auditAgainstDOM runs the second path and compares the id sets. It never
// changes which path answered, and its own rule never reaches the report.
func auditAgainstDOM(d Document, primary Page) *AuditResult {
	other, ok := extractDesktopDOM(d)
	if !ok {
		return &AuditResult{OnlyState: len(primary.Listings)}
	}
	inState := make(map[int64]bool, len(primary.Listings))
	for _, l := range primary.Listings {
		inState[l.ItemID] = true
	}
	var res AuditResult
	inDOM := make(map[int64]bool, len(other.Listings))
	for _, l := range other.Listings {
		inDOM[l.ItemID] = true
		if inState[l.ItemID] {
			res.Agreed++
		} else {
			res.OnlyDOM++
		}
	}
	for id := range inState {
		if !inDOM[id] {
			res.OnlyState++
		}
	}
	return &res
}
```

- [ ] **Step 4: Run the whole suite**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -v && go vet ./avito/ && go test ./avito/ -run TestPackageReachesNoNetwork
```

Expected: every test PASSes; `go vet` clean.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/extract.go avito/extract_test.go avito/model.go && git commit -m "$(cat <<'MSG'
feat(avito): one entry point, and an order that is per surface

On desktop the state block is primary and a fall back to the DOM is recorded as
a degradation, because that transition is the earliest warning available that
this install has been classified as a bot — earlier than a 429 and much earlier
than a captcha. On mobile there is no state catalogue at all, so the DOM is
primary and nothing about it is a degradation.

ErrNoExtractor is deliberately distinct from an empty result set. One is our
failure to read a page, the other is the site's answer, and a classifier that
conflated them would answer a legitimately empty query by dropping the session
and re-paying a twenty-second challenge solve.

The audit runs the second path alongside a successful first one. A fallback
that only runs when the primary fails has its first production execution during
an incident, with no known-good baseline to be judged against.

MSG
)"
```

---

### Task 12: The two commands

**Files:**
- Create: `cmd/avitoparse/main.go`, `cmd/avitocapture/main.go`

**Interfaces:**
- Consumes: `avito.Extract`, `avito.Document`, `avito.Options`.
- Produces: two binaries. `avitoparse` is the offline debugging tool; `avitocapture` is how a fixture is made.

- [ ] **Step 1: Write `avitoparse`**

`cmd/avitoparse/main.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// avitoparse reads a captured page from disk and prints what the extractor made
// of it. It touches no network, which is the point: every question about why a
// page parsed the way it did is answered here rather than by fetching it again
// and getting a different page.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
)

func main() {
	surface := flag.String("surface", "", "desktop|mobile (default: guessed from the file name)")
	asJSON := flag.Bool("json", false, "print the whole Page as JSON")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: avitoparse [-surface desktop|mobile] [-json] <file[.gz]>")
		os.Exit(2)
	}
	path := flag.Arg(0)

	body, err := readMaybeGzip(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	s := avito.Surface(*surface)
	if s == "" {
		s = avito.SurfaceDesktop
		if strings.Contains(path, "mobile") {
			s = avito.SurfaceMobile
		}
	}

	page, err := avito.Extract(avito.Document{Surface: s, Body: body}, avito.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "extract:", err)
		if hit, ok := (avito.Document{Body: body}).Wall(); ok {
			fmt.Fprintf(os.Stderr, "the body is a wall: %s (%q)\n", hit.Label, hit.Marker)
		}
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(page); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("surface=%s kind=%s rule=%s path=%s script-type=%q\n",
		page.Surface, page.Kind, page.Report.Rule, page.Report.Path, page.Report.ScriptType)
	fmt.Printf("listings=%d found=%d dropped=%d ok=%v empty-declared=%v",
		len(page.Listings), page.Report.Found, page.Report.Dropped, page.Ok(), page.EmptyDeclared)
	if page.TotalCount != nil {
		fmt.Printf(" total=%d", *page.TotalCount)
	}
	fmt.Println()
	if page.CanonicalURL != "" {
		fmt.Println("canonical:", page.CanonicalURL)
	}
	for _, w := range page.Report.Warnings {
		fmt.Printf("warn  %s: %s\n", w.Code, w.Detail)
	}
	for i, l := range page.Listings {
		price := "—"
		switch l.PriceKind {
		case avito.PriceExact:
			price = fmt.Sprintf("%d.%02d", l.Price.Minor/100, l.Price.Minor%100)
		case avito.PriceNegotiable:
			price = "договорная"
		}
		fmt.Printf("%3d  %-12d %-10s %s\n", i+1, l.ItemID, price, l.Title)
	}
}

func readMaybeGzip(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		r = zr
	}
	return io.ReadAll(r)
}
```

- [ ] **Step 2: Build it and run it against a real fixture**

```bash
cd S:/Projects/Avito_Go && go build ./cmd/avitoparse && ./avitoparse avito/testdata/desktop-canonical-p1.html.gz | head -8
```

Expected: `rule=desktop.catalog.v1 path=state`, `listings=50`, then the first rows with real ids, prices and titles.

```bash
cd S:/Projects/Avito_Go && ./avitoparse -surface mobile avito/testdata/mobile-search-30.html.gz | head -6
```

Expected: `rule=mobile.cards.v1 path=dom`, `listings=30 ... total=59959`.

```bash
cd S:/Projects/Avito_Go && ./avitoparse -surface mobile avito/testdata/mobile-search-empty.html.gz | head -3
```

Expected: `listings=0 ... ok=false empty-declared=true total=0`.

```bash
cd S:/Projects/Avito_Go && ./avitoparse avito/testdata/desktop-spa-shell.html.gz | head -3
```

Expected: `kind=redirect rule=desktop.redirect.v1`, then a `canonical:` line.

If any of these disagree, the bug is in the extractor and not in the command: go back and fix it before continuing. These four are the increment's acceptance criteria.

- [ ] **Step 3: Write `avitocapture`**

`cmd/avitocapture/main.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// avitocapture fetches one URL and freezes it as a fixture.
//
// It is a developer tool and it does no fingerprinting: it goes through a
// BlankTrail port, which does. The real transport is increment 2, and this
// command must not grow into it — anything it learns about retries, sessions or
// classification belongs there instead.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type meta struct {
	URL        string `json:"url"`
	Surface    string `json:"surface"`
	CapturedAt string `json:"captured_at"`
	// FrozenNow is what a test runs its clock at, so a page with a validity
	// window cannot make the suite fail on a day nobody changed anything.
	FrozenNow  int64  `json:"frozen_now"`
	Status     int    `json:"status"`
	Bytes      int    `json:"bytes"`
	ProxyUsed  string `json:"proxy_used"`
}

func main() {
	proxy := flag.String("proxy", "", "proxy URL, e.g. socks5://127.0.0.1:20000")
	surface := flag.String("surface", "desktop", "desktop|mobile")
	out := flag.String("out", "avito/testdata", "directory to write into")
	name := flag.String("name", "", "fixture name (required)")
	timeout := flag.Duration("timeout", 120*time.Second, "the first request may be solving a challenge")
	flag.Parse()
	if flag.NArg() != 1 || *name == "" {
		fmt.Fprintln(os.Stderr, "usage: avitocapture -name <fixture> [-proxy URL] [-surface desktop|mobile] <url>")
		os.Exit(2)
	}

	tr := &http.Transport{}
	if *proxy != "" {
		u, err := url.Parse(*proxy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proxy:", err)
			os.Exit(1)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	// The proxy re-signs origins with its own MITM CA. This is a capture tool,
	// not a security boundary, and increment 2 pins the CA properly.
	tr.TLSClientConfig = insecureTLS()

	req, err := http.NewRequest(http.MethodGet, flag.Arg(0), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "request:", err)
		os.Exit(1)
	}
	// Accept-Language is ours in both modes: BlankTrail passes it through
	// verbatim and Avito needs it.
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

	client := &http.Client{Transport: tr, Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	htmlPath := filepath.Join(*out, *name+".html.gz")
	f, err := os.Create(htmlPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestCompression)
	if _, err := zw.Write(body); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	zw.Close()
	f.Close()

	now := time.Now()
	m := meta{
		URL: flag.Arg(0), Surface: *surface,
		CapturedAt: now.UTC().Format(time.RFC3339),
		FrozenNow:  now.Unix(),
		Status:     resp.StatusCode, Bytes: len(body), ProxyUsed: *proxy,
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, *name+".meta.json"), append(mb, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "meta:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, http %d)\n", htmlPath, len(body), resp.StatusCode)
	fmt.Println("REMEMBER: this page carries third-party personal data. Scrub before publishing.")
}
```

Add `insecureTLS` in the same file:

```go
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // MITM CA; see the package comment
}
```

with `"crypto/tls"` added to the imports.

- [ ] **Step 4: Build both and confirm `avito/` is still offline**

```bash
cd S:/Projects/Avito_Go && go build ./... && go test ./avito/ -run TestPackageReachesNoNetwork && go vet ./...
```

Expected: builds; the offline test still passes (the command imports `net/http`, the package does not); vet clean.

- [ ] **Step 5: Capture the two fixtures that are still missing**

An item page and a soft block. BlankTrail must be running; use a port opened for the surface you want.

```bash
cd S:/Projects/Avito_Go && ./avitocapture -name desktop-item -surface desktop \
  -proxy socks5://127.0.0.1:20000 \
  "https://www.avito.ru/ekaterinburg/telefony/iphone_15_128_gb_7775028313"
./avitoparse avito/testdata/desktop-item.html.gz | head -4
```

Expected: `kind=item rule=item.markers.v1`, one listing with a price and a view count.

If that listing has since been removed, take any live item URL from
`./avitoparse avito/testdata/desktop-canonical-p1.html.gz` and use that instead.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd/ avito/testdata/ && git commit -m "$(cat <<'MSG'
feat(cmd): avitoparse for reading a fixture, avitocapture for making one

avitoparse touches no network, which is the point: every question about why a
page parsed the way it did is answered from the frozen bytes rather than by
fetching it again and getting a different page.

avitocapture does no fingerprinting because it goes through a BlankTrail port,
which does. It must not grow into the transport — what it would learn about
retries, sessions and classification belongs to increment 2.

Both are outside package avito, so the offline guard still holds: the commands
import net/http and the package does not.

MSG
)"
```

---

### Task 13: Golden fixtures over the real captures

**Files:**
- Create: `avito/golden_test.go`, `avito/testdata/*.want.json`, `avito/testdata/*.meta.json`

**Interfaces:**
- Consumes: `Extract`, and the fixtures on disk.
- Produces: `go test ./avito/ -update` regenerates the `want.json` files.

The golden file pins the **whole** `Page`, `ExtractionReport` included. Fifty listings reached by the DOM path instead of the state path is exactly the regression this design exists to catch, and a fixture pinning only the items would let it pass green.

- [ ] **Step 1: Write the failing test**

`avito/golden_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the .want.json files from the current output")

type fixtureMeta struct {
	URL       string  `json:"url"`
	Surface   Surface `json:"surface"`
	FrozenNow int64   `json:"frozen_now"`
	Status    int     `json:"status"`
}

func loadFixture(t *testing.T, name string) (Document, fixtureMeta) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".html.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m fixtureMeta
	if mb, err := os.ReadFile(filepath.Join("testdata", name+".meta.json")); err == nil {
		if err := json.Unmarshal(mb, &m); err != nil {
			t.Fatalf("meta: %v", err)
		}
	}
	if m.Surface == "" {
		m.Surface = SurfaceDesktop
		if strings.Contains(name, "mobile") {
			m.Surface = SurfaceMobile
		}
	}
	return Document{Surface: m.Surface, Body: body}, m
}

// TestGolden pins the whole Page for every captured page.
func TestGolden(t *testing.T) {
	names := []string{
		"desktop-spa-shell",
		"desktop-canonical-p1",
		"desktop-seller",
		"mobile-search-30",
		"mobile-search-empty",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			doc, m := loadFixture(t, name)
			page, err := Extract(doc, Options{Now: m.FrozenNow})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			got, err := json.MarshalIndent(page, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", name+".want.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", path)
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run: go test ./avito/ -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("%s: the extracted Page changed.\n"+
					"If this is intended, run: go test ./avito/ -update\n"+
					"and READ THE DIFF — a change of Report.Rule or Report.Path "+
					"with the same listings is the regression this fixture exists "+
					"to catch, not a formatting change.", name)
			}
		})
	}
}

// TestGoldenExpectations pins the handful of facts a diff would let slide,
// because a golden file only says "the same as last time".
func TestGoldenExpectations(t *testing.T) {
	cases := []struct {
		name     string
		kind     EnvelopeKind
		rule     string
		path     Path
		listings int
	}{
		{"desktop-spa-shell", EnvelopeRedirect, RuleDesktopRedirect, PathState, 0},
		{"desktop-canonical-p1", EnvelopeCatalog, RuleDesktopCatalog, PathState, 50},
		{"desktop-seller", EnvelopeCatalog, RuleDesktopCatalog, PathState, 45},
		{"mobile-search-30", EnvelopeCatalog, RuleMobileCards, PathDOM, 30},
		{"mobile-search-empty", EnvelopeCatalog, RuleMobileCards, PathDOM, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, m := loadFixture(t, c.name)
			page, err := Extract(doc, Options{Now: m.FrozenNow})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if page.Kind != c.kind {
				t.Errorf("Kind = %q, want %q", page.Kind, c.kind)
			}
			if page.Report.Rule != c.rule {
				t.Errorf("Rule = %q, want %q", page.Report.Rule, c.rule)
			}
			if page.Report.Path != c.path {
				t.Errorf("Path = %q, want %q", page.Report.Path, c.path)
			}
			if len(page.Listings) != c.listings {
				t.Errorf("Listings = %d, want %d", len(page.Listings), c.listings)
			}
		})
	}
}

// TestDesktopPathsAgree is the calibration the audit sample exists to keep
// honest: on a real page the two independent extractors must find the same ids.
func TestDesktopPathsAgree(t *testing.T) {
	doc, m := loadFixture(t, "desktop-canonical-p1")
	page, err := Extract(doc, Options{Now: m.FrozenNow, AuditDOM: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Audit == nil {
		t.Fatal("Audit = nil")
	}
	if page.Audit.Agreed != 50 || page.Audit.OnlyState != 0 || page.Audit.OnlyDOM != 0 {
		t.Errorf("Audit = %+v, want 50 agreed and nothing exclusive. A "+
			"disagreement here means one of the two paths has drifted, and the "+
			"whole point of having two is that they are checked against each "+
			"other before an incident.", *page.Audit)
	}
}

```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestGolden -v
```

Expected: FAIL — no `.want.json` files yet, and the message tells you to run with `-update`.

- [ ] **Step 3: Generate the goldens, then read them**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestGolden -update && ls avito/testdata/*.want.json
```

Now read each one before trusting it. A golden file generated from a bug pins the bug:

```bash
cd S:/Projects/Avito_Go && python -c "
import json,glob
for p in sorted(glob.glob('avito/testdata/*.want.json')):
    d=json.load(open(p,encoding='utf-8'))
    print(p.split('/')[-1], '->', d['Kind'], d['Report']['Rule'], d['Report']['Path'],
          'listings=%d'%len(d.get('Listings') or []), 'warnings=%d'%len(d['Report'].get('Warnings') or []))
"
```

Expected, and check every line: the shell is `redirect`, the canonical page is `catalog`/`desktop.catalog.v1`/`state` with 50 listings, the seller page the same rule with 45, mobile is `mobile.cards.v1`/`dom` with 30, and the empty mobile page the same rule with 0. **Any `warnings` count above zero on a healthy fixture needs explaining before you commit it.**

- [ ] **Step 4: Run the whole suite**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -v && go test ./avito/ -run TestPackageReachesNoNetwork
```

Expected: all PASS, including `TestPackageReachesNoNetwork`, `TestGoldenExpectations` and `TestDesktopPathsAgree`.

If `TestGoldenExpectations` fails on the seller page's count of 45, correct the number in the test to whatever `avitoparse` actually reports for that fixture and say so in the commit message — the 45 came from counting `urlPath` occurrences during measurement, which counts promoted rows the extractor may legitimately drop.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/golden_test.go avito/testdata/ && git commit -m "$(cat <<'MSG'
test(avito): golden files that pin the whole Page, report included

Fifty listings reached by the DOM path instead of the state path is exactly the
regression this design exists to catch, and a fixture pinning only the items
would let it pass green. So the golden is the whole Page, and a separate test
states the handful of facts a diff would let slide — a golden file only ever
says "the same as last time".

TestDesktopPathsAgree is the calibration: on a real page the two independent
extractors must find the same fifty ids. The whole point of having two paths is
that they are checked against each other before an incident, not during one.

MSG
)"
```

---

### Task 14: Mutation tests — prove the robustness claims

**Files:**
- Create: `avito/mutation_test.go`

**Interfaces:**
- Consumes: `Extract`, the fixtures.
- Produces: nothing exported. These are the tests that turn the design's claims into properties.

The design makes four claims about surviving markup change. Each is derived in code from the real fixture, so the claim is proved against the page Avito actually serves rather than against a hand-written sample that happens to satisfy it.

- [ ] **Step 1: Write the failing test**

`avito/mutation_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// mutateFixture loads a real capture and hands its bytes to fn.
func mutateFixture(t *testing.T, name string, fn func([]byte) []byte) (Page, error) {
	t.Helper()
	doc, m := loadFixture(t, name)
	doc.Body = fn(doc.Body)
	return Extract(doc, Options{Now: m.FrozenNow})
}

// TestMutationRehashEveryClass. Avito ships CSS modules: every class is a stem
// plus a build hash and both change without announcement. An extractor that
// depended on one would return zero cards after an ordinary deploy — and zero
// cards is what a new-listing monitor reports on a quiet market, so the failure
// would be invisible.
func TestMutationRehashEveryClass(t *testing.T) {
	before, m := loadFixture(t, "mobile-search-30")
	base, err := Extract(before, Options{Now: m.FrozenNow})
	if err != nil {
		t.Fatal(err)
	}
	after, err := mutateFixture(t, "mobile-search-30", rehashClasses)
	if err != nil {
		t.Fatalf("rehashing every class broke extraction: %v", err)
	}
	if len(after.Listings) != len(base.Listings) {
		t.Fatalf("listings %d -> %d after rehashing every class; no extractor "+
			"may depend on a CSS class", len(base.Listings), len(after.Listings))
	}
	for i := range base.Listings {
		if base.Listings[i].ItemID != after.Listings[i].ItemID {
			t.Fatalf("listing %d changed identity after a rehash", i)
		}
	}
}

// TestMutationRenameScriptType. The type is a deliberate non-MIME so no browser
// executes the block; it is presentation trivia and the likeliest thing to be
// renamed. Selection is on data-mfe-state, so a rename must change nothing but
// the recorded value.
func TestMutationRenameScriptType(t *testing.T) {
	after, err := mutateFixture(t, "desktop-canonical-p1", func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(`type="mime/invalid"`), []byte(`type="application/x-renamed"`))
	})
	if err != nil {
		t.Fatalf("renaming the script type broke extraction: %v", err)
	}
	if after.Report.Rule != RuleDesktopCatalog || after.Report.Path != PathState {
		t.Errorf("Rule/Path = %q/%q after a rename; selection must not depend on the type",
			after.Report.Rule, after.Report.Path)
	}
	if len(after.Listings) != 50 {
		t.Errorf("Listings = %d, want 50", len(after.Listings))
	}
	if after.Report.ScriptType != "application/x-renamed" {
		t.Errorf("ScriptType = %q; the new value must be recorded so a drift "+
			"report can say it moved", after.Report.ScriptType)
	}
}

// TestMutationEntityInTitle. x/net/html returns raw bytes for script data. A
// second unescaping pass rewrites a literal &amp; inside a JSON string to &,
// corrupting a seller's title with no error anywhere.
func TestMutationEntityInTitle(t *testing.T) {
	const marker = "Ремонт &amp; отделка"
	after, err := mutateFixture(t, "desktop-canonical-p1", func(b []byte) []byte {
		// Rewrite the first title in the state block, entities and all.
		i := bytes.Index(b, []byte(`"title":"`))
		if i < 0 {
			t.Fatal(`no "title": in the fixture`)
		}
		start := i + len(`"title":"`)
		end := bytes.IndexByte(b[start:], '"')
		if end < 0 {
			t.Fatal("unterminated title")
		}
		out := make([]byte, 0, len(b))
		out = append(out, b[:start]...)
		out = append(out, marker...)
		out = append(out, b[start+end:]...)
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range after.Listings {
		if l.Title == marker {
			found = true
		}
		if l.Title == "Ремонт & отделка" {
			t.Fatal("the entity was decoded; every seller whose title contains " +
				"an ampersand is now silently corrupted")
		}
	}
	if !found {
		t.Errorf("the injected title did not survive verbatim")
	}
}

// TestMutationDeleteStateBlock. Avito can serve a desktop page with its state
// removed — loaderData.toggles carries runtime_catalog_drop_state_for_bots. The
// run must come back on the DOM path, with card fidelity, and it must say so.
func TestMutationDeleteStateBlock(t *testing.T) {
	after, err := mutateFixture(t, "desktop-canonical-p1", deleteMFEState)
	if err != nil {
		t.Fatalf("deleting the state block failed the page; it must degrade, not fail: %v", err)
	}
	if after.Report.Path != PathDOM {
		t.Fatalf("Path = %q, want dom", after.Report.Path)
	}
	if !after.Report.HasWarning(WarnDegraded) {
		t.Error("no degradation warning; that transition is the earliest " +
			"available signal that this install has been classified as a bot")
	}
	if len(after.Listings) != 50 {
		t.Errorf("Listings = %d, want 50 — the fallback must keep card fidelity",
			len(after.Listings))
	}
}

// rehashClasses rewrites the value of every class attribute, leaving every tag,
// every other attribute and all text exactly as served.
func rehashClasses(body []byte) []byte {
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		s := string(raw)
		i := strings.Index(s, ` class="`)
		if i < 0 {
			out.WriteString(s)
			continue
		}
		start := i + len(` class="`)
		end := strings.IndexByte(s[start:], '"')
		if end < 0 {
			out.WriteString(s)
			continue
		}
		out.WriteString(s[:start])
		out.WriteString("zzz-rehashed-9QwErT")
		out.WriteString(s[start+end:])
	}
}

// deleteMFEState removes the whole data-mfe-state script, leaving the
// server-rendered cards untouched.
func deleteMFEState(body []byte) []byte {
	open := bytes.Index(body, []byte(`data-mfe-state="true"`))
	if open < 0 {
		return body
	}
	tagStart := bytes.LastIndex(body[:open], []byte("<script"))
	closeIdx := bytes.Index(body[open:], []byte("</script>"))
	if tagStart < 0 || closeIdx < 0 {
		return body
	}
	end := open + closeIdx + len("</script>")
	out := make([]byte, 0, len(body))
	out = append(out, body[:tagStart]...)
	out = append(out, body[end:]...)
	return out
}
```

- [ ] **Step 2: Run it and watch it fail or pass, and read which**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run TestMutation -v
```

Expected on a correct implementation: all four PASS. If any fails, the failure is real — the implementation depends on something it claimed not to. Fix the implementation, never the test.

The likeliest genuine failure is `TestMutationDeleteStateBlock` reporting fewer than 50 listings, because the desktop microdata path drops cards whose `data-item-id` is missing. If so, the fix belongs in `decodeDesktopCard`: fall back to the id embedded in the card's own `itemprop="url"` path before dropping the card.

- [ ] **Step 3: Run the whole suite one final time**

```bash
cd S:/Projects/Avito_Go && go test ./... -v 2>&1 | tail -30 && go test ./avito/ -run TestPackageReachesNoNetwork && go vet ./...
```

Expected: everything PASSes, vet clean.

- [ ] **Step 4: Commit**

```bash
cd S:/Projects/Avito_Go && git add avito/mutation_test.go avito/ && git commit -m "$(cat <<'MSG'
test(avito): prove the robustness claims instead of asserting them

Four mutations, each derived in code from a real capture rather than from a
hand-written sample that happens to satisfy the claim.

Rehash every CSS class and nothing changes — Avito ships CSS modules and both
the stem and the hash move without announcement. Rename the script type and
nothing changes but the recorded value. Inject a literal &amp; into a title and
it survives verbatim. Delete the state block and the page comes back on the DOM
path with all fifty cards and a degradation warning, because Avito can withhold
that block from a client it has classified.

Each of these failing would be silent in production: the monitor would return
zero listings, which is indistinguishable from a quiet market.

MSG
)"
```

---

## Done when

- `go test ./...` passes, `TestPackageReachesNoNetwork` passes, `go vet ./...` is clean.
- `./avitoparse avito/testdata/desktop-canonical-p1.html.gz` prints 50 listings with `rule=desktop.catalog.v1 path=state`, and the same 50 ids come back from the microdata path under `-update`'s audit.
- `./avitoparse -surface mobile avito/testdata/mobile-search-30.html.gz` prints 30 listings with `rule=mobile.cards.v1 path=dom` and `total=59959`.
- `./avitoparse -surface mobile avito/testdata/mobile-search-empty.html.gz` prints `ok=false empty-declared=true total=0`.
- `./avitoparse avito/testdata/desktop-spa-shell.html.gz` prints `kind=redirect` and a canonical URL.
- The four mutation tests pass.
- `git log --oneline` shows one commit per task, each explaining why rather than what.

## What increment 2 inherits

`Extract` is the seam. Increment 2's classifier consumes `Page`, `Page.Ok()`, `Page.TotalCount`, `Page.EmptyDeclared`, `Document.Wall()` and `ErrNoExtractor`, and turns them into `ClassOK`, `ClassEmpty`, `ClassShape`, `ClassSoftBlock` and the rest. Nothing in this increment knows those names, and nothing in it should learn them: the extractor says what it saw, and the transport decides what that means.
