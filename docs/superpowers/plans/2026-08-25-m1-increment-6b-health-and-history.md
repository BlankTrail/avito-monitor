# M1 increment 6B — Здоровье сбора, История, and the chart

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person can see whether their collector is healthy — and tell a
flagged monitor, a throttled monitor and a quiet market apart, which look
identical from everywhere else in the product — and can read a listing's price
history as a picture.

**Architecture:** Two screens on 6A's shell, one new package. `internal/chart`
draws a PNG against `image/png` and nothing else, lifted from the donor.
`internal/store/health.go` and `internal/store/history.go` are the reads; the
screens are functions returning strings, the way 6A's are.

**Tech Stack:** Go 1.26, `image/png` (standard library), `modernc.org/sqlite`.
No new dependency.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §The
panel (Здоровье сбора, История), §Collection (saturation, card yield), §Being
seen as a bot (the path-share alarm).

**Predecessor:** `2026-08-24-m1-increment-6a-panel-shell.md`, merged as
`4975b74`. 6C (Правила, Результаты, `internal/export`) follows this.

## Global Constraints

- **Go 1.26, no CGO.** `modernc.org/sqlite` only.
- **Eight direct dependencies, and this plan adds none.** No plotting library:
  what is needed is a line, a grid and eleven glyphs.
- **All user-facing copy is Russian**, correct orthography, «ё» where it belongs.
- **Nothing may quote a throughput number the soak did not produce**, and none
  without its sample size and single-address caveat.
- **A metric with no data says so rather than inventing a number.** The spec's
  own rule about saturation, and it governs every figure on the health screen.
- **Secrets never reach a page, a log or an error.**

---

## What already exists

**From 6A, and used as-is:** `web.Server` with routing, `auth`, `render`,
`page`, `s.tabs`; `esc`/`attr`/`field`/`checkbox`/`note`/`writeFragment`;
`rawHTML`; `num` for a Russian rate; `internal/ru.Plural`.

**In the store, and read by this plan:**

| table | columns this plan reads |
|---|---|
| `passes` | `watch_id, started_at, class, path, evidential, is_seed, items_seen, arrivals, drop_state, degraded_why, error, surface, total_count` |
| `watches` | `id, name, surface, state, halted_reason, interval_sec, enabled` |
| `listings` | `item_id, thread_id, title, url_path` |
| `listing_snapshots` | `item_id, ts, price_kind, price_minor, currency, surface` |

**Already on the store and reused:** `Saturation(ctx, watchID, n) (float64, bool, error)`,
`Volume(ctx) (Volume, error)`, `Watches(ctx)`.

**The class vocabulary** (`internal/transport/class.go`): `ok`, `empty`,
`shape`, `soft-block`, `cold`, `throttled`, `burned`, `challenge`, `mangled`,
`other`. `shape` is **ours** — our parser stopped understanding the payload —
and the screen must say so rather than filing it with the site's refusals.

**Local facts:** Bash heredocs eat backslashes here; use Write/Edit for anything
with `\n` or `\t`. `GOPROXY` is `direct`, so `go mod tidy` needs
`GOPROXY=https://proxy.golang.org,direct`.

## File structure

| file | responsible for |
|---|---|
| `internal/chart/chart.go` | `Line`, `Series`, `Point`, `Axis`, `Encode` |
| `internal/chart/draw.go` | the pixel work |
| `internal/chart/glyphs.go` | the digit font, which has no letters |
| `internal/store/health.go` | path shares, evidential fractions, card yield, the block picture |
| `internal/store/history.go` | one thread's price series |
| `internal/web/health.go` | Здоровье сбора |
| `internal/web/history.go` | История, and the `<img>` the chart hangs off |

---

## Task 1: `internal/chart` — a line that refuses to guess

**Files:**
- Create: `internal/chart/chart.go`, `draw.go`, `glyphs.go`, `chart_test.go`

A lift from the donor's `internal/chart`, which is 504 + 113 + 60 lines plus 726
of tests. Written against `image/png` for the reason the XLSX writer and the Bot
API client were: what is needed is a line, a grid and eleven glyphs, and a
plotting library to reach them is a plotting library in a public AGPL repository.

**The font has no letters, and that is load-bearing.** Every word belongs to
whoever shows the picture — a caption, a heading above an `<img>` — where it
stays selectable and translatable. This package never holds a word.

**Two things it will not do**, and both are this increment's reason for having
it: a reading with no price **breaks the line** rather than being drawn as a
zero, and a stretch nobody collected **breaks it too**. A chart that joined
those up would draw a move nobody measured.

**Interfaces:**
- Produces:

```go
// In internal/chart:
type Point struct {
    TS    int64   // whole Unix seconds UTC
    Value float64 // NaN means read-but-absent; it breaks the line
}
type Series struct {
    Points []Point       // must rise in TS; this package does not re-sort
    Colour color.RGBA    // zero takes the next palette entry
}
type Axis struct {
    Invert bool
    Format func(float64) string
}
type Line struct {
    Width, Height int
    Y             Axis
    Series        []Series
    Loc           *time.Location
    MaxGap        int64 // seconds; zero joins everything
}
func (l Line) Encode(w io.Writer) error
var ErrNoData = errors.New("chart: нет точек для графика")
```

- [ ] **Step 1: Lift the package**

```bash
cd S:/Projects/Avito_Go && mkdir -p internal/chart && \
  cp S:/Projects/WB_GO/internal/chart/*.go internal/chart/ && \
  sed -i 's|wildberries-monitor|avito-monitor|g' internal/chart/*.go && \
  gofmt -w ./internal/chart && go test ./internal/chart/ 2>&1 | tail -5
```

Expected: PASS. The package imports nothing of the donor's, so the module path
rewrite may be a no-op — check and do not be surprised either way.

- [ ] **Step 2: Read every file and remove what is about the other product**

Search for anything naming Wildberries, an article number, a rank or a position:

```bash
cd S:/Projects/Avito_Go && grep -rn "Wildberries\|wb\.\|nmID\|nm_id\|артикул\|позици" internal/chart/
```

`Axis.Invert` exists for search position and stays — its doc comment explains
why a position chart drawn the usual way up reads as an improvement every time
the product falls. That reasoning is about charts, not about Wildberries, and
this product will want it in M2. Change only comments that name the other site.

- [ ] **Step 3: Write the test for the two refusals**

Append to `internal/chart/chart_test.go` — the donor's own tests come with the
lift and stay; these are the two this increment depends on, written here so they
fail loudly if a future edit joins the line up.

```go
// TestAReadingWithNoPriceBreaksTheLine.
//
// NaN is what a snapshot with price_kind 'negotiable' or 'absent' becomes. Drawn
// as a zero it is a price crash that never happened; interpolated over, it is a
// price that held when nobody could see it.
func TestAReadingWithNoPriceBreaksTheLine(t *testing.T) {
	joined := Line{Series: []Series{{Points: []Point{
		{TS: 1000, Value: 100}, {TS: 2000, Value: 200},
	}}}}
	broken := Line{Series: []Series{{Points: []Point{
		{TS: 1000, Value: 100}, {TS: 1500, Value: math.NaN()}, {TS: 2000, Value: 200},
	}}}}

	var a, b bytes.Buffer
	if err := joined.Encode(&a); err != nil {
		t.Fatal(err)
	}
	if err := broken.Encode(&b); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("a NaN in the middle drew the same picture as no NaN at all")
	}
}

// TestAStretchNobodyCollectedBreaksTheLineToo.
//
// MaxGap is the caller's knowledge and not this package's: only the caller knows
// how often the listing was supposed to be read, and the difference between «the
// price held for a week» and «nobody looked for a week» is exactly that number.
func TestAStretchNobodyCollectedBreaksTheLineToo(t *testing.T) {
	points := []Point{{TS: 1000, Value: 100}, {TS: 1_000_000, Value: 200}}

	var joined, broken bytes.Buffer
	if err := (Line{Series: []Series{{Points: points}}}).Encode(&joined); err != nil {
		t.Fatal(err)
	}
	if err := (Line{MaxGap: 3600, Series: []Series{{Points: points}}}).Encode(&broken); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(joined.Bytes(), broken.Bytes()) {
		t.Error("a gap of eleven days was drawn as a straight line")
	}
}

// TestNothingToDrawIsAnErrorAndNotAnEmptyGrid.
//
// A picture of an empty grid looks like a product whose price was nothing, and
// prompts a second look at a working program. The caller says «нет данных».
func TestNothingToDrawIsAnErrorAndNotAnEmptyGrid(t *testing.T) {
	var buf bytes.Buffer
	if err := (Line{}).Encode(&buf); !errors.Is(err, ErrNoData) {
		t.Errorf("err = %v, want ErrNoData", err)
	}
	if err := (Line{Series: []Series{{Points: []Point{
		{TS: 1000, Value: math.NaN()},
	}}}}).Encode(&buf); !errors.Is(err, ErrNoData) {
		t.Errorf("a series of nothing but NaN: err = %v, want ErrNoData", err)
	}
}

// TestThePngIsAPng, so an <img> tag does not have to be trusted.
func TestThePngIsAPng(t *testing.T) {
	var buf bytes.Buffer
	if err := (Line{Series: []Series{{Points: []Point{
		{TS: 1000, Value: 100}, {TS: 2000, Value: 200},
	}}}}).Encode(&buf); err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Errorf("format = %q", format)
	}
	if img.Bounds().Dx() < 240 || img.Bounds().Dy() < 160 {
		t.Errorf("the chart is %dx%d, smaller than the stated minimum",
			img.Bounds().Dx(), img.Bounds().Dy())
	}
}
```

Add to that file's imports: `bytes`, `errors`, `image`, `image/png` (blank, for
the decoder), `math`.

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/chart/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS, the donor's and these four.

If `TestNothingToDrawIsAnErrorAndNotAnEmptyGrid`'s second case fails — a series
of nothing but NaN drawing a grid rather than returning `ErrNoData` — that is a
real defect and not a wrong expectation. Fix `measure`/`draw` so a series with no
finite point counts as no data, and say in the commit that the lift arrived with
it.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/chart && \
  git commit -m "feat(chart): a line that refuses to guess"
```

---

## Task 2: `internal/store/health.go` — the numbers that make the transport honest

**Files:**
- Create: `internal/store/health.go`, `internal/store/health_test.go`

Every figure here answers one question the spec states as the screen's whole
reason: **a monitor being flagged, a monitor being throttled and a quiet market
look identical from everywhere else in the product.**

**Interfaces:**
- Produces:

```go
// In internal/store:
type PathShare struct {
    Path  string // "state" | "dom" | ""
    Count int
}
type ClassHour struct {
    Hour  int64  // Unix seconds, truncated to the hour
    Class string
    Count int
}
type WatchHealth struct {
    WatchID          int64
    Name             string
    Surface          string
    Passes           int   // in the window
    Evidential       int   // of those
    LastEvidentialAt int64 // 0 when there has never been one
    LastPassAt       int64
    // CardYield is items_seen on the newest ok pass over the median of the last
    // ten. Zero and false when there are not ten yet: the alarm is not armed
    // until then, and a ratio computed from three passes is a number that means
    // nothing dressed as one that means something.
    CardYield   float64
    CardYieldOK bool
}
func (s *Store) PathShares(ctx context.Context, since int64) ([]PathShare, error)
func (s *Store) ClassByHour(ctx context.Context, since int64) ([]ClassHour, error)
func (s *Store) WatchHealth(ctx context.Context, since int64) ([]WatchHealth, error)
```

- [ ] **Step 1: Write the failing test**

Create `internal/store/health_test.go`. `openTemp(t)` is the existing helper.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// pass writes one pass row directly, because these tests are about reading and
// SavePass would drag a whole extraction in with it.
func pass(t *testing.T, s *Store, watchID int64, at int64, class, path string, evidential bool, itemsSeen int) {
	t.Helper()
	_, err := s.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, path,
		   mode, evidential, items_seen)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, 'direct', ?, ?)`,
		watchID, at, at, class, path, evidential, itemsSeen)
	if err != nil {
		t.Fatal(err)
	}
}

// TestPathSharesAreTheEarliestWarningThereIs.
//
// Spec §Being seen as a bot: on the desktop surface a silent switch from state
// to dom is the earliest available sign that this install has been flagged —
// earlier than a 429 and much earlier than a captcha.
func TestPathSharesAreTheEarliestWarningThereIs(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for i := 0; i < 7; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "state", true, 30)
	}
	for i := 0; i < 3; i++ {
		pass(t, s, id, 20_000+int64(i), "ok", "dom", true, 30)
	}
	got, err := s.PathShares(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for _, p := range got {
		byPath[p.Path] = p.Count
	}
	if byPath["state"] != 7 || byPath["dom"] != 3 {
		t.Errorf("shares = %+v, want state 7 and dom 3", byPath)
	}
}

// TestTheWindowIsRespected, because «the share over the last day» is a different
// number from «the share ever», and the second hides a switch that happened this
// morning behind a month of health.
func TestTheWindowIsRespected(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	pass(t, s, id, 1_000, "ok", "state", true, 30)  // old
	pass(t, s, id, 90_000, "ok", "dom", true, 30)   // inside

	got, err := s.PathShares(context.Background(), 50_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "dom" {
		t.Errorf("shares = %+v, want only the recent one", got)
	}
}

// TestTheBlockPictureIsByClassAndHour.
func TestTheBlockPictureIsByClassAndHour(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	// Two in one hour, one in the next.
	pass(t, s, id, 3600, "throttled", "", false, 0)
	pass(t, s, id, 3601, "throttled", "", false, 0)
	pass(t, s, id, 7200, "burned", "", false, 0)

	got, err := s.ClassByHour(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two buckets", got)
	}
	if got[0].Hour != 3600 || got[0].Class != "throttled" || got[0].Count != 2 {
		t.Errorf("first bucket = %+v", got[0])
	}
	if got[1].Hour != 7200 || got[1].Class != "burned" || got[1].Count != 1 {
		t.Errorf("second bucket = %+v", got[1])
	}
}

// TestTheAgeOfTheLastEvidentialPassIsSeparateFromTheAgeOfTheLastPass.
//
// The spec calls this the one line that separates a quiet market from a dead
// collector. A watch polling every five minutes and answering soft-block every
// time has a recent last pass and an ancient last evidential one, and only the
// second says anything is wrong.
func TestTheAgeOfTheLastEvidentialPassIsSeparateFromTheAgeOfTheLastPass(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	pass(t, s, id, 1_000, "ok", "state", true, 30)
	pass(t, s, id, 90_000, "soft-block", "", false, 0)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	h := got[0]
	if h.LastPassAt != 90_000 {
		t.Errorf("LastPassAt = %d, want 90000", h.LastPassAt)
	}
	if h.LastEvidentialAt != 1_000 {
		t.Errorf("LastEvidentialAt = %d, want 1000", h.LastEvidentialAt)
	}
	if h.Passes != 2 || h.Evidential != 1 {
		t.Errorf("passes %d of which evidential %d, want 2 and 1", h.Passes, h.Evidential)
	}
}

// TestAWatchThatHasNeverBeenReadSaysSoRatherThanReportingNineteenSeventy.
func TestAWatchThatHasNeverBeenReadSaysSoRatherThanReportingNineteenSeventy(t *testing.T) {
	s := openTemp(t)
	addTestWatch(t, s, "новое", 300)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("a watch with no passes is missing from the health list: %+v", got)
	}
	if got[0].LastEvidentialAt != 0 || got[0].LastPassAt != 0 {
		t.Errorf("%+v; zero means «never», and the screen renders that as a word", got[0])
	}
}

// TestCardYieldIsNotArmedUntilThereAreTenPasses.
//
// Spec §Being seen as a bot: it needs ten ok passes before it means anything, so
// until a watch has them the alarm is not armed. A ratio computed from three
// passes is a number that means nothing dressed as one that means something.
func TestCardYieldIsNotArmedUntilThereAreTenPasses(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for i := 0; i < 9; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	got, err := s.WatchHealth(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CardYieldOK {
		t.Errorf("the alarm is armed on nine passes: %+v", got[0])
	}

	pass(t, s, id, 20_000, "ok", "dom", true, 30)
	got, err = s.WatchHealth(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].CardYieldOK {
		t.Error("ten passes did not arm the alarm")
	}
	if got[0].CardYield < 0.99 || got[0].CardYield > 1.01 {
		t.Errorf("CardYield = %v on a steady watch, want about 1", got[0].CardYield)
	}
}

// TestCardYieldFallsWhenThePageStartsCarryingFewerCards.
//
// Spec: a fall below half on a 200 with no wall marker means the page is being
// served to us differently. This checks the ratio moves; the screen decides what
// to say about it.
func TestCardYieldFallsWhenThePageStartsCarryingFewerCards(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	pass(t, s, id, 20_000, "ok", "dom", true, 9)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].CardYieldOK {
		t.Fatal("the alarm is not armed")
	}
	if got[0].CardYield > 0.5 {
		t.Errorf("CardYield = %v after the page halved, want under 0.5", got[0].CardYield)
	}
}

// TestOnlyOkPassesCountTowardsCardYield. A soft block returns no cards at all,
// and counting its zero would drag the median to nothing and disarm the one
// alarm that would have caught it.
func TestOnlyOkPassesCountTowardsCardYield(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	for i := 0; i < 5; i++ {
		pass(t, s, id, 20_000+int64(i), "soft-block", "", false, 0)
	}
	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CardYield < 0.99 || got[0].CardYield > 1.01 {
		t.Errorf("CardYield = %v; the soft blocks were counted", got[0].CardYield)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Path|BlockPicture|Evidential|CardYield|NeverBeenRead|Window' -v
```

Expected: FAIL to build — `undefined: PathShares`.

- [ ] **Step 3: Write `internal/store/health.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"sort"
)

// This file is what the Здоровье сбора screen reads.
//
// Its whole reason is one sentence of the spec: a monitor being flagged, a
// monitor being throttled and a quiet market look identical from everywhere
// else in the product. Every figure here exists to tell those three apart.
//
// A figure with no data says so rather than inventing a number — see
// CardYieldOK, and LastEvidentialAt's zero. That is the spec's own rule about
// saturation applied everywhere: a metric that invents a number where it has no
// data is worse than one that says it has none.

// PathShare is how many passes one extraction path answered.
type PathShare struct {
	Path  string
	Count int
}

// PathShares is the share of passes each path answered since a moment.
//
// The earliest warning there is, on the desktop surface: a silent switch from
// state to dom means the hydration catalogue was withheld, which is this
// install having been classified — earlier than a 429 and much earlier than a
// captcha. On mobile dom is what a healthy pass has always answered, so there
// the signal is card yield instead; the screen says which surface it is looking
// at rather than alarming on both.
func (s *Store) PathShares(ctx context.Context, since int64) ([]PathShare, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, count(*) FROM passes
		WHERE started_at >= ?
		GROUP BY path ORDER BY path`, since)
	if err != nil {
		return nil, fmt.Errorf("store: path shares: %w", err)
	}
	defer rows.Close()

	var out []PathShare
	for rows.Next() {
		var p PathShare
		if err := rows.Scan(&p.Path, &p.Count); err != nil {
			return nil, fmt.Errorf("store: path shares: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClassHour is one bucket of the block picture.
type ClassHour struct {
	Hour  int64
	Class string
	Count int
}

// ClassByHour is the block picture: what the site answered, by class and hour.
//
// By hour rather than by day because throttling arrives and clears inside one,
// and a daily bucket shows a install that was blocked for forty minutes as a
// day that was mostly fine.
func (s *Store) ClassByHour(ctx context.Context, since int64) ([]ClassHour, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT (started_at / 3600) * 3600 AS hour, class, count(*)
		FROM passes
		WHERE started_at >= ?
		GROUP BY hour, class
		ORDER BY hour, class`, since)
	if err != nil {
		return nil, fmt.Errorf("store: class by hour: %w", err)
	}
	defer rows.Close()

	var out []ClassHour
	for rows.Next() {
		var c ClassHour
		if err := rows.Scan(&c.Hour, &c.Class, &c.Count); err != nil {
			return nil, fmt.Errorf("store: class by hour: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WatchHealth is one watch's state of collection.
type WatchHealth struct {
	WatchID    int64
	Name       string
	Surface    string
	Passes     int
	Evidential int
	// LastEvidentialAt is zero when there has never been one, and the screen
	// renders that as a word. A watch created five minutes ago and a watch that
	// has been soft-blocked since March are both «no evidential pass», and 1970
	// is not a date either of them has anything to do with.
	LastEvidentialAt int64
	LastPassAt       int64
	// CardYield is the newest ok pass's items_seen over the median of the last
	// ten. CardYieldOK is false until there are ten, because the alarm is not
	// armed before that and a ratio from three passes is a number that means
	// nothing dressed as one that means something.
	CardYield   float64
	CardYieldOK bool
}

// cardYieldWindow is how many ok passes the median is taken over. The spec's
// number, and it is what arms the alarm.
const cardYieldWindow = 10

// WatchHealth is every watch with its collection state, in id order.
//
// Every watch, including one that has never been read: a watch missing from
// this screen is a watch nobody can see is broken.
func (s *Store) WatchHealth(ctx context.Context, since int64) ([]WatchHealth, error) {
	watches, err := s.Watches(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WatchHealth, 0, len(watches))
	for _, w := range watches {
		h := WatchHealth{WatchID: w.ID, Name: w.Name, Surface: string(w.Surface)}
		if err := s.db.QueryRowContext(ctx, `
			SELECT count(*),
			       coalesce(sum(evidential), 0),
			       coalesce(max(started_at), 0),
			       coalesce(max(CASE WHEN evidential = 1 THEN started_at END), 0)
			FROM passes WHERE watch_id = ? AND started_at >= ?`,
			w.ID, since).Scan(&h.Passes, &h.Evidential, &h.LastPassAt,
			&h.LastEvidentialAt); err != nil {
			return nil, fmt.Errorf("store: watch health %d: %w", w.ID, err)
		}
		h.CardYield, h.CardYieldOK, err = s.cardYield(ctx, w.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// cardYield is the newest ok pass's items_seen against the median of the last
// ten ok passes.
//
// Only ok passes. A soft block returns no cards at all, and counting its zero
// would drag the median to nothing and disarm the one alarm that would have
// caught it.
//
// The median and not the mean, because one busy pass in ten moves a mean and
// does not move a median — and what this is looking for is a step change in what
// the page carries, not a busy afternoon.
func (s *Store) cardYield(ctx context.Context, watchID int64) (float64, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT items_seen FROM passes
		WHERE watch_id = ? AND class = 'ok'
		ORDER BY id DESC LIMIT ?`, watchID, cardYieldWindow+1)
	if err != nil {
		return 0, false, fmt.Errorf("store: card yield: %w", err)
	}
	defer rows.Close()

	var seen []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return 0, false, fmt.Errorf("store: card yield: %w", err)
		}
		seen = append(seen, n)
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("store: card yield: %w", err)
	}
	if len(seen) < cardYieldWindow {
		return 0, false, nil
	}

	newest := seen[0]
	window := append([]int(nil), seen[:cardYieldWindow]...)
	sort.Ints(window)
	median := float64(window[len(window)/2])
	if median == 0 {
		// Every one of the last ten ok passes returned nothing. That is a real
		// state and it is not a ratio: the screen says «страница пуста», and a
		// division here would be by zero.
		return 0, false, nil
	}
	return float64(newest) / median, true, nil
}
```

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS, the existing ones included.

Note on `TestCardYieldIsNotArmedUntilThereAreTenPasses`: with ten passes of 30,
the window is the ten most recent, the newest is 30 and the median is 30, so the
ratio is 1. With eleven where the newest is 9, the window still holds the newest
plus nine thirties, so the median is 30 and the ratio is 0.3.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store && \
  git commit -m "feat(store): the reads that tell a flagged monitor from a quiet market"
```

---

## Task 3: `internal/store/history.go` — one thread's price series

**Files:**
- Create: `internal/store/history.go`, `internal/store/history_test.go`

The chart is **per thread**, not per listing, because a listing has
incarnations: a seller who reposts the same phone produces a new item id, and a
history drawn per listing restarts from nothing every time — which is exactly
the history a person opened the screen to avoid.

**Interfaces:**
- Produces:

```go
// In internal/store:
type PricePoint struct {
    TS int64
    // Minor is the price in kopecks. Present is false for a reading where the
    // price was read and was not a number — «Цена договорная» — which is a
    // different fact from no reading at all, and the chart breaks the line on it.
    Minor   int64
    Present bool
}
type ThreadSeries struct {
    ThreadID int64
    Title    string   // the newest incarnation's
    URLPath  string
    ItemIDs  []int64  // every incarnation, oldest first
    Points   []PricePoint
}
func (s *Store) ThreadSeries(ctx context.Context, threadID int64, since int64) (ThreadSeries, error)
func (s *Store) ThreadsWithHistory(ctx context.Context, watchID int64, limit int) ([]ThreadSeries, error)
```

`ThreadsWithHistory` returns the heads only — id, title, path, no points — for
the list the screen draws before anything is picked.

- [ ] **Step 1: Write the failing test**

Create `internal/store/history_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

func listing(t *testing.T, s *Store, itemID, threadID int64, title string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, 0, 0)`,
		itemID, threadID, "/m/x_"+title, title); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, s *Store, itemID, ts int64, kind string, minor int64) {
	t.Helper()
	var price any
	if kind == "exact" {
		price = minor
	}
	if _, err := s.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, fingerprint, source, path, price_kind, price_minor)
		VALUES (?, 'mobile', ?, 'fp', 'search', 'dom', ?, ?)`,
		itemID, ts, kind, price); err != nil {
		t.Fatal(err)
	}
}

// TestTheSeriesIsPerThreadAndNotPerListing.
//
// A seller who reposts the same phone produces a new item id. A history drawn
// per listing restarts from nothing every time, which is exactly the history a
// person opened this screen to avoid.
func TestTheSeriesIsPerThreadAndNotPerListing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	listing(t, s, 100, 100, "первое")
	listing(t, s, 200, 100, "второе") // a repost: same thread

	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 2_000, "exact", 3_900_000)
	snapshot(t, s, 200, 3_000, "exact", 3_800_000)

	got, err := s.ThreadSeries(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 {
		t.Fatalf("%d points, want the whole thread's three", len(got.Points))
	}
	if got.Points[0].TS != 1_000 || got.Points[2].TS != 3_000 {
		t.Errorf("points = %+v, want oldest first", got.Points)
	}
	if len(got.ItemIDs) != 2 {
		t.Errorf("ItemIDs = %v, want both incarnations", got.ItemIDs)
	}
}

// TestAPriceThatWasReadAndWasNotANumberIsNotZero.
//
// «Цена договорная» is the seller saying something. Drawn as a zero it is a
// price crash that never happened; dropped, it is a line joined across a stretch
// where the price was known not to exist.
func TestAPriceThatWasReadAndWasNotANumberIsNotZero(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "объявление")
	snapshot(t, s, 100, 1_000, "exact", 4_000_000)
	snapshot(t, s, 100, 2_000, "negotiable", 0)
	snapshot(t, s, 100, 3_000, "exact", 3_800_000)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 {
		t.Fatalf("%d points, want three", len(got.Points))
	}
	if got.Points[1].Present {
		t.Error("a negotiable price came back as present")
	}
	if got.Points[1].Minor != 0 {
		t.Errorf("Minor = %d on an absent price; the chart reads Present, not this",
			got.Points[1].Minor)
	}
	if !got.Points[0].Present || !got.Points[2].Present {
		t.Error("a real price came back as absent")
	}
}

// TestAPriceWeCouldNotReadIsAlsoNotZero, and is the same kind of absence: our
// blindness is not the seller withdrawing a price, but neither is a number.
func TestAPriceWeCouldNotReadIsAlsoNotZero(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "объявление")
	snapshot(t, s, 100, 1_000, "absent", 0)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 1 || got.Points[0].Present {
		t.Errorf("points = %+v", got.Points)
	}
}

// TestTheTitleIsTheNewestIncarnations, because that is what the listing is
// called now, and a chart headed with a title from March is a chart of
// something else.
func TestTheTitleIsTheNewestIncarnations(t *testing.T) {
	s := openTemp(t)
	listing(t, s, 100, 100, "старое имя")
	listing(t, s, 200, 100, "новое имя")
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 200, 2_000, "exact", 100)

	got, err := s.ThreadSeries(context.Background(), 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "новое имя" {
		t.Errorf("Title = %q", got.Title)
	}
}

// TestAThreadWithNothingInItIsNotAnError. A person following a link to a thread
// whose snapshots retention has thinned away gets an empty chart and a sentence,
// not a five hundred.
func TestAThreadWithNothingInItIsNotAnError(t *testing.T) {
	got, err := openTemp(t).ThreadSeries(context.Background(), 999, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 0 || got.ThreadID != 999 {
		t.Errorf("got %+v", got)
	}
}

// TestTheListOffersTheThreadsThisWatchActuallySaw.
func TestTheListOffersTheThreadsThisWatchActuallySaw(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	listing(t, s, 100, 100, "моё")
	listing(t, s, 300, 300, "чужое")
	if _, err := s.DB().Exec(
		`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
		 VALUES (?, 100, 1, 0, 0)`, id); err != nil {
		t.Fatal(err)
	}
	snapshot(t, s, 100, 1_000, "exact", 100)
	snapshot(t, s, 300, 1_000, "exact", 100)

	got, err := s.ThreadsWithHistory(ctx, id, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ThreadID != 100 {
		t.Errorf("threads = %+v, want only this watch's", got)
	}
	if len(got[0].Points) != 0 {
		t.Error("the list carries points; it is a list of heads")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Thread|Price' -v
```

Expected: FAIL to build — `undefined: ThreadSeries`.

Before writing the implementation, check the two tables the fixtures write to:

```bash
cd S:/Projects/Avito_Go && grep -n "CREATE TABLE listings" -A 8 internal/store/migrations/0001_avito.sql && \
  grep -n "CREATE TABLE watch_members" -A 12 internal/store/migrations/0001_avito.sql
```

If `listings` or `watch_members` has a NOT NULL column the fixture does not set,
add it to the fixture rather than to the schema — the fixture is what is wrong.

- [ ] **Step 3: Write `internal/store/history.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// This file is what the История screen reads.
//
// Per thread and not per listing, because a listing has incarnations: a seller
// who reposts the same phone produces a new number, and a history drawn per
// listing restarts from nothing every time — which is exactly the history a
// person opened the screen to avoid.

// PricePoint is one reading.
type PricePoint struct {
	TS int64
	// Minor is kopecks, and it is only meaningful when Present is true.
	//
	// Present is false for a reading where the price was read and was not a
	// number — «Цена договорная» — and for one where we could not read it at
	// all. Both are absences and neither is a zero: drawn as zero either is a
	// price crash that never happened.
	Minor   int64
	Present bool
}

// ThreadSeries is one advertisement's history across every number it has worn.
type ThreadSeries struct {
	ThreadID int64
	Title    string
	URLPath  string
	ItemIDs  []int64
	Points   []PricePoint
}

// ThreadSeries reads one thread's price history, oldest point first.
//
// Oldest first because internal/chart says its points must rise in TS and does
// not re-sort — a chart that quietly reordered its input would hide the caller's
// bug behind a plausible picture.
func (s *Store) ThreadSeries(ctx context.Context, threadID, since int64) (ThreadSeries, error) {
	out := ThreadSeries{ThreadID: threadID}

	// The head: the newest incarnation's title, because that is what the
	// listing is called now. A chart headed with a title from March is a chart
	// of something else.
	err := s.db.QueryRowContext(ctx, `
		SELECT title, url_path FROM listings
		WHERE thread_id = ?
		ORDER BY first_seen_at DESC, item_id DESC LIMIT 1`, threadID).
		Scan(&out.Title, &out.URLPath)
	if err != nil && err != sql.ErrNoRows {
		return out, fmt.Errorf("store: thread %d: %w", threadID, err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.item_id, s.ts, s.price_kind, s.price_minor
		FROM listing_snapshots s
		JOIN listings l ON l.item_id = s.item_id
		WHERE l.thread_id = ? AND s.ts >= ?
		ORDER BY s.ts, s.id`, threadID, since)
	if err != nil {
		return out, fmt.Errorf("store: thread %d: %w", threadID, err)
	}
	defer rows.Close()

	seenItem := map[int64]bool{}
	for rows.Next() {
		var itemID int64
		var kind string
		var minor sql.NullInt64
		var p PricePoint
		if err := rows.Scan(&itemID, &p.TS, &kind, &minor); err != nil {
			return out, fmt.Errorf("store: thread %d: %w", threadID, err)
		}
		// Present only for a price the site actually stated as a number. The
		// three price kinds are three different statements and the chart needs
		// exactly this one bit of the difference.
		if kind == "exact" && minor.Valid {
			p.Minor, p.Present = minor.Int64, true
		}
		out.Points = append(out.Points, p)
		if !seenItem[itemID] {
			seenItem[itemID] = true
			out.ItemIDs = append(out.ItemIDs, itemID)
		}
	}
	return out, rows.Err()
}

// ThreadsWithHistory is the list the screen offers before anything is picked.
//
// Heads only — no points. Loading every series to draw a list of names would
// read the whole snapshot table to render a dropdown.
func (s *Store) ThreadsWithHistory(ctx context.Context, watchID int64, limit int) ([]ThreadSeries, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.thread_id,
		       (SELECT title FROM listings h WHERE h.thread_id = l.thread_id
		        ORDER BY h.first_seen_at DESC, h.item_id DESC LIMIT 1),
		       (SELECT url_path FROM listings h WHERE h.thread_id = l.thread_id
		        ORDER BY h.first_seen_at DESC, h.item_id DESC LIMIT 1),
		       count(DISTINCT s.id)
		FROM watch_members m
		JOIN listings l ON l.item_id = m.item_id
		JOIN listing_snapshots s ON s.item_id = l.item_id
		WHERE m.watch_id = ?
		GROUP BY l.thread_id
		ORDER BY max(s.ts) DESC
		LIMIT ?`, watchID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: threads with history: %w", err)
	}
	defer rows.Close()

	var out []ThreadSeries
	for rows.Next() {
		var t ThreadSeries
		var points int
		if err := rows.Scan(&t.ThreadID, &t.Title, &t.URLPath, &points); err != nil {
			return nil, fmt.Errorf("store: threads with history: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store && \
  git commit -m "feat(store): one thread's price history, across every number it has worn"
```

---

## Task 4: Здоровье сбора

**Files:**
- Create: `internal/web/health.go`, `internal/web/health_test.go`
- Modify: `internal/web/server.go` (route and tab)

**Interfaces:**
- Consumes: `store.PathShares`, `store.ClassByHour`, `store.WatchHealth`, `store.Saturation`, `store.Volume`.
- Produces: `func (s *Server) healthPage(http.ResponseWriter, *http.Request)`

- [ ] **Step 1: Write the failing test**

Create `internal/web/health_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"strings"
	"testing"
)

func aPass(t *testing.T, s *Server, watchID, at int64, class, path string, evidential bool, items int) {
	t.Helper()
	_, err := s.Store.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, path,
		   mode, evidential, items_seen)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, 'direct', ?, ?)`,
		watchID, at, at, class, path, evidential, items)
	if err != nil {
		t.Fatal(err)
	}
}

func aWatch(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	rec := post(t, s, "/watches",
		addWatchForm(name, "https://m.avito.ru/all?q="+name, 300))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("adding a watch: %s", rec.Body.String())
	}
	ws, err := s.Store.Watches(t.Context())
	if err != nil || len(ws) == 0 {
		t.Fatalf("watches = %+v (%v)", ws, err)
	}
	return ws[len(ws)-1].ID
}

// TestTheScreenIsReachable.
func TestTheScreenIsReachable(t *testing.T) {
	if rec := get(t, newServer(t), "/health"); rec.Code != http.StatusOK {
		t.Errorf("GET /health = %d", rec.Code)
	}
}

// TestShapeIsLabelledAsOursAndNotTheirs.
//
// The spec asks for exactly this. «shape» filed with the site's refusals reads
// as the site blocking us; it is our parser having stopped understanding the
// payload, which is our bug and a different thing to go and fix.
func TestShapeIsLabelledAsOursAndNotTheirs(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, 3600, "shape", "", false, 0)
	aPass(t, s, id, 3600, "throttled", "", false, 0)

	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "наш разбор") {
		t.Errorf("shape is not marked as ours: %s", body)
	}
}

// TestAWatchWithNoEvidentialPassSaysSoRatherThanShowing1970.
//
// The one line that separates a quiet market from a dead collector, per the
// spec. A date of 1 January 1970 is neither.
func TestAWatchWithNoEvidentialPassSaysSoRatherThanShowing1970(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, 90_000, "soft-block", "", false, 0)

	body := get(t, s, "/health").Body.String()
	if strings.Contains(body, "1970") {
		t.Error("the screen shows the epoch as a date")
	}
	if !strings.Contains(body, "ни разу") {
		t.Errorf("the screen does not say there has never been one: %s", body)
	}
}

// TestTheTwoAgesAreBothShown.
//
// A watch polling every five minutes and answering soft-block every time has a
// recent last pass and an ancient last evidential one, and only the second says
// anything is wrong. Showing one of the two would hide exactly that.
func TestTheTwoAgesAreBothShown(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aPass(t, s, id, 1_000, "ok", "dom", true, 30)
	aPass(t, s, id, 1_699_000_000, "soft-block", "", false, 0)

	body := get(t, s, "/health").Body.String()
	for _, want := range []string{"последний проход", "последний доказательный"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestCardYieldSaysItIsNotArmedRatherThanShowingAZero.
//
// A ratio of nought on a healthy new watch would read as a total failure of
// extraction. «Ещё не набралось десяти проходов» is the truth.
func TestCardYieldSaysItIsNotArmedRatherThanShowingAZero(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := 0; i < 3; i++ {
		aPass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "не набралось") {
		t.Errorf("the screen does not say the alarm is unarmed: %s", body)
	}
}

// TestSaturationWithNoArrivalsIsAQuietQueryAndNotAnAlarm.
//
// Spec: zero arrivals across the whole window is not a division by zero and not
// an alarm — it is a quiet query, reported as «нечего измерять» rather than as a
// ratio, because a metric that invents a number where it has no data is worse
// than one that says it has none.
func TestSaturationWithNoArrivalsIsAQuietQueryAndNotAnAlarm(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	for i := 0; i < 10; i++ {
		aPass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "нечего измерять") {
		t.Errorf("a quiet query is not reported as one: %s", body)
	}
	if strings.Contains(body, "теряет объявления") {
		t.Error("a quiet query raised the undersampled alarm")
	}
}

// TestTheDatabaseSizeAndRowCountsAreShown, which is where somebody looks when
// the disk is filling.
func TestTheDatabaseSizeAndRowCountsAreShown(t *testing.T) {
	s := newServer(t)
	aWatch(t, s, "наблюдение")
	body := get(t, s, "/health").Body.String()
	for _, want := range []string{"МБ", "passes", "listing_snapshots"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestAnEmptyInstallIsNotAWallOfZeroes.
//
// A fresh install has no passes at all, and a screen of «0%» and «0» reads as a
// broken collector rather than as one that has not run yet.
func TestAnEmptyInstallIsNotAWallOfZeroes(t *testing.T) {
	body := get(t, newServer(t), "/health").Body.String()
	if !strings.Contains(body, "ещё не было ни одного прохода") {
		t.Errorf("an install with no passes does not say so: %s", body)
	}
}

// TestTheHealthTabIsInTheHeader, and reachable.
func TestTheHealthTabIsInTheHeader(t *testing.T) {
	s := newServer(t)
	found := false
	for _, tab := range s.tabs("/") {
		if tab.Href == "/health" {
			found = true
		}
	}
	if !found {
		t.Error("there is no tab for the health screen")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'Health|Shape|Evidential|CardYield|Saturation|Database|EmptyInstall|TwoAges' -v
```

Expected: FAIL — 404, because there is no route yet.

- [ ] **Step 3: Add the route and the tab**

In `internal/web/server.go`'s `Handler`:

```go
	mux.Handle("GET /health", s.auth(http.HandlerFunc(s.healthPage)))
```

and in `tabs`, between Наблюдения and Прокси:

```go
		{Label: "Здоровье сбора", Href: "/health"},
```

- [ ] **Step 4: Write `internal/web/health.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Здоровье сбора: the screen that makes the transport honest.
//
// Its whole reason is one sentence of the spec — a monitor being flagged, a
// monitor being throttled and a quiet market look identical from everywhere
// else in the product — and every figure on it exists to tell those three
// apart.
//
// Nothing here invents a number where it has no data. That is the spec's rule
// about saturation, and it governs the rest: an unarmed card-yield alarm says
// «ещё не набралось десяти проходов», a watch never read says «ни разу», and a
// query with no arrivals says «нечего измерять» rather than showing a ratio.

// healthWindow is how far back the shares and the block picture look.
//
// A day, which is the spec's word. Long enough that an hour of throttling does
// not dominate it, short enough that a switch from state to dom this morning is
// not buried under a month of health.
const healthWindow = 24 * time.Hour

func (s *Server) healthPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	since := s.now().Add(-healthWindow).Unix()

	shares, err := s.Store.PathShares(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hours, err := s.Store.ClassByHour(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	health, err := s.Store.WatchHealth(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder

	total := 0
	for _, sh := range shares {
		total += sh.Count
	}
	if total == 0 {
		b.WriteString(`<div class="card">` + note("warning",
			"За последние сутки ещё не было ни одного прохода. "+
				"Это не поломка, если наблюдения только что созданы.") + `</div>`)
	}

	b.WriteString(s.pathCard(shares, total))
	b.WriteString(s.watchCards(ctx, health))
	b.WriteString(s.blockCard(hours))
	b.WriteString(s.volumeCard(ctx))

	s.render(w, r, page{Title: "Здоровье сбора", Body: rawHTML(b.String())})
}

// pathCard is the extraction-path share, and what a shift in it means.
func (s *Server) pathCard(shares []store.PathShare, total int) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Чем разобраны страницы</h2>`)
	if total == 0 {
		b.WriteString(note("neutral", "Нет проходов за сутки."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(`<table><tr><th>Путь</th><th class="num">Проходов</th>` +
		`<th class="num">Доля</th></tr>`)
	dom := 0
	for _, sh := range shares {
		label := sh.Path
		switch sh.Path {
		case "state":
			label = "состояние страницы (state)"
		case "dom":
			label = "разметка (dom)"
			dom = sh.Count
		case "":
			label = "не дошло до разбора"
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td>`+
			`<td class="num">%s%%</td></tr>`,
			esc(label), sh.Count, esc(num(100*float64(sh.Count)/float64(total))))
	}
	b.WriteString(`</table>`)
	// The alarm is about the desktop surface only, and the screen says so
	// rather than alarming on mobile where dom is what a healthy pass has
	// always answered.
	if dom > 0 {
		b.WriteString(note("neutral",
			"На десктопной версии переход со «состояния» на «разметку» — самый "+
				"ранний признак, что установку пометили: раньше, чем 429, и "+
				"намного раньше капчи. На мобильной версии «разметка» — это "+
				"норма, и там тот же вопрос отвечает съём карточек ниже."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// watchCards is the per-watch half: the two ages, the evidential fraction, the
// saturation figure and the card yield.
func (s *Server) watchCards(ctx context.Context, health []store.WatchHealth) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Наблюдения</h2>`)
	if len(health) == 0 {
		b.WriteString(note("neutral", "Наблюдений пока нет."))
		b.WriteString(`</div>`)
		return b.String()
	}
	b.WriteString(`<table><tr><th>Наблюдение</th><th>Последний проход</th>` +
		`<th>Последний доказательный</th><th class="num">Доказательных</th>` +
		`<th>Насыщенность</th><th>Съём карточек</th></tr>`)

	now := s.now().Unix()
	for _, h := range health {
		fmt.Fprintf(&b, `<tr><td>%s<div class="hint">%s</div></td>`,
			esc(h.Name), esc(h.Surface))
		fmt.Fprintf(&b, `<td>%s</td>`, esc(ago(h.LastPassAt, now)))
		// The two ages side by side, because only the second says anything is
		// wrong when a watch answers soft-block every five minutes.
		fmt.Fprintf(&b, `<td>%s</td>`, esc(ago(h.LastEvidentialAt, now)))
		if h.Passes == 0 {
			b.WriteString(`<td class="num">—</td>`)
		} else {
			fmt.Fprintf(&b, `<td class="num">%d из %d</td>`, h.Evidential, h.Passes)
		}
		fmt.Fprintf(&b, `<td>%s</td>`, s.saturationCell(ctx, h.WatchID))
		fmt.Fprintf(&b, `<td>%s</td></tr>`, cardYieldCell(h))
	}
	b.WriteString(`</table>`)
	b.WriteString(note("neutral",
		"«Последний доказательный» — это единственная строка, которая отличает "+
			"тихий рынок от мёртвого сбора: наблюдение может опрашиваться "+
			"каждые пять минут и всё это время отвечать мягкой блокировкой."))
	b.WriteString(`</div>`)
	return b.String()
}

// saturationCell is items_on_page / arrivals over the last ten evidential
// passes.
//
// Zero arrivals is a quiet query and not an alarm: the spec is explicit that it
// is neither a division by zero nor a problem, and is reported as having nothing
// to measure rather than as a ratio.
func (s *Server) saturationCell(ctx context.Context, watchID int64) string {
	v, ok, err := s.Store.Saturation(ctx, watchID, 10)
	if err != nil || !ok {
		return `<span class="pill">нечего измерять</span>`
	}
	if v < 2 {
		return fmt.Sprintf(`<span class="pill bad">%s</span>`+
			`<div class="hint">теряет объявления между опросами</div>`, esc(num(v)))
	}
	return fmt.Sprintf(`<span class="pill ok">%s</span>`, esc(num(v)))
}

// cardYieldCell is the mobile half of the flagged-install question.
func cardYieldCell(h store.WatchHealth) string {
	if !h.CardYieldOK {
		return `<span class="pill">ещё не набралось десяти проходов</span>`
	}
	if h.CardYield < 0.5 {
		return fmt.Sprintf(`<span class="pill bad">%s</span>`+
			`<div class="hint">страница отдаёт вдвое меньше карточек, чем обычно</div>`,
			esc(num(h.CardYield)))
	}
	return fmt.Sprintf(`<span class="pill ok">%s</span>`, esc(num(h.CardYield)))
}

// blockCard is the picture of what the site answered, by class and hour.
func (s *Server) blockCard(hours []store.ClassHour) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Что отвечала площадка</h2>`)
	if len(hours) == 0 {
		b.WriteString(note("neutral", "Нет проходов за сутки."))
		b.WriteString(`</div>`)
		return b.String()
	}

	byHour := map[int64]map[string]int{}
	classes := map[string]bool{}
	var order []int64
	for _, h := range hours {
		if byHour[h.Hour] == nil {
			byHour[h.Hour] = map[string]int{}
			order = append(order, h.Hour)
		}
		byHour[h.Hour][h.Class] += h.Count
		classes[h.Class] = true
	}
	names := make([]string, 0, len(classes))
	for c := range classes {
		names = append(names, c)
	}
	sort.Strings(names)

	b.WriteString(`<table><tr><th>Час</th>`)
	for _, c := range names {
		fmt.Fprintf(&b, `<th class="num">%s</th>`, esc(classLabel(c)))
	}
	b.WriteString(`</tr>`)
	for _, hour := range order {
		fmt.Fprintf(&b, `<tr><td>%s</td>`,
			esc(time.Unix(hour, 0).UTC().Format("02.01 15:04")))
		for _, c := range names {
			n := byHour[hour][c]
			if n == 0 {
				b.WriteString(`<td class="num">—</td>`)
				continue
			}
			fmt.Fprintf(&b, `<td class="num">%d</td>`, n)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)
	if classes["shape"] {
		b.WriteString(note("warning",
			"«Разметка не разобрана» — это наш разбор, а не отказ площадки: "+
				"страница пришла, а наш код перестал её понимать. Чинить надо "+
				"здесь, а не ждать снятия блокировки."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// classLabel is what a class is called on screen.
//
// «shape» reads as «наш разбор» because it is ours: the spec asks for it to be
// labelled as ours rather than theirs, and filed with the site's refusals it
// would send somebody to wait out a block that is not happening.
func classLabel(c string) string {
	switch c {
	case "ok":
		return "в порядке"
	case "empty":
		return "пусто"
	case "shape":
		return "разметка не разобрана (наш разбор)"
	case "soft-block":
		return "мягкая блокировка"
	case "cold":
		return "холодная личность"
	case "throttled":
		return "притормозили"
	case "burned":
		return "личность сожжена"
	case "challenge":
		return "капча"
	case "mangled":
		return "битые байты"
	case "other":
		return "прочее"
	}
	return c
}

func (s *Server) volumeCard(ctx context.Context) string {
	v, err := s.Store.Volume(ctx)
	if err != nil {
		return `<div class="card">` + note("bad", err.Error()) + `</div>`
	}
	names := make([]string, 0, len(v.Rows))
	for n := range v.Rows {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(`<div class="card"><h2>База</h2>`)
	fmt.Fprintf(&b, `<p class="sum">Файл занимает <strong>%s МБ</strong>.</p>`,
		esc(num(float64(v.FileBytes)/(1<<20))))
	b.WriteString(`<table><tr><th>Таблица</th><th class="num">Строк</th></tr>`)
	for _, n := range names {
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td></tr>`,
			esc(n), v.Rows[n])
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// ago renders a moment as how long ago it was.
//
// Zero is «ни разу», not the epoch. A watch created five minutes ago and a watch
// soft-blocked since March are both «no evidential pass», and 1 January 1970 is
// a date neither has anything to do with.
func ago(at, now int64) string {
	if at <= 0 {
		return "ни разу"
	}
	d := now - at
	switch {
	case d < 0:
		return "только что"
	case d < 60:
		return fmt.Sprintf("%d %s назад", d, ru.Plural(int(d), "секунду", "секунды", "секунд"))
	case d < 3600:
		m := d / 60
		return fmt.Sprintf("%d %s назад", m, ru.Plural(int(m), "минуту", "минуты", "минут"))
	case d < 86400:
		h := d / 3600
		return fmt.Sprintf("%d %s назад", h, ru.Plural(int(h), "час", "часа", "часов"))
	}
	days := d / 86400
	return fmt.Sprintf("%d %s назад", days, ru.Plural(int(days), "день", "дня", "дней"))
}
```

- [ ] **Step 5: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go vet ./internal/web/ && \
  go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS, 6A's included — `TestEveryTabIsReachable` now covers
the new tab and must still pass.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && \
  git commit -m "feat(web): Здоровье сбора, where a quiet market and a dead collector differ"
```

---

## Task 5: История

**Files:**
- Create: `internal/web/history.go`, `internal/web/history_test.go`
- Modify: `internal/web/server.go` (two routes and a tab)

Two routes, because a chart is an image: `GET /history` renders the page and
`GET /history/chart?thread=N` renders the PNG the `<img>` points at.

**Interfaces:**
- Consumes: `store.ThreadSeries`, `store.ThreadsWithHistory`, `chart.Line`.
- Produces: `historyPage`, `historyChart`.

- [ ] **Step 1: Write the failing test**

Create `internal/web/history_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"image"
	_ "image/png"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func aListing(t *testing.T, s *Server, itemID, threadID int64, title string) {
	t.Helper()
	if _, err := s.Store.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, 0, 0)`,
		itemID, threadID, "/m/x_"+strconv.FormatInt(itemID, 10), title); err != nil {
		t.Fatal(err)
	}
}

func aSnapshot(t *testing.T, s *Server, itemID, ts int64, kind string, minor int64) {
	t.Helper()
	var price any
	if kind == "exact" {
		price = minor
	}
	if _, err := s.Store.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, fingerprint, source, path, price_kind, price_minor)
		VALUES (?, 'mobile', ?, 'fp', 'search', 'dom', ?, ?)`,
		itemID, ts, kind, price); err != nil {
		t.Fatal(err)
	}
}

// TestTheChartIsAPng, so the <img> on the page is not a promise.
func TestTheChartIsAPng(t *testing.T) {
	s := newServer(t)
	aListing(t, s, 100, 100, "iPhone 15")
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 100, 1_700_003_600, "exact", 3_500_000)

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
	img, format, err := image.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || img.Bounds().Dx() == 0 {
		t.Errorf("format=%q bounds=%v", format, img.Bounds())
	}
}

// TestAThreadWithNoPricesSaysSoRatherThanDrawingAnEmptyGrid.
//
// A picture of an empty grid looks like a product whose price was nothing, and
// prompts a second look at a working program.
func TestAThreadWithNoPricesSaysSoRatherThanDrawingAnEmptyGrid(t *testing.T) {
	s := newServer(t)
	aListing(t, s, 100, 100, "договорная")
	aSnapshot(t, s, 100, 1_700_000_000, "negotiable", 0)

	rec := get(t, s, "/history/chart?thread=100")
	if rec.Code == http.StatusOK && rec.Header().Get("Content-Type") == "image/png" {
		t.Error("a thread with no prices drew a chart")
	}
	if !strings.Contains(rec.Body.String(), "нет данных") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestThePageLinksTheChartAndNamesTheThread.
func TestThePageLinksTheChartAndNamesTheThread(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15, 128 ГБ")
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	if _, err := s.Store.DB().Exec(
		`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
		 VALUES (?, 100, 1, 0, 0)`, id); err != nil {
		t.Fatal(err)
	}

	body := get(t, s, "/history?thread=100").Body.String()
	if !strings.Contains(body, "/history/chart?thread=100") {
		t.Error("the page does not point an img at the chart")
	}
	if !strings.Contains(body, "iPhone 15, 128 ГБ") {
		t.Error("the page does not name the thread")
	}
}

// TestARepostIsOneHistoryAndNotTwo.
//
// The whole reason the chart is per thread. Two incarnations of one
// advertisement are one line, and the page says how many numbers it has worn.
func TestARepostIsOneHistoryAndNotTwo(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "старое объявление")
	aListing(t, s, 200, 100, "то же, заново")
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	aSnapshot(t, s, 200, 1_700_100_000, "exact", 3_800_000)
	for _, item := range []int64{100, 200} {
		if _, err := s.Store.DB().Exec(
			`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
			 VALUES (?, ?, 1, 0, 0)`, id, item); err != nil {
			t.Fatal(err)
		}
	}

	body := get(t, s, "/history?thread=100").Body.String()
	if !strings.Contains(body, "2 номера") && !strings.Contains(body, "два номера") {
		t.Errorf("the page does not say the thread has worn two numbers: %s", body)
	}
	if strings.Contains(body, "старое объявление") {
		t.Error("the page is headed with a title the listing no longer has")
	}
}

// TestTheListIsOfferedWhenNothingIsPicked.
func TestTheListIsOfferedWhenNothingIsPicked(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	aListing(t, s, 100, 100, "iPhone 15")
	aSnapshot(t, s, 100, 1_700_000_000, "exact", 4_000_000)
	if _, err := s.Store.DB().Exec(
		`INSERT INTO watch_members (watch_id, item_id, present, first_seen_at, last_seen_at)
		 VALUES (?, 100, 1, 0, 0)`, id); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/history").Body.String()
	if !strings.Contains(body, "iPhone 15") {
		t.Error("the list does not offer the thread")
	}
}

// TestAnEmptyInstallSaysThereIsNothingToShowYet.
func TestAnEmptyInstallSaysThereIsNothingToShowYet(t *testing.T) {
	body := get(t, newServer(t), "/history").Body.String()
	if !strings.Contains(body, "пока нечего показать") {
		t.Errorf("body = %q", body)
	}
}

// TestABadThreadParameterIsRefusedRatherThanRenderingThreadZero.
func TestABadThreadParameterIsRefusedRatherThanRenderingThreadZero(t *testing.T) {
	s := newServer(t)
	for _, bad := range []string{"?thread=слово", "?thread=-1", "?thread="} {
		rec := get(t, s, "/history/chart"+bad)
		if rec.Header().Get("Content-Type") == "image/png" {
			t.Errorf("%q drew a chart", bad)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'Chart|History|Repost|List|Thread' -v
```

Expected: FAIL — 404.

- [ ] **Step 3: Add the routes and the tab**

In `Handler`:

```go
	mux.Handle("GET /history", s.auth(http.HandlerFunc(s.historyPage)))
	mux.Handle("GET /history/chart", s.auth(http.HandlerFunc(s.historyChart)))
```

and in `tabs`, after Здоровье сбора:

```go
		{Label: "История", Href: "/history"},
```

`tabs` marks a tab active on an exact path match, so `/history?thread=100` would
highlight none and fall through to the first. Change the comparison to match the
path prefix for `/history`, or accept the fall-through — **do the first**, and
say why in the code: a person who followed a link into a chart and sees
«Наблюдения» highlighted has been told they are somewhere they are not.

- [ ] **Step 4: Write `internal/web/history.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/chart"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// История: the price chart, per thread.
//
// Per thread and not per listing because a listing has incarnations. A seller
// who reposts the same phone produces a new number, and a history drawn per
// listing restarts from nothing every time — which is exactly what a person
// opened this screen to avoid.
//
// Two routes, because a chart is an image: the page renders the frame and the
// <img> points at the second, which answers a PNG. The alternative — a data URI
// inline — puts a hundred kilobytes of base64 into every page load and into the
// browser's history.

// chartMaxGap is how long a silence has to be before the line breaks.
//
// Six hours. The number is the caller's knowledge and not the chart's: only
// this side knows how often a listing was supposed to be read, and the
// difference between «the price held for a week» and «nobody looked for a week»
// is exactly this. Six is comfortably longer than any watch's interval and far
// shorter than the retention that thins old snapshots to one a day.
const chartMaxGap = 6 * 60 * 60

func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var b strings.Builder

	threadID, _ := strconv.ParseInt(r.URL.Query().Get("thread"), 10, 64)
	if threadID > 0 {
		series, err := s.Store.ThreadSeries(ctx, threadID, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		b.WriteString(s.chartCard(series))
	}

	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	offered := 0
	for _, watch := range watches {
		threads, err := s.Store.ThreadsWithHistory(ctx, watch.ID, 100)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(threads) == 0 {
			continue
		}
		offered += len(threads)
		b.WriteString(threadList(watch, threads))
	}
	if offered == 0 && threadID == 0 {
		b.WriteString(`<div class="card">` + note("neutral",
			"Пока нечего показать: история появится, когда наблюдение "+
				"сделает хотя бы один проход и запишет цены.") + `</div>`)
	}

	s.render(w, r, page{Title: "История", Body: rawHTML(b.String())})
}

func (s *Server) chartCard(series store.ThreadSeries) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	title := series.Title
	if title == "" {
		title = "объявление " + strconv.FormatInt(series.ThreadID, 10)
	}
	fmt.Fprintf(&b, `<h2>%s</h2>`, esc(title))

	if n := len(series.ItemIDs); n > 1 {
		// Said out loud, because it is the reason this chart is one line and
		// not several: the same advertisement has worn several numbers.
		fmt.Fprintf(&b, `<p class="hint">Одно объявление под %d %s — `+
			`график сшит по всем.</p>`,
			n, esc(ru.Plural(n, "номером", "номерами", "номерами")))
	}
	if series.URLPath != "" {
		fmt.Fprintf(&b, `<p><a href="https://www.avito.ru%s" rel="noreferrer">`+
			`Открыть на Авито</a></p>`, attr(series.URLPath))
	}
	fmt.Fprintf(&b, `<p><img src="/history/chart?thread=%d" alt="%s" `+
		`style="max-width:100%%"></p>`,
		series.ThreadID, attr("График цены: "+title))
	b.WriteString(`</div>`)
	return b.String()
}

func threadList(watch store.Watch, threads []store.ThreadSeries) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h2>%s</h2><table>`, esc(watch.Name))
	for _, t := range threads {
		title := t.Title
		if title == "" {
			title = "объявление " + strconv.FormatInt(t.ThreadID, 10)
		}
		fmt.Fprintf(&b, `<tr><td><a href="/history?thread=%d">%s</a></td></tr>`,
			t.ThreadID, esc(title))
	}
	b.WriteString(`</table></div>`)
	return b.String()
}

// historyChart answers the PNG the page's <img> points at.
func (s *Server) historyChart(w http.ResponseWriter, r *http.Request) {
	threadID, err := strconv.ParseInt(r.URL.Query().Get("thread"), 10, 64)
	if err != nil || threadID <= 0 {
		// Not a chart of thread zero: a request this malformed is a link
		// somebody built by hand, and drawing an empty grid for it would look
		// like an answer.
		http.Error(w, "нет такого объявления", http.StatusBadRequest)
		return
	}
	series, err := s.Store.ThreadSeries(r.Context(), threadID, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	points := make([]chart.Point, 0, len(series.Points))
	for _, p := range series.Points {
		pt := chart.Point{TS: p.TS, Value: notANumber()}
		if p.Present {
			// Roubles rather than kopecks on the axis: a Russian reader does
			// not price a phone in kopecks, and four extra digits on every tick
			// label buy nothing.
			pt.Value = float64(p.Minor) / 100
		}
		points = append(points, pt)
	}

	line := chart.Line{
		Series: []chart.Series{{Points: points}},
		MaxGap: chartMaxGap,
		Loc:    time.UTC,
		Y:      chart.Axis{Format: func(v float64) string { return num(v) }},
	}
	var buf strings.Builder
	_ = buf
	w.Header().Set("Content-Type", "image/png")
	if err := line.Encode(w); err != nil {
		// The header is already sent, so this cannot become a 400. Rewriting it
		// is the least bad option and the test asserts the body rather than the
		// status.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "нет данных для графика")
	}
}
```

The `notANumber` helper and the `strings.Builder` above are placeholders the
implementer must remove: use `math.NaN()` directly and delete `buf`. They are
named here so the reviewer sees them go.

**The `Content-Type` ordering is wrong as written and must be fixed:** setting
the header before `Encode` means a failure cannot change it. Restructure to
encode into a `bytes.Buffer` first, and only set the header and write once the
encode succeeded:

```go
	var buf bytes.Buffer
	if err := line.Encode(&buf); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "нет данных для графика")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(buf.Bytes())
```

A chart is at most a few tens of kilobytes, so buffering it costs nothing and
buys a correct status code.

- [ ] **Step 5: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go vet ./internal/web/ && \
  go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && \
  git commit -m "feat(web): История, one line across every number a listing has worn"
```

---

## Task 6: The demonstrable

**Files:**
- Create: `docs/measurements/2026-08-XX-panel-6b.md`

- [ ] **Step 1: Full verification**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./cmd ./internal && go vet ./... && \
  go build ./... && go test ./... 2>&1 | grep -v "no test files"
```

Expected: `gofmt -l` prints nothing, vet clean, every package passes.

- [ ] **Step 2: Drive the two screens against real data**

Collect first, because both screens are about collected data and a screen shown
an empty database proves only that it does not crash:

```bash
cd S:/Projects/Avito_Go && D=/tmp/6b.db && rm -f "$D"* && \
  go run ./cmd/avitomon transport -db "$D" set direct && \
  go run ./cmd/avitomon watch add -db "$D" -name "iphone" "https://m.avito.ru/all?q=iphone+15" && \
  go run ./cmd/avitomon run --once -db "$D"
```

Wait past the watch's interval and run a second pass, so there is a second
snapshot and the chart has a line rather than a point. Then start the panel and
walk both screens.

Record in `docs/measurements/2026-08-XX-panel-6b.md`:

- the path shares the real passes produced, and whether they were `state` or
  `dom` on the mobile surface;
- what the two ages read after two passes;
- whether card yield was armed (it will not be — that needs ten `ok` passes) and
  what the screen said instead;
- what saturation reported, and whether the query was quiet;
- whether the chart drew a line, and how many points it had;
- **anything that did not work.**

**If the browser pane still does not composite frames**, say so in §1 exactly as
6A's write-up did, drive it over HTTP, and state plainly that the chart was
checked as bytes rather than looked at. A PNG that decodes is not a PNG that
reads.

- [ ] **Step 3: Commit**

```bash
cd S:/Projects/Avito_Go && git add docs/measurements && \
  git commit -m "docs(measurements): what the health and history screens actually showed"
```

---

## Done when

- `gofmt -l` prints nothing; `go vet ./...` clean; `go test ./...` passes.
- The chart breaks its line on a reading with no price and on a stretch nobody
  collected, and returns `ErrNoData` rather than drawing an empty grid.
- Здоровье сбора shows the path shares over a day, both ages per watch, the
  evidential fraction, saturation with its two-arrival alarm, card yield with
  its ten-pass arming, the block picture by class and hour, and the database
  size with per-table row counts.
- `shape` is labelled as **ours**, in those words, and not filed with the site's
  refusals.
- A watch that has never been read says «ни разу» and no screen anywhere shows
  1970.
- A quiet query says «нечего измерять» and does not raise the undersampled
  alarm.
- История draws one line across every number a listing has worn, names how many
  there were, and says «нет данных» rather than drawing an empty grid.
- Both tabs are in the header and both are reachable.
- The demonstrable has been performed against real collected data and written
  up, including anything that did not work.

## What this plan deliberately does not build

**Правила and Результаты.** They are 6C.

**A position chart.** `chart.Axis.Invert` is lifted and unused: M1 records
`pass_items.position` but no screen asks about it, and a chart nothing links to
is a screen nobody finds.

**Any alarm that fires.** This increment shows the numbers; `watch-undersampled`
and `extraction-fell-back` are change kinds the collector emits, and wiring the
health figures to emit them belongs where passes are written, not on a screen
that reads them. The screen says the same thing the alarm would, to whoever is
looking — which is the gap, and it is named here rather than hidden.

**Retention of the chart's own data.** Snapshots thin to one a day past thirty
days, so an old chart is coarser than a new one and nothing on the screen says
so. Worth a line of copy in 6C or M2; not worth a mechanism now.

## Self-review

**Spec coverage.** §The panel's Здоровье сбора lists eight things: path shares
(Task 4), evidential fraction (4), saturation with its alarm (4), card yield (2
and 4), the age of the last evidential pass (2 and 4), the block picture with
`ClassShape` as ours (4), `store.Volume` (4), and the halted-watch banner —
which 6A already renders on every screen and is therefore not repeated. История
is Task 5, and its two refusals are Task 1's tests.

**Placeholder scan.** Task 5 Step 4 contains two deliberate placeholders
(`notANumber`, an unused `strings.Builder`) and a **known-wrong** header
ordering, all three called out in prose immediately below the block with the
correction written out. That is a plan telling its implementer where the trap is
rather than hiding one; it is the only such case.

**Type consistency.** `store.PathShare`, `store.ClassHour`, `store.WatchHealth`,
`store.PricePoint`, `store.ThreadSeries` are declared once in Tasks 2 and 3 and
used with the same field names in Tasks 4 and 5. `chart.Point`, `chart.Series`,
`chart.Line`, `chart.Axis` come from Task 1 and are used in Task 5.
`ago(at, now)` is declared in Task 4 and used only there. `aWatch`, `aPass`,
`aListing`, `aSnapshot` are test helpers declared in Tasks 4 and 5;
`historyChart`'s tests use `aWatch` from `health_test.go`, which is the one
cross-file dependency in the suite and is why Task 4 comes first.
