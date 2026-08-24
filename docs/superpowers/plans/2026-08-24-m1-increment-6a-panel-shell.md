# M1 increment 6A — the panel shell and the screens that configure collection

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person can install this program, answer the transport question, add
egress addresses, add a watch and configure everything else from a browser, with
no command line — and the panel refuses to leave the loopback interface until
they have set a password of their own.

**Architecture:** One `internal/web` package: `net/http`'s own router, Go
templates, one stylesheet and one page of hand-written JavaScript, every asset
embedded in the binary. Handlers assemble their own HTML and escape at the point
of interpolation. Everything the panel cannot do without a live third party —
BlankTrail's control API, Telegram, the operating system's autostart entry — is a
function field on `Server`, so the suite needs none of them.

**Tech Stack:** Go 1.26, `net/http`, `html/template`, `crypto/pbkdf2` (standard
library since 1.24 — no new dependency), `modernc.org/sqlite`, `golang.org/x/sys`
for the autostart entry.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §The
panel, and §M1 cut into increments, item 6.

## Scope: increment 6 is three plans, and this is the first

The spec's increment 6 is one sentence covering eight screens, a chart and an
export. The donor has 20 390 lines of `internal/web`, 1 403 of `internal/chart`
and 7 495 of `internal/export`. One plan over that would be a document nobody
executes.

Cut into three, each of which produces working software on its own:

| plan | screens | after it, a person can |
|---|---|---|
| **6A — this one** | shell, first run, Настройки, Наблюдения, Прокси | install and run the whole collector from a browser |
| **6B** | Здоровье сбора, История + `internal/chart` | see whether their collector is healthy, and a price history |
| **6C** | Правила, Результаты + `internal/export` | edit rules and take the data out as CSV/XLSX/JSON |

The increment's own demonstrable — «the whole product is usable from a browser
with no command line» — is met at the end of 6C. 6A is what makes it *runnable*
from a browser; 6B and 6C are what make it *readable*.

## Global Constraints

Copied from the spec; every task's requirements implicitly include these.

- **Go 1.26, no CGO.** `modernc.org/sqlite` is the only SQLite permitted.
- **Eight direct dependencies, and this plan adds none.** `crypto/pbkdf2` is
  standard library as of Go 1.24. `golang.org/x/sys` is already in the budget
  «for the tray and the autostart entry».
- **No plotting, spreadsheet or front-end library.** Spec §7 names htmx; the
  donor does the eighty lines by hand instead, and so does this.
- **`Accept-Language: ru` always** on anything this program fetches.
- **All user-facing copy is Russian**, with correct orthography — «ё» where it
  belongs, «—» not «-». Identifiers, routes and comments stay English.
- **`127.0.0.1` by default. No password by default. The LAN is refused until the
  user sets a password of their own.**
- **Secrets never reach a file this program creates, a log, or an error.** The
  BlankTrail key, the Telegram bot token, the MTProto api_hash and session.
- **The AGPL source link is a constant, not a setting** — a configurable link is
  a link that can be pointed somewhere else.
- **Nothing may quote a throughput number the soak did not produce**, and nothing
  may quote one without its sample size and its single-address caveat.

---

## What already exists, and what this plan must add

Read this before Task 1. The executor has not seen this codebase.

**Exists and is used as-is:**

| thing | where | shape |
|---|---|---|
| `store.Store` | `internal/store/store.go` | `Open(ctx, path)`, `DB() *sql.DB`, `Path() string`, `Close()` |
| the watch row | `internal/store/watches.go` | `store.Watch{ID, Name, Kind, URL, Surface avito.Surface, MaxPages, IntervalSec, AbsenceConfirmations, AbsenceMinSpanSec, State, HaltedReason, SeededAt *int64, NextDueAt, LastTotalCount *int64, TooBroad, Enabled, CreatedAt, UpdatedAt}`; `AddWatch(ctx, Watch) (int64, error)`, `Watch(ctx, id) (Watch, error)`, `DueWatches(ctx, now, limit)`, `MarkHalted`, `SetNextDue` |
| transport choice | `internal/store/settings.go` | `TransportMode(ctx) (transport.Mode, error)` returning `store.ErrNoTransportChosen` when unset; `SetTransportMode(ctx, m)` |
| BlankTrail settings | `internal/store/settings.go` | `BlankTrailConfig{ControlURL, APIKey, Ports}`; `BlankTrailConfig(ctx)`, `SetBlankTrailConfig(ctx, c)` |
| Telegram settings | `internal/store/settings.go` | `TelegramConfig{BotToken, APIID, APIHash}`; `TelegramConfig(ctx)`, `SetTelegramConfig(ctx, c)` |
| volume | `internal/store/volume.go` | `Volume{FileBytes int64, Rows map[string]int64}`; `Volume(ctx)`; `SetSpaceFloor(bytes int64)`; `CheckSpace(ctx)` returning `store.ErrDiskLow` |
| retention | `internal/store/fingerprint.go` | `Retention{SnapshotDailyAfter, SnapshotWeeklyAfter, PassHourlyAfter, PassDailyAfter time.Duration; BodyMaxAge time.Duration; BodyMaxBytes int64; AnchorEvery time.Duration}`; `DefaultRetention()`; `Retention()`, `SetRetention(r)` — **in memory only, nothing persists it** |
| pacing constants | `internal/transport/pace.go` | `DefaultFloor = 3500ms`, `DefaultJitter = 0.2`, `DefaultCeiling = 60`, `DefaultWindow = 10 * time.Minute` |
| transport capabilities | `internal/transport/mode.go` | `Caps{SolvesChallenges, RotatesEgress bool, Addresses int}` |
| proxy line parsing | `blanktrail/upstream.go` | `Parse(raw, defaultScheme string) (ups []Upstream, bad []string)`; `Source{...}.Load(ctx) (ups, bad, err)`; `Upstream{Scheme, Host, Port, Username, Password}` with `URL()` and `Key()` |
| gateway listing | `blanktrail/license.go` | `(*Client).Gateways(ctx) (GatewayList, error)`; `GatewayList{Available []Gateway, Reason string}` |
| control-plane health | `blanktrail/client.go` | `(*Client).Health(ctx) error` |
| the egress mixer | `blanktrail/channel.go` | `NewMixer(chs ...Channel) *Mixer` with per-channel weight decay |
| Telegram delivery | `internal/telegram` | `Ladder{Rungs []Sender}`, `NewBot(token, do)`, `(*Bot).GetMe(ctx)` |
| rules | `internal/rules` | `All(ctx, store)`, `Save(ctx, store, Rule)`, `DefaultsForWatch(watchID, targets)`, `GlobalDefaults(targets)` |

**Does not exist and this plan adds it:**

- `internal/store`: `Watches(ctx)` — the list; channel and proxy CRUD; the panel's
  own settings (password hash, LAN tick, autostart intention); persisted retention.
- Migration `0004` for the settings columns retention needs.
- `internal/ru` — the Russian plural, which `internal/telegram/summary.go`
  currently owns privately and the panel needs too.
- `internal/web` — the whole package.
- `internal/autostart` — lifted from the donor.
- `cmd/avitomon/panel.go` — the command that opens the port.

**Known local facts the executor will trip over otherwise:**

- Bash heredocs on this machine eat backslashes. Use the Write/Edit tools for any
  file containing `\n`, `\t` or a regexp.
- `GOPROXY` is `direct` here, which makes `go mod tidy` fail on a gotd test
  dependency (`nhooyr.io/websocket`, HTTP 502). Run
  `GOPROXY=https://proxy.golang.org,direct go mod tidy` if tidying is needed.
- `store.CheckSpace` refuses to collect below `max(spaceFloorBytes, total/50)`.
  On a 950 GB volume that is 19 GB, so tests that collect must use `t.TempDir()`
  on a volume with room.

---

## File structure

Each file has one screen's worth of responsibility. The donor's `internal/web`
is 20 390 lines in one package with a 1 744-line `jobs.go`; this keeps the
package and splits the file, because a screen is the unit a person reviews.

| file | responsible for |
|---|---|
| `internal/ru/plural.go` | the three Russian number forms, and nothing else |
| `internal/store/migrations/0004_panel.sql` | retention and autostart columns on `settings` |
| `internal/store/panel.go` | `Watches`, panel settings, persisted retention |
| `internal/store/channels.go` | channel and proxy rows |
| `internal/web/server.go` | `Server`, routing, `auth`, `ListenAddress`, `render`, tabs |
| `internal/web/password.go` | hashing and verifying the panel password |
| `internal/web/html.go` | the escaping helpers every handler uses |
| `internal/web/firstrun.go` | the transport question, with its numbers |
| `internal/web/settings.go` | Настройки |
| `internal/web/watches.go` | Наблюдения and the budget arithmetic |
| `internal/web/budget.go` | the arithmetic on its own, testable without HTTP |
| `internal/web/channels.go` | Прокси, its per-entry tests, its ceiling arithmetic |
| `internal/web/templates/layout.html` | the one layout |
| `internal/web/static/monitor.css` | the one stylesheet |
| `internal/web/static/app.js` | the eighty lines that replace htmx |
| `internal/autostart/` | the operating system's start-at-login entry |
| `cmd/avitomon/panel.go` | `avitomon panel` |

`budget.go` is separate from `watches.go` on purpose. The arithmetic is the one
thing on that screen that can be wrong in a way nobody notices — a schedule
accepted that quietly exceeds the address ceiling is a ban three days later —
and it must be testable as arithmetic rather than through a form post.

---

## Task 1: `internal/ru` — one plural, not two

Increment 5 put a Russian plural inside `internal/telegram/summary.go`. The
panel needs the same three forms for «наблюдение / наблюдения / наблюдений» and
«адрес / адреса / адресов». A second copy is a second place to get the teens
wrong, and only one of them would be tested.

**Files:**
- Create: `internal/ru/plural.go`, `internal/ru/plural_test.go`
- Modify: `internal/telegram/summary.go` (delete its private `plural`, call this)

**Interfaces:**
- Produces: `func ru.Plural(n int, one, few, many string) string`

- [ ] **Step 1: Write the failing test**

Create `internal/ru/plural_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package ru

import "testing"

// TestTheTeensAreAllTheManyForm, which is the rule a naive last-digit switch
// gets wrong: 11, 12 and 14 take the same form as 5, not the forms 1, 2 and 4
// take.
func TestTheTeensAreAllTheManyForm(t *testing.T) {
	for _, n := range []int{11, 12, 13, 14, 111, 112, 114, 1011} {
		if got := Plural(n, "объявление", "объявления", "объявлений"); got != "объявлений" {
			t.Errorf("Plural(%d) = %q, want the many form", n, got)
		}
	}
}

// TestOtherwiseTheLastDigitDecides.
func TestOtherwiseTheLastDigitDecides(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "объявлений"},
		{1, "объявление"}, {2, "объявления"}, {3, "объявления"}, {4, "объявления"},
		{5, "объявлений"}, {9, "объявлений"}, {10, "объявлений"},
		{21, "объявление"}, {22, "объявления"}, {25, "объявлений"},
		{101, "объявление"}, {104, "объявления"},
	}
	for _, c := range cases {
		if got := Plural(c.n, "объявление", "объявления", "объявлений"); got != c.want {
			t.Errorf("Plural(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestANegativeCountUsesItsMagnitude. «-1 объявление» is what a difference
// reads as, and «-1 объявлений» reads as a machine talking.
func TestANegativeCountUsesItsMagnitude(t *testing.T) {
	if got := Plural(-1, "объявление", "объявления", "объявлений"); got != "объявление" {
		t.Errorf("Plural(-1) = %q", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/ru/
```

Expected: FAIL to build — `undefined: Plural`.

- [ ] **Step 3: Write `internal/ru/plural.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ru is the Russian this program has to get right in more than one
// place.
//
// Small on purpose. It exists because the alternative was a second copy of the
// plural rule — one in the notifier and one in the panel — of which only one
// would have had a test, and «5 наблюдение» on a screen is the clearest
// possible sign that nobody read the output.
package ru

// Plural picks the form that agrees with n.
//
// The teens are all the many-form and the last digit decides otherwise, which
// is the standard rule and the one a naive switch on n%10 gets backwards for
// eleven through fourteen.
func Plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
```

- [ ] **Step 4: Point the notifier at it**

In `internal/telegram/summary.go`, delete the private `plural` function entirely
and change `changeCount` to:

```go
func changeCount(n int) string {
	return fmt.Sprintf("%d %s", n,
		ru.Plural(n, "изменение", "изменения", "изменений"))
}
```

Add `"github.com/BlankTrail/avito-monitor/internal/ru"` to that file's imports.

- [ ] **Step 5: Run both packages**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/ru/ ./internal/telegram/
```

Expected: PASS. `internal/telegram`'s `TestTheNounAgreesWithTheNumber` still
passes, unchanged — which is the point: the behaviour moved, it did not change.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/ru internal/telegram && git commit -m "refactor(ru): one plural rule, not one per package"
```

---

## Task 2: The panel's own settings, and the list of watches

**Files:**
- Create: `internal/store/migrations/0004_panel.sql`, `internal/store/panel.go`, `internal/store/panel_test.go`

**Interfaces:**
- Produces:

```go
// In internal/store:
type PanelSettings struct {
    PasswordHash string // "" means no password is set
    LAN          bool
    Autostart    bool
}
func (s *Store) PanelSettings(ctx context.Context) (PanelSettings, error)
func (s *Store) SetPanelSettings(ctx context.Context, p PanelSettings) error
func (s *Store) Watches(ctx context.Context) ([]Watch, error)
func (s *Store) UpdateWatch(ctx context.Context, w Watch) error
func (s *Store) DeleteWatch(ctx context.Context, id int64) error
func (s *Store) StoredRetention(ctx context.Context) (Retention, error)
func (s *Store) SetStoredRetention(ctx context.Context, r Retention) error
func (s *Store) SpaceFloorBytes(ctx context.Context) (int64, error)
func (s *Store) SetSpaceFloorBytes(ctx context.Context, bytes int64) error
func (s *Store) RequestsPerMinute(ctx context.Context) (int, error)
func (s *Store) SetRequestsPerMinute(ctx context.Context, v int) error
func (s *Store) QuietHours(ctx context.Context) (fromMin, toMin int, err error)
func (s *Store) SetQuietHours(ctx context.Context, fromMin, toMin int) error
```

- [ ] **Step 1: Write the failing test**

Create `internal/store/panel_test.go`. `openTemp(t)` is the existing helper in
`internal/store/store_test.go` — use it, not a new one.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

func addTestWatch(t *testing.T, s *Store, name string, intervalSec int) int64 {
	t.Helper()
	now := time.Now().Unix()
	id, err := s.AddWatch(context.Background(), Watch{
		Name: name, Kind: "search", URL: "https://m.avito.ru/all?q=" + name,
		Surface: avito.SurfaceMobile, MaxPages: 1, IntervalSec: intervalSec,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		NextDueAt: now, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestWatchesListsThemAllIncludingTheOnesNotDue.
//
// DueWatches answers the scheduler's question and is useless to the screen: a
// panel that showed only what is due would show an empty list for most of every
// interval, which reads as "my watches are gone".
func TestWatchesListsThemAllIncludingTheOnesNotDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	addTestWatch(t, s, "первое", 300)
	second := addTestWatch(t, s, "второе", 600)

	// Push the second far into the future, so nothing is due about it.
	if err := s.SetNextDue(ctx, second, time.Now().Unix()+100_000); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Watches returned %d, want both", len(got))
	}
	if got[0].Name != "первое" || got[1].Name != "второе" {
		t.Errorf("order = %q, %q; want by id so two loads list them the same way",
			got[0].Name, got[1].Name)
	}
}

// TestADisabledWatchIsStillListed. Turning one off is not deleting it, and a
// screen that hid it would leave no way to turn it back on.
func TestADisabledWatchIsStillListed(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "выключено", 300)

	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	w.Enabled = false
	if err := s.UpdateWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("got %d watches, first enabled=%v", len(got), got[0].Enabled)
	}
}

// TestUpdateWatchDoesNotResetWhatTheCollectorOwns.
//
// The form posts a name, a URL and an interval. seeded_at, state and
// halted_reason belong to the collector, and a save that wrote the form's zero
// values over them would un-seed a watch every time somebody renamed it — which
// would make its next pass a seeding pass and silence a day of alerts.
func TestUpdateWatchDoesNotResetWhatTheCollectorOwns(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	if err := s.MarkHalted(ctx, id, "проблема с ip", 12_345); err != nil {
		t.Fatal(err)
	}
	before, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	edit := Watch{ID: id, Name: "переименовано", Kind: before.Kind,
		URL: before.URL, Surface: before.Surface, MaxPages: 1,
		IntervalSec: 600, AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		Enabled: true}
	if err := s.UpdateWatch(ctx, edit); err != nil {
		t.Fatal(err)
	}
	after, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "переименовано" || after.IntervalSec != 600 {
		t.Errorf("the edit did not land: %+v", after)
	}
	if after.State != before.State || after.HaltedReason != before.HaltedReason {
		t.Errorf("renaming a watch cleared its halt: state=%q reason=%q",
			after.State, after.HaltedReason)
	}
}

// TestPanelSettingsRoundTrip.
func TestPanelSettingsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	got, err := s.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != (PanelSettings{}) {
		t.Errorf("a fresh database holds %+v; the panel must start closed", got)
	}

	want := PanelSettings{PasswordHash: "pbkdf2$…", LAN: true, Autostart: true}
	if err := s.SetPanelSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err = s.PanelSettings(ctx); err != nil || got != want {
		t.Errorf("read back %+v (%v), want %+v", got, err, want)
	}
}

// TestRetentionSurvivesARestart.
//
// SetRetention holds it in an atomic pointer, which is right for the process
// and wrong for the product: a person who lowered their archive to a week found
// it back at a year on the next start, with no way to tell that had happened.
func TestRetentionSurvivesARestart(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	got, err := s.StoredRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultRetention() {
		t.Errorf("a fresh database does not hold the defaults: %+v", got)
	}

	want := DefaultRetention()
	want.BodyMaxAge = 7 * 24 * time.Hour
	want.BodyMaxBytes = 256 << 20
	want.SnapshotDailyAfter = 48 * time.Hour
	if err := s.SetStoredRetention(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err = s.StoredRetention(ctx); err != nil || got != want {
		t.Errorf("read back %+v (%v), want %+v", got, err, want)
	}
}

// TestAnUnsetRetentionMeansTheDefaultsAndNeverUnbounded.
//
// A zero in one of these columns is what a hand-edited database or a restored
// backup leaves. Read as "no limit" it is an archive that grows without bound
// on exactly the install that is already broken.
func TestAnUnsetRetentionMeansTheDefaultsAndNeverUnbounded(t *testing.T) {
	s := openTemp(t)
	if _, err := s.DB().Exec(
		`UPDATE settings SET retention_body_max_bytes = 0, retention_anchor_sec = 0`); err != nil {
		t.Fatal(err)
	}
	got, err := s.StoredRetention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyMaxBytes != DefaultRetention().BodyMaxBytes {
		t.Errorf("BodyMaxBytes = %d, want the default", got.BodyMaxBytes)
	}
	if got.AnchorEvery != DefaultRetention().AnchorEvery {
		t.Errorf("AnchorEvery = %v, want the default", got.AnchorEvery)
	}
}

// TestDeletingAWatchLeavesItsHistory.
//
// passes.watch_id has no cascade for the same reason changes.pass_id has none:
// «why was I told this was gone» has to stay answerable after somebody tidied
// up the watch that said it.
func TestDeletingAWatchLeavesItsHistory(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "уйдёт", 300)

	if err := s.DeleteWatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the watch is still listed")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Watches|PanelSettings|Retention|UpdateWatch|Deleting' -v
```

Expected: FAIL to build — `undefined: Watches`, `undefined: PanelSettings`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0004_panel.sql`:

```sql
-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Retention, the free-space floor, and the autostart intention.
--
-- The spec's settings table has the panel's password and its LAN tick already,
-- because those were decisions taken before the first file. Retention was not:
-- store.SetRetention holds it in an atomic pointer, which is right for one
-- process and wrong for a product. A person who lowered their archive to a week
-- found it back at a year on the next start, and nothing told them.
--
-- Every column defaults to zero, and zero means "the built-in default" rather
-- than "no limit" — see StoredRetention. An archive with no limit is unbounded
-- on a broken install, which is exactly when it grows fastest.
--
-- Durations are seconds because that is what the rest of this schema stores and
-- what a settings form can render as a number of days without a parse.
ALTER TABLE settings ADD COLUMN retention_snapshot_daily_sec  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_snapshot_weekly_sec INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_pass_hourly_sec     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_pass_daily_sec      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_body_max_age_sec    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_body_max_bytes      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE settings ADD COLUMN retention_anchor_sec          INTEGER NOT NULL DEFAULT 0;

-- The absolute free-space floor. Zero is unset and means the built-in, never
-- "no floor" — CheckSpace also enforces two percent of the volume, and the
-- larger of the two wins.
ALTER TABLE settings ADD COLUMN space_floor_bytes INTEGER NOT NULL DEFAULT 0;

-- Whether the user asked for start-at-login. Stored as an intention and never
-- read as the truth: internal/autostart is asked whether an entry actually
-- exists, because an entry can be removed by anything, and a checkbox showing a
-- stored intention would tell somebody their monitor starts at login when it
-- does not.
ALTER TABLE settings ADD COLUMN autostart INTEGER NOT NULL DEFAULT 0;
```

- [ ] **Step 4: Write `internal/store/panel.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

// This file is what the panel reads and writes that the collector does not.

// PanelSettings is how the interface is reached.
//
// The zero value is a closed panel: no password, no LAN, no autostart. That is
// the state a fresh install is in, and it is the safe one — the panel holds a
// proxy key, a bot token and everything the monitor has collected.
type PanelSettings struct {
	// PasswordHash is empty when no password has been set. It is a hash and
	// never a password: this column is in a file people copy, back up and paste
	// into bug reports.
	PasswordHash string
	LAN          bool
	// Autostart is what the user asked for, not what is true. Whether an entry
	// exists is asked of the operating system — see internal/autostart.
	Autostart bool
}

func (s *Store) PanelSettings(ctx context.Context) (PanelSettings, error) {
	var p PanelSettings
	err := s.db.QueryRowContext(ctx,
		`SELECT panel_password_hash, panel_lan, autostart FROM settings WHERE id = 1`).
		Scan(&p.PasswordHash, &p.LAN, &p.Autostart)
	if errors.Is(err, sql.ErrNoRows) {
		return PanelSettings{}, nil
	}
	if err != nil {
		// The hash is never echoed back into this message.
		return PanelSettings{}, fmt.Errorf("store: panel settings: %w", err)
	}
	return p, nil
}

func (s *Store) SetPanelSettings(ctx context.Context, p PanelSettings) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET panel_password_hash = ?, panel_lan = ?, autostart = ?, updated_at = ?
		WHERE id = 1`,
		p.PasswordHash, p.LAN, p.Autostart, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: panel settings: %w", err)
	}
	return nil
}

// Watches is every watch, in id order.
//
// Every one, including the disabled and the halted. DueWatches answers the
// scheduler's question; a screen that showed only what is due would be empty
// for most of every interval, which reads as "my watches are gone".
func (s *Store) Watches(ctx context.Context) ([]Watch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+watchColumns+` FROM watches ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: watches: %w", err)
	}
	defer rows.Close()

	var out []Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, fmt.Errorf("store: watches: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateWatch saves the fields a person can edit, and only those.
//
// state, halted_reason, seeded_at, next_due_at, last_total_count and too_broad
// belong to the collector. A save that wrote the form's zero values over them
// would un-seed a watch every time somebody renamed it — making its next pass a
// seeding pass, and silencing a day of alerts for an edit nobody connected to
// it.
func (s *Store) UpdateWatch(ctx context.Context, w Watch) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		UPDATE watches
		SET name = ?, url = ?, surface = ?, max_pages = ?, interval_sec = ?,
		    absence_confirmations = ?, absence_min_span_sec = ?, enabled = ?,
		    updated_at = ?
		WHERE id = ?`,
		w.Name, w.URL, string(w.Surface), w.MaxPages, w.IntervalSec,
		w.AbsenceConfirmations, w.AbsenceMinSpanSec, w.Enabled,
		s.now().UTC().Unix(), w.ID)
	if err != nil {
		return fmt.Errorf("store: update watch %d: %w", w.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchWatch
	}
	return nil
}

// DeleteWatch removes the watch and leaves its history.
//
// passes.watch_id carries no cascade, for the same reason changes.pass_id
// carries none: «почему мне сказали, что это пропало» has to stay answerable
// after somebody tidied up the watch that said it.
func (s *Store) DeleteWatch(ctx context.Context, id int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM watches WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete watch %d: %w", id, err)
	}
	return nil
}

// StoredRetention reads the persisted thresholds.
//
// A zero column means the built-in default and never "no limit". That is not
// defensive tidiness: a zero is what a hand-edited database, a restored backup
// and a future importer all leave, and read as unbounded it is an archive that
// grows without bound on precisely the install that is already broken.
func (s *Store) StoredRetention(ctx context.Context) (Retention, error) {
	d := DefaultRetention()
	var snapDaily, snapWeekly, passHourly, passDaily, bodyAge, anchor int64
	var bodyBytes int64
	err := s.db.QueryRowContext(ctx, `
		SELECT retention_snapshot_daily_sec, retention_snapshot_weekly_sec,
		       retention_pass_hourly_sec, retention_pass_daily_sec,
		       retention_body_max_age_sec, retention_body_max_bytes,
		       retention_anchor_sec
		FROM settings WHERE id = 1`).
		Scan(&snapDaily, &snapWeekly, &passHourly, &passDaily, &bodyAge,
			&bodyBytes, &anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, fmt.Errorf("store: retention: %w", err)
	}

	secs := func(v int64, fallback time.Duration) time.Duration {
		if v <= 0 {
			return fallback
		}
		return time.Duration(v) * time.Second
	}
	return Retention{
		SnapshotDailyAfter:  secs(snapDaily, d.SnapshotDailyAfter),
		SnapshotWeeklyAfter: secs(snapWeekly, d.SnapshotWeeklyAfter),
		PassHourlyAfter:     secs(passHourly, d.PassHourlyAfter),
		PassDailyAfter:      secs(passDaily, d.PassDailyAfter),
		BodyMaxAge:          secs(bodyAge, d.BodyMaxAge),
		BodyMaxBytes:        orDefault(bodyBytes, d.BodyMaxBytes),
		AnchorEvery:         secs(anchor, d.AnchorEvery),
	}, nil
}

func orDefault(v, fallback int64) int64 {
	if v <= 0 {
		return fallback
	}
	return v
}

func (s *Store) SetStoredRetention(ctx context.Context, r Retention) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	sec := func(d time.Duration) int64 { return int64(d / time.Second) }
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings
		SET retention_snapshot_daily_sec = ?, retention_snapshot_weekly_sec = ?,
		    retention_pass_hourly_sec = ?, retention_pass_daily_sec = ?,
		    retention_body_max_age_sec = ?, retention_body_max_bytes = ?,
		    retention_anchor_sec = ?, updated_at = ?
		WHERE id = 1`,
		sec(r.SnapshotDailyAfter), sec(r.SnapshotWeeklyAfter),
		sec(r.PassHourlyAfter), sec(r.PassDailyAfter),
		sec(r.BodyMaxAge), r.BodyMaxBytes, sec(r.AnchorEvery),
		s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: retention: %w", err)
	}
	return nil
}

// SpaceFloorBytes is the persisted absolute floor. Zero means the built-in.
func (s *Store) SpaceFloorBytes(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx,
		`SELECT space_floor_bytes FROM settings WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: space floor: %w", err)
	}
	return v, nil
}

func (s *Store) SetSpaceFloorBytes(ctx context.Context, bytes int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET space_floor_bytes = ?, updated_at = ? WHERE id = 1`,
		bytes, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: space floor: %w", err)
	}
	return nil
}

// RequestsPerMinute is the user's own ceiling per egress address.
//
// It is the tighter half of a pair, and the schema's comment says which: the
// ten-minute window of sixty divided out, not the three-and-a-half-second
// per-identity floor. The effective ceiling is the lower of this figure and the
// transport's own.
func (s *Store) RequestsPerMinute(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx,
		`SELECT requests_per_minute FROM settings WHERE id = 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: requests per minute: %w", err)
	}
	return v, nil
}

func (s *Store) SetRequestsPerMinute(ctx context.Context, v int) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET requests_per_minute = ?, updated_at = ? WHERE id = 1`,
		v, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: requests per minute: %w", err)
	}
	return nil
}

// QuietHours is the do-not-disturb window in minutes since local midnight.
//
// -1 in either column means there is no window, which is the schema's default
// and what an empty box on the settings screen means. Not zero: zero is
// midnight, and a window from midnight to midnight is a real thing somebody
// might ask for.
func (s *Store) QuietHours(ctx context.Context) (fromMin, toMin int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT quiet_from_min, quiet_to_min FROM settings WHERE id = 1`).
		Scan(&fromMin, &toMin)
	if errors.Is(err, sql.ErrNoRows) {
		return -1, -1, nil
	}
	if err != nil {
		return -1, -1, fmt.Errorf("store: quiet hours: %w", err)
	}
	return fromMin, toMin, nil
}

func (s *Store) SetQuietHours(ctx context.Context, fromMin, toMin int) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE settings SET quiet_from_min = ?, quiet_to_min = ?, updated_at = ?
		WHERE id = 1`, fromMin, toMin, s.now().UTC().Unix()); err != nil {
		return fmt.Errorf("store: quiet hours: %w", err)
	}
	return nil
}
```

`ErrNoSuchWatch` is already declared in `internal/store/watches.go:14` — use it,
do not declare a second one. The `avito` import above is needed only by the test
file's `addTestWatch` (`avito.SurfaceMobile`), not by `panel.go`; drop it from
`panel.go`'s import block if `go build` says it is unused.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS, the existing ones included — the migration adds
columns and changes no reader.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store && git commit -m "feat(store): the panel's settings, and retention that survives a restart"
```

---

## Task 3: Channels and proxies in the store

**Files:**
- Create: `internal/store/channels.go`, `internal/store/channels_test.go`

The `channels` and `proxies` tables exist in migration 0001, lifted verbatim
from the donor's `0005_ops.sql`, and nothing has ever written to them. This task
gives them a reader and a writer. It does **not** wire them into the transport —
that is Task 8's screen and the `avitomon panel` command in Task 10.

**Interfaces:**
- Produces:

```go
// In internal/store:
type Channel struct {
    ID                   int64
    Name                 string
    Kind                 string // "proxy-list" | "rotating" | "gateway" | "direct"
    Source               string
    RotateURL            string
    RotateMinIntervalSec int
    Weight               int
    Enabled              bool
}
type Proxy struct {
    ID        int64
    ChannelID int64
    Scheme    string
    Host      string
    Port      int
    Username  string
    Password  string
    Enabled   bool
}
func (s *Store) Channels(ctx context.Context) ([]Channel, error)
func (s *Store) SaveChannel(ctx context.Context, c Channel) (int64, error)
func (s *Store) DeleteChannel(ctx context.Context, id int64) error
func (s *Store) ReplaceProxies(ctx context.Context, channelID int64, ps []Proxy) error
func (s *Store) Proxies(ctx context.Context, channelID int64) ([]Proxy, error)
func (s *Store) CountProxies(ctx context.Context) (map[int64]int, error)
```

- [ ] **Step 1: Write the failing test**

Create `internal/store/channels_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

func aList(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id, err := s.SaveChannel(context.Background(), Channel{
		Name: name, Kind: "proxy-list", Source: "proxies.txt",
		Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestReplacingAListIsAtomicInBothDirections.
//
// A refreshed list is the whole list, not a merge: an address the provider
// removed must stop being used, and the only way to say that is to replace the
// set. Doing it in one transaction is what keeps a failed refresh from leaving
// the channel with no addresses at all — which reads to the pool as "this
// channel is empty" and silently drops it out of the mix.
func TestReplacingAListIsAtomicInBothDirections(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "провайдер")

	first := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stored %d, want 2", len(got))
	}

	// The provider dropped .1 and added .3.
	second := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.3", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, second); err != nil {
		t.Fatal(err)
	}
	got, err = s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("after the refresh: %d, want 2", len(got))
	}
	for _, p := range got {
		if p.Host == "10.0.0.1" {
			t.Error("an address the provider removed is still stored")
		}
	}
}

// TestARefreshThatFailsHalfwayChangesNothing.
//
// The second entry has a scheme the CHECK constraint refuses. Without one
// transaction the first would land, the second would fail, and the channel
// would be left holding one address out of two — a quiet halving of the budget.
func TestARefreshThatFailsHalfwayChangesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "провайдер")

	good := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, good); err != nil {
		t.Fatal(err)
	}

	bad := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.9", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "carrier-pigeon", Host: "10.0.0.8", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, bad); err == nil {
		t.Fatal("a scheme the schema refuses was accepted")
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != "10.0.0.1" {
		t.Errorf("a failed refresh changed the stored list: %+v", got)
	}
}

// TestDeletingAChannelTakesItsAddressesWithIt, which the ON DELETE CASCADE in
// migration 0001 is there for; this asserts it is actually in force, because
// SQLite enforces foreign keys only when the pragma is on.
func TestDeletingAChannelTakesItsAddressesWithIt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "уйдёт")
	if err := s.ReplaceProxies(ctx, ch, []Proxy{
		{ChannelID: ch, Scheme: "http", Host: "10.0.0.1", Port: 8080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM proxies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d orphaned addresses; the cascade is not in force", n)
	}
}

// TestTheSameAddressTwiceInOneListIsStoredOnce.
//
// A provider's file repeats. The UNIQUE constraint is what makes the address
// count on the screen mean "how many addresses", which is the number the budget
// arithmetic multiplies — a duplicate counted twice doubles a person's stated
// ceiling and earns them a ban.
func TestTheSameAddressTwiceInOneListIsStoredOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "с повторами")

	dup := Proxy{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true}
	if err := s.ReplaceProxies(ctx, ch, []Proxy{dup, dup}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("stored %d copies of one address", len(got))
	}
}

// TestCountProxiesAnswersForEveryChannelAtOnce. The screen asks it of every row
// it draws, and one query per row is one query per row.
func TestCountProxiesAnswersForEveryChannelAtOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	a, b := aList(t, s, "первый"), aList(t, s, "второй")
	if err := s.ReplaceProxies(ctx, a, []Proxy{
		{ChannelID: a, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
		{ChannelID: a, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.CountProxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[a] != 2 {
		t.Errorf("channel %d counted %d, want 2", a, counts[a])
	}
	if _, ok := counts[b]; ok && counts[b] != 0 {
		t.Errorf("channel %d counted %d, want nothing", b, counts[b])
	}
}

// TestSaveChannelUpdatesRatherThanDuplicating, so editing a channel's weight
// does not orphan the addresses hanging off its id.
func TestSaveChannelUpdatesRatherThanDuplicating(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := aList(t, s, "провайдер")
	if err := s.ReplaceProxies(ctx, id, []Proxy{
		{ChannelID: id, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	again, err := s.SaveChannel(ctx, Channel{
		ID: id, Name: "переименован", Kind: "proxy-list", Source: "proxies.txt",
		Weight: 50, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("the edit took a new id %d, orphaning %d's addresses", again, id)
	}
	chs, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 1 || chs[0].Name != "переименован" || chs[0].Weight != 50 {
		t.Errorf("channels = %+v", chs)
	}
	ps, err := s.Proxies(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Errorf("the edit lost %d addresses", 1-len(ps))
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Channel|Proxies|Replacing|Refresh' -v
```

Expected: FAIL to build — `undefined: Channel`.

- [ ] **Step 3: Write `internal/store/channels.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// This file is the egress configuration: where requests go out from.
//
// The tables are migration 0001's, lifted from the donor verbatim, and until
// now nothing wrote to them. Both transport modes have an egress, which is why
// the BlankTrail-versus-direct decision is a setting and not a channel kind.

// Channel is one way out to the network.
type Channel struct {
	ID   int64
	Name string
	// Kind is one of proxy-list, rotating, gateway, direct. The schema has the
	// CHECK; this type does not repeat it, because two places stating the
	// vocabulary is one place for them to disagree.
	Kind                 string
	Source               string
	RotateURL            string
	RotateMinIntervalSec int
	// Weight is the share of ports this channel gets, before the Mixer starts
	// demoting it for producing blocks. 100 is the default and means "an equal
	// share"; the number is relative to the other channels and not a percentage.
	Weight  int
	Enabled bool
}

// Proxy is one parsed entry of a list.
type Proxy struct {
	ID        int64
	ChannelID int64
	Scheme    string
	Host      string
	Port      int
	Username  string
	// Password is a credential and is never put into an error, a log or a
	// rendered page. The screen shows a host and a port.
	Password string
	Enabled  bool
}

const channelColumns = `id, name, kind, source, rotate_url,
	rotate_min_interval_sec, weight, enabled`

// Channels is every configured way out, in id order.
func (s *Store) Channels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+channelColumns+` FROM channels ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Source, &c.RotateURL,
			&c.RotateMinIntervalSec, &c.Weight, &c.Enabled); err != nil {
			return nil, fmt.Errorf("store: channels: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveChannel inserts or updates, keyed on the id.
//
// An update rather than a delete and an insert, because proxies.channel_id
// cascades: replacing the row would take every address with it, and a person
// changing a channel's weight would find their list empty.
func (s *Store) SaveChannel(ctx context.Context, c Channel) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now().UTC().Unix()
	if c.Weight <= 0 {
		c.Weight = 100
	}

	if c.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO channels
			  (name, kind, source, rotate_url, rotate_min_interval_sec,
			   weight, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Name, c.Kind, c.Source, c.RotateURL, c.RotateMinIntervalSec,
			c.Weight, c.Enabled, now, now)
		if err != nil {
			return 0, fmt.Errorf("store: save channel: %w", err)
		}
		return res.LastInsertId()
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE channels
		SET name = ?, kind = ?, source = ?, rotate_url = ?,
		    rotate_min_interval_sec = ?, weight = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		c.Name, c.Kind, c.Source, c.RotateURL, c.RotateMinIntervalSec,
		c.Weight, c.Enabled, now, c.ID); err != nil {
		return 0, fmt.Errorf("store: save channel %d: %w", c.ID, err)
	}
	return c.ID, nil
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete channel %d: %w", id, err)
	}
	return nil
}

// ReplaceProxies swaps a channel's whole address list, in one transaction.
//
// The whole list and not a merge: an address the provider removed must stop
// being used, and only replacing the set says that. One transaction because a
// refresh that failed halfway would leave the channel holding some of its
// addresses — a quiet halving of the budget that nothing would report.
//
// INSERT OR IGNORE on the UNIQUE constraint, because a provider's file repeats
// itself and the address count is what the budget arithmetic multiplies. A
// duplicate counted twice doubles a person's stated ceiling and earns them a
// ban.
func (s *Store) ReplaceProxies(ctx context.Context, channelID int64, ps []Proxy) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM proxies WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	for _, p := range ps {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO proxies
			  (channel_id, scheme, host, port, username, password, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			channelID, p.Scheme, p.Host, p.Port, p.Username, p.Password,
			p.Enabled); err != nil {
			// The address is named and the credential is not.
			return fmt.Errorf("store: replace proxies: %s:%d: %w", p.Host, p.Port, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: replace proxies: %w", err)
	}
	return nil
}

const proxyColumns = `id, channel_id, scheme, host, port, username, password, enabled`

func (s *Store) Proxies(ctx context.Context, channelID int64) ([]Proxy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+proxyColumns+` FROM proxies WHERE channel_id = ? ORDER BY id`,
		channelID)
	if err != nil {
		return nil, fmt.Errorf("store: proxies: %w", err)
	}
	defer rows.Close()

	var out []Proxy
	for rows.Next() {
		var p Proxy
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Scheme, &p.Host, &p.Port,
			&p.Username, &p.Password, &p.Enabled); err != nil {
			return nil, fmt.Errorf("store: proxies: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountProxies is how many addresses each channel holds.
//
// All of them in one query, because the screen asks it of every row it draws
// and the budget arithmetic asks it of every enabled channel at once.
func (s *Store) CountProxies(ctx context.Context) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, count(*) FROM proxies WHERE enabled = 1 GROUP BY channel_id`)
	if err != nil {
		return nil, fmt.Errorf("store: count proxies: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: count proxies: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Check the foreign-key pragma is actually on**

`TestDeletingAChannelTakesItsAddressesWithIt` fails unless `foreign_keys` is
enabled. This store sets its pragmas in the DSN — see the comment at the top of
`internal/store/store.go`. Confirm `_pragma=foreign_keys(1)` is in it:

```bash
cd S:/Projects/Avito_Go && grep -n "foreign_keys" internal/store/store.go
```

It is there — `internal/store/store.go:75` has `_pragma=foreign_keys(ON)` with a
comment saying SQLite defaults it off. The step exists so the test's failure has
an obvious first suspect: a cascade the schema declares and the connection does
not enforce is a constraint that exists only on paper.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store && git commit -m "feat(store): channels and proxies, with a refresh that is all or nothing"
```

---

## Task 4: The password, hashed

**Files:**
- Create: `internal/web/password.go`, `internal/web/password_test.go`

Split from the server shell on purpose: this is the one piece of Task 5 whose
failure mode is silent. A password comparison that is subtly wrong still lets
the right password in, so the tests that matter are the ones about the wrong
one.

The donor stores the panel password in plaintext and compares it with
`subtle.ConstantTimeCompare`. This schema has `panel_password_hash`, which is
better and is the one edit: the column lives in a file people copy to another
machine, back up to a cloud drive and paste into bug reports.

**Interfaces:**
- Produces: `func HashPassword(plain string) (string, error)`, `func VerifyPassword(hash, plain string) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/web/password_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strings"
	"testing"
)

// TestTheStoredFormIsNotThePassword.
//
// panel_password_hash lives in a file people copy to another machine, back up
// to a cloud drive and paste into bug reports. A column holding the password
// itself makes every one of those a disclosure.
func TestTheStoredFormIsNotThePassword(t *testing.T) {
	const plain = "правильный конь батарейка скрепка"
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, plain) || strings.Contains(hash, "батарейка") {
		t.Fatalf("the password is in its own hash: %q", hash)
	}
	if !VerifyPassword(hash, plain) {
		t.Error("the right password was refused")
	}
}

// TestTheWrongPasswordIsRefused, including the near misses a comparison bug
// lets through.
func TestTheWrongPasswordIsRefused(t *testing.T) {
	hash, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{
		"", "секре", "секрет ", " секрет", "секретъ", "СЕКРЕТ", "другое",
	} {
		if VerifyPassword(hash, wrong) {
			t.Errorf("%q was accepted", wrong)
		}
	}
}

// TestTwoHashesOfOnePasswordDiffer.
//
// A salt per password. Without one, two installations with the same password
// have the same column, and one leaked database tells an attacker which other
// ones to try it on.
func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	a, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("секрет")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of one password are identical; there is no salt")
	}
	if !VerifyPassword(a, "секрет") || !VerifyPassword(b, "секрет") {
		t.Error("a salted hash does not verify")
	}
}

// TestAnUnreadableHashRefusesRatherThanAccepts.
//
// A hand-edited database, a truncated restore, a column written by a newer
// release. Every one of these must refuse: a verifier that returned true on a
// hash it could not parse would turn a corrupted column into an open door.
func TestAnUnreadableHashRefusesRatherThanAccepts(t *testing.T) {
	for _, junk := range []string{
		"", "секрет", "pbkdf2", "pbkdf2$", "pbkdf2$1$", "pbkdf2$notanumber$aa$bb",
		"pbkdf2$100000$нехекс$bb", "scrypt$1$aa$bb",
	} {
		if VerifyPassword(junk, "секрет") {
			t.Errorf("the hash %q accepted a password", junk)
		}
		if VerifyPassword(junk, "") {
			t.Errorf("the hash %q accepted an empty password", junk)
		}
	}
}

// TestNoPasswordIsNotAPassword. An empty hash is what a fresh install has, and
// it must not be satisfiable by anything — the caller decides whether to ask at
// all, and a verifier that said yes to "" would make that decision for it.
func TestNoPasswordIsNotAPassword(t *testing.T) {
	if VerifyPassword("", "") {
		t.Error("an empty hash accepted an empty password")
	}
}

// TestHashingRefusesAnEmptyPassword. «Требовать пароль» with nothing typed is a
// form mistake, and storing the hash of "" would make it look configured.
func TestHashingRefusesAnEmptyPassword(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("an empty password was hashed")
	}
	if _, err := HashPassword("   "); err == nil {
		t.Error("a password of spaces was hashed")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/
```

Expected: FAIL to build — `undefined: HashPassword`.

- [ ] **Step 3: Write `internal/web/password.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The panel password is stored as a hash and never as itself.
//
// The donor compares a plaintext column with subtle.ConstantTimeCompare, which
// is the right comparison over the wrong thing: settings.panel_password_hash
// lives in a file people copy to another machine, back up to a cloud drive and
// paste into bug reports, and a column holding the password makes every one of
// those a disclosure.
//
// crypto/pbkdf2 is standard library as of Go 1.24, so this costs no dependency.
// PBKDF2-HMAC-SHA256 rather than argon2 or bcrypt for exactly that reason — the
// budget is eight direct dependencies and a ninth for a login form on the
// loopback interface would not be a good trade.

// pbkdf2Iterations is the work factor.
//
// 600 000 is what OWASP states for PBKDF2-HMAC-SHA256. It costs a few hundred
// milliseconds once per login, on a panel with one user, which is nothing; and
// it is what turns a leaked database column from a lookup into a campaign.
const pbkdf2Iterations = 600_000

const (
	saltBytes = 16
	keyBytes  = 32
)

// ErrEmptyPassword is returned when there is nothing to hash.
var ErrEmptyPassword = errors.New("web: пароль не может быть пустым")

// HashPassword returns the string that goes in the column.
//
// The format is pbkdf2$<iterations>$<salt hex>$<key hex>. The iteration count
// travels with the hash so it can be raised later without locking anybody out:
// an old hash verifies against its own number, and is rewritten at the next
// change of password rather than at the next login, because rewriting on login
// would need this function to reach the store.
func HashPassword(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", ErrEmptyPassword
	}
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("web: генерация соли: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, plain, salt, pbkdf2Iterations, keyBytes)
	if err != nil {
		return "", fmt.Errorf("web: хеширование пароля: %w", err)
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s",
		pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// VerifyPassword reports whether plain is the password behind hash.
//
// False for anything it cannot read, and that is the whole of its error
// handling. A verifier that returned true — or that distinguished "malformed"
// from "wrong" to its caller — would turn a hand-edited column, a truncated
// restore or a hash written by a newer release into an open door.
func VerifyPassword(hash, plain string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, plain, salt, iter, len(want))
	if err != nil {
		return false
	}
	// Constant time: a comparison that returned early would leak the key one
	// byte at a time to anything that can measure a response, which on a
	// machine somebody shares is not hypothetical.
	return subtle.ConstantTimeCompare(got, want) == 1
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS. The suite takes a second or two — six hundred
thousand iterations, several times over, is the point.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): the panel password, hashed rather than stored"
```

---

## Task 5: The server shell — routing, the gate, and the refusal to leave the machine

**Files:**
- Create: `internal/web/server.go`, `internal/web/server_test.go`, `internal/web/html.go`, `internal/web/templates/layout.html`, `internal/web/static/monitor.css`, `internal/web/static/app.js`

**Interfaces:**
- Consumes: `HashPassword`, `VerifyPassword` from Task 4; `store.PanelSettings` from Task 2.
- Produces:

```go
// In internal/web:
const SourceURL = "https://github.com/BlankTrail/avito-monitor"
var ErrWouldExposeUnprotectedPanel = errors.New(...)

type Tab struct{ Label, Href string; Active bool }

type Server struct {
    Store *store.Store
    Now   func() time.Time

    // Live third parties, as fields, so the suite needs none of them.
    CheckBlankTrail func(ctx context.Context, url, apiKey string) error
    CheckTelegram   func(ctx context.Context, token string) (username string, err error)
    Gateways        func(ctx context.Context) (blanktrail.GatewayList, error)
    CheckChannel    func(ctx context.Context, id int64) (summary string, err error)
    Autostart       Autostart
}
type Autostart interface {
    Enabled() (bool, error)
    Enable() error
    Disable() error
}
func (s *Server) Handler() http.Handler
func (s *Server) ListenAddress(ctx context.Context, port int, lan bool) (string, error)
func (s *Server) render(w http.ResponseWriter, r *http.Request, p page)
```

- [ ] **Step 1: Write the failing test**

Create `internal/web/server_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		Store: openStore(t),
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func setPassword(t *testing.T, s *Server, plain string) {
	t.Helper()
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetPanelSettings(context.Background(),
		store.PanelSettings{PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
}

// TestWithNoPasswordThePanelOpens.
//
// The default, and it is a decision rather than an oversight: the server listens
// on the loopback interface, so what a password keeps out is another program or
// another account on this same machine. Worth having on a shared machine, worth
// nothing on a personal one, and not something to decide on somebody's behalf.
func TestWithNoPasswordThePanelOpens(t *testing.T) {
	if rec := get(t, newServer(t), "/"); rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200", rec.Code)
	}
}

// TestWithAPasswordEveryPageIsBehindIt.
func TestWithAPasswordEveryPageIsBehindIt(t *testing.T) {
	s := newServer(t)
	setPassword(t, s, "секрет")

	rec := get(t, s, "/")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET / = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("no Basic challenge: %q", rec.Header().Get("WWW-Authenticate"))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("", "секрет")
	ok := httptest.NewRecorder()
	s.Handler().ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Errorf("with the password: %d, want 200", ok.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("", "не секрет")
	bad := httptest.NewRecorder()
	s.Handler().ServeHTTP(bad, req)
	if bad.Code != http.StatusUnauthorized {
		t.Errorf("with the wrong password: %d, want 401", bad.Code)
	}
}

// TestStaticAssetsAreNotBehindThePassword.
//
// They are the same bytes for everybody and carry nothing about this
// installation. Behind the gate, the only thing an unauthenticated visitor gets
// is an unstyled prompt.
func TestStaticAssetsAreNotBehindThePassword(t *testing.T) {
	s := newServer(t)
	setPassword(t, s, "секрет")
	if rec := get(t, s, "/static/monitor.css"); rec.Code != http.StatusOK {
		t.Errorf("GET /static/monitor.css = %d, want 200", rec.Code)
	}
}

// TestThePanelWillNotLeaveTheMachineWithoutAPasswordOfYourOwn.
//
// The refusal lives here rather than in the settings screen because a setting
// can be written by anything — a hand-edited database, a restored backup, a
// future importer — and the only place that reliably sees the combination is
// the one about to open the port. The panel holds a proxy key, a bot token and
// everything the monitor has collected.
func TestThePanelWillNotLeaveTheMachineWithoutAPasswordOfYourOwn(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()

	if _, err := s.ListenAddress(ctx, 8080, true); !errors.Is(err, ErrWouldExposeUnprotectedPanel) {
		t.Fatalf("err = %v, want the refusal", err)
	}
	// Loopback with no password is fine, and is the default.
	addr, err := s.ListenAddress(ctx, 8080, false)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q, want the loopback", addr)
	}

	setPassword(t, s, "секрет")
	addr, err = s.ListenAddress(ctx, 8080, true)
	if err != nil {
		t.Fatal(err)
	}
	if addr != ":8080" {
		t.Errorf("addr = %q, want every interface", addr)
	}
}

// TestTheSourceLinkIsOnEveryPage.
//
// The AGPL obliges whoever serves this to offer the source of what they are
// serving. A constant and not a setting: a configurable link is a link that can
// be pointed somewhere else.
func TestTheSourceLinkIsOnEveryPage(t *testing.T) {
	rec := get(t, newServer(t), "/")
	if !strings.Contains(rec.Body.String(), SourceURL) {
		t.Error("the page does not carry the source link")
	}
}

// TestEveryTabIsReachable. A tab in the header that answers 404 is a tab a
// person clicks once and stops trusting.
func TestEveryTabIsReachable(t *testing.T) {
	s := newServer(t)
	for _, tab := range s.tabs("/") {
		rec := get(t, s, tab.Href)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s (%s) = %d, want 200", tab.Href, tab.Label, rec.Code)
		}
	}
}

// TestExactlyOneTabIsActive, so the header says where you are.
func TestExactlyOneTabIsActive(t *testing.T) {
	s := newServer(t)
	for _, probe := range s.tabs("/") {
		active := 0
		for _, tab := range s.tabs(probe.Href) {
			if tab.Active {
				active++
			}
		}
		if active != 1 {
			t.Errorf("on %s, %d tabs are active", probe.Href, active)
		}
	}
}

// TestAValueFromTheDatabaseIsEscaped.
//
// Every handler in this package assembles its own HTML. A watch named with a
// script tag is what a person pastes in from somewhere else, and the panel
// renders it back to them on the machine that holds their proxy key.
func TestAValueFromTheDatabaseIsEscaped(t *testing.T) {
	const nasty = `<script>alert(1)</script>`
	if got := esc(nasty); strings.Contains(got, "<script>") {
		t.Errorf("esc(%q) = %q", nasty, got)
	}
	if got := attr(`" onload="alert(1)`); strings.Contains(got, `onload=`) &&
		!strings.Contains(got, "&#34;") {
		t.Errorf("attr left a quote unescaped: %q", got)
	}
}

// TestAPageRendersAsHTML, with the charset — a Russian panel served without one
// is a page of question marks in whatever browser guesses wrong.
func TestAPageRendersAsHTML(t *testing.T) {
	rec := get(t, newServer(t), "/")
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") || !strings.Contains(ct, "utf-8") {
		t.Errorf("Content-Type = %q", ct)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/
```

Expected: FAIL to build — `undefined: Server`.

- [ ] **Step 3: Write `internal/web/html.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// This file is the escaping discipline, named in one place.
//
// Every handler in this package assembles its own HTML rather than rendering a
// template per screen — the donor's reasoning, and it holds: a screen is a
// function, and a function is easier to read than a template plus the struct it
// is given. The cost is that escaping is manual, so it gets three named helpers
// and a rule: no user value reaches a page except through one of them.

// esc is a value inside an element.
func esc(s string) string { return html.EscapeString(s) }

// attr is a value inside a double-quoted attribute.
//
// Separate from esc so the call site says which context it is in. They do the
// same thing today — html.EscapeString escapes the quote as well — and a future
// reader changing one must be able to see that the other exists.
func attr(s string) string { return html.EscapeString(s) }

// rawHTML marks a string as already-escaped markup.
//
// This is where the discipline is named, so that a future caller passing a raw
// user string through it has one obvious line to have gone past.
func rawHTML(s string) template.HTML { return template.HTML(s) }

// field renders one labelled input.
func field(label, name, value, kind, hint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<label class="field"><span class="label">%s</span>`, esc(label))
	fmt.Fprintf(&b, `<input class="input" type="%s" name="%s" value="%s">`,
		attr(kind), attr(name), attr(value))
	if hint != "" {
		fmt.Fprintf(&b, `<span class="hint">%s</span>`, esc(hint))
	}
	b.WriteString(`</label>`)
	return b.String()
}

// checkbox renders one labelled tick.
//
// The hidden companion is not decoration: an unticked checkbox posts nothing at
// all, so a form read with r.FormValue cannot tell "unticked" from "this build
// does not have that field" — and the second must never turn a setting off.
func checkbox(label, name string, on bool, hint string) string {
	checked := ""
	if on {
		checked = " checked"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<input type="hidden" name="%s" value="0">`, attr(name))
	fmt.Fprintf(&b, `<label class="check"><input type="checkbox" name="%s" value="1"%s>`,
		attr(name), checked)
	fmt.Fprintf(&b, `<span>%s</span>`, esc(label))
	if hint != "" {
		fmt.Fprintf(&b, `<span class="hint">%s</span>`, esc(hint))
	}
	b.WriteString(`</label>`)
	return b.String()
}

// note renders one line of explanation, in a tone.
//
// tone is "neutral", "warning" or "bad". Three and not more: the panel says
// «всё в порядке», «обратите внимание» or «сломано», and a fourth shade would be
// a distinction the reader has to learn.
func note(tone, text string) string {
	return fmt.Sprintf(`<p class="note %s">%s</p>`, attr(tone), esc(text))
}
```

- [ ] **Step 4: Write `internal/web/server.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web is the product's face: a small server that renders what the store
// holds and lets a person configure, start and watch a collection.
//
// Go templates and a page of vanilla JavaScript, with every asset in the binary.
// Spec §7 names htmx; this package does the eighty lines it would have provided
// by hand instead, for the reason static/app.js states at the top of itself —
// fifty kilobytes of third-party code vendored into a public AGPL repository, to
// replace something a reader can audit in a minute.
//
// Access is closed by default and stays on this machine until the user says
// otherwise. A monitor that shipped listening on every interface with no
// password would be a monitor whose owner's proxy credentials are one port scan
// away.
//
// Everything that needs a live third party — BlankTrail's control API, Telegram,
// the operating system's autostart entry — is a function field on Server rather
// than a call into the package that does it. The suite needs no network and no
// registry, and the wiring that supplies them lives in cmd/avitomon.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// SourceURL is where the AGPL requires this page to point.
//
// A constant rather than a setting: the licence obliges whoever serves this to
// offer the source of what they are serving, and a configurable link is a link
// that can be pointed somewhere else.
const SourceURL = "https://github.com/BlankTrail/avito-monitor"

// ErrWouldExposeUnprotectedPanel is returned when a panel anyone could walk into
// is asked to listen beyond the loopback interface.
var ErrWouldExposeUnprotectedPanel = errors.New(
	"web: панель без пароля не открывается за пределы этой машины: " +
		"задайте пароль в настройках")

// Autostart is the operating system's own start-at-login mechanism, or nil in a
// build that has none.
//
// Enabled is asked rather than remembered: an entry can be removed by anything,
// and a checkbox showing a stored intention would tell the user their monitor
// starts at login when it does not.
type Autostart interface {
	Enabled() (bool, error)
	Enable() error
	Disable() error
}

// Tab is one entry in the header navigation.
type Tab struct {
	Label  string
	Href   string
	Active bool
}

// Server serves the interface.
type Server struct {
	Store *store.Store
	Now   func() time.Time

	// CheckBlankTrail reports whether the configured control API answers. A
	// field rather than a call into the SDK so that tests do not need a live
	// one — the preflight is the SDK's and re-implementing it here would be a
	// second opinion nobody asked for.
	CheckBlankTrail func(ctx context.Context, url, apiKey string) error

	// CheckTelegram asks Telegram who this bot is, over whatever ladder the
	// wiring built.
	CheckTelegram func(ctx context.Context, token string) (username string, err error)

	// Gateways lists what the licensed proxy has configured, so a gateway is
	// picked from what exists rather than typed from memory.
	Gateways func(ctx context.Context) (blanktrail.GatewayList, error)

	// CheckChannel reports what one egress channel holds, or why it cannot be
	// used. Reading a proxy list is the engine's business; this package's tests
	// must not need a licensed service.
	CheckChannel func(ctx context.Context, id int64) (summary string, err error)

	Autostart Autostart

	tmpl     *template.Template
	tmplOnce sync.Once
	tmplErr  error
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static assets are served without authentication: they are the same bytes
	// for everybody and carry nothing about this installation. Behind the
	// password, the only thing an unauthenticated visitor gets is an unstyled
	// prompt.
	mux.Handle("GET /static/", http.FileServerFS(staticFS))

	mux.Handle("GET /", s.auth(http.HandlerFunc(s.watchesPage)))
	mux.Handle("POST /watches", s.auth(http.HandlerFunc(s.saveWatch)))
	mux.Handle("POST /watches/delete", s.auth(http.HandlerFunc(s.deleteWatch)))
	mux.Handle("POST /watches/toggle", s.auth(http.HandlerFunc(s.toggleWatch)))

	mux.Handle("GET /first-run", s.auth(http.HandlerFunc(s.firstRunPage)))
	mux.Handle("POST /first-run", s.auth(http.HandlerFunc(s.saveFirstRun)))

	mux.Handle("GET /settings", s.auth(http.HandlerFunc(s.settingsPage)))
	mux.Handle("POST /settings", s.auth(http.HandlerFunc(s.saveSettings)))
	mux.Handle("GET /settings/blanktrail", s.auth(http.HandlerFunc(s.checkBlankTrail)))
	mux.Handle("GET /settings/telegram", s.auth(http.HandlerFunc(s.checkTelegram)))

	mux.Handle("GET /channels", s.auth(http.HandlerFunc(s.channelsPage)))
	mux.Handle("POST /channels", s.auth(http.HandlerFunc(s.saveChannel)))
	mux.Handle("POST /channels/delete", s.auth(http.HandlerFunc(s.deleteChannel)))
	mux.Handle("GET /channels/test", s.auth(http.HandlerFunc(s.testChannel)))
	mux.Handle("GET /channels/gateways", s.auth(http.HandlerFunc(s.listGateways)))

	return mux
}

// auth is the password gate, when there is one.
//
// Off by default. The server listens on the loopback interface, so what a
// password keeps out here is another program or another account on this same
// machine — worth having on a shared machine, worth nothing on a personal one,
// and not something to decide on somebody's behalf.
//
// Basic authentication rather than a login form and a session cookie, which
// would buy nothing: there is one user, and a cookie would add a store, an
// expiry and a logout button to arrive at the same place.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read per request rather than at start, so that setting a password
		// takes effect on the next page instead of the next restart — a setting
		// that needs a restart is a setting people believe they changed.
		p, err := s.Store.PanelSettings(r.Context())
		if err != nil {
			http.Error(w, "settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if p.PasswordHash == "" {
			next.ServeHTTP(w, r)
			return
		}
		_, given, ok := r.BasicAuth()
		if !ok || !VerifyPassword(p.PasswordHash, given) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Avito Monitor"`)
			http.Error(w, "требуется пароль", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListenAddress decides where to listen.
//
// lan comes from the setting the user ticked. The refusal is here rather than in
// the settings screen because a setting can be written by anything — a
// hand-edited database, a restored backup, a future importer — and the only
// place that reliably sees the combination is the one about to open the port.
func (s *Server) ListenAddress(ctx context.Context, port int, lan bool) (string, error) {
	if !lan {
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
	p, err := s.Store.PanelSettings(ctx)
	if err != nil {
		return "", err
	}
	if p.PasswordHash == "" {
		return "", ErrWouldExposeUnprotectedPanel
	}
	return fmt.Sprintf(":%d", port), nil
}

// tabs is the header navigation.
//
// Four in 6A. Здоровье сбора and История arrive with 6B, Правила and Результаты
// with 6C; a tab that answers 404 is a tab a person clicks once and stops
// trusting, so they are added when their screens are.
func (s *Server) tabs(path string) []Tab {
	out := []Tab{
		{Label: "Наблюдения", Href: "/"},
		{Label: "Прокси", Href: "/channels"},
		{Label: "Настройки", Href: "/settings"},
	}
	for i := range out {
		out[i].Active = out[i].Href == path
	}
	// The root is active for anything not otherwise claimed, so a form post
	// that redirects somewhere unexpected still highlights one tab.
	claimed := false
	for _, t := range out {
		claimed = claimed || t.Active
	}
	if !claimed {
		out[0].Active = true
	}
	return out
}

// page is what the layout template is given.
type page struct {
	Title     string
	Body      template.HTML
	Tabs      []Tab
	SourceURL string
	// Banner is a persistent warning above everything — a halted watch, a
	// transport nobody has chosen, a disk about to fill. Already rendered.
	Banner template.HTML
}

func (s *Server) templates() (*template.Template, error) {
	s.tmplOnce.Do(func() {
		s.tmpl, s.tmplErr = template.ParseFS(templateFS, "templates/*.html")
	})
	return s.tmpl, s.tmplErr
}

// render writes one full page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, p page) {
	t, err := s.templates()
	if err != nil {
		http.Error(w, "template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	p.SourceURL = SourceURL
	p.Tabs = s.tabs(r.URL.Path)
	p.Banner = rawHTML(s.banner(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", p); err != nil {
		// The status is already sent by now, so this cannot become a 500.
		// Saying so beats a silently truncated page.
		fmt.Fprintf(w, "<!-- render: %s -->", esc(err.Error()))
	}
}

// banner is what has to be said above every screen.
//
// Three things, in this order, and the order is the urgency: nothing collects
// until a transport is chosen; a halted watch has stopped and will not restart
// itself; a full disk stops collection at the next pass. Each is a state a
// person would otherwise discover as silence.
func (s *Server) banner(ctx context.Context) string {
	var b strings.Builder

	if _, err := s.Store.TransportMode(ctx); errors.Is(err, store.ErrNoTransportChosen) {
		b.WriteString(`<div class="banner warning">` +
			`Не выбран способ выхода в сеть — сбор не идёт. ` +
			`<a href="/first-run">Выбрать</a></div>`)
	}
	watches, err := s.Store.Watches(ctx)
	if err == nil {
		for _, w := range watches {
			if w.State != "halted" {
				continue
			}
			fmt.Fprintf(&b,
				`<div class="banner bad">Наблюдение «%s» остановлено: %s</div>`,
				esc(w.Name), esc(w.HaltedReason))
		}
	}
	if err := s.Store.CheckSpace(ctx); err != nil {
		fmt.Fprintf(&b, `<div class="banner bad">%s</div>`, esc(err.Error()))
	}
	return b.String()
}
```

`firstRunPage` and `saveFirstRun` arrive in Task 6; `watchesPage`, `saveWatch`,
`deleteWatch` and `toggleWatch` in Task 8 (Task 7 is the arithmetic they use);
`settingsPage`, `saveSettings`, `checkBlankTrail` and `checkTelegram` in Task 9;
`channelsPage`, `saveChannel`, `deleteChannel`, `testChannel` and `listGateways`
in Task 10. To make Task 5's tests
run, add this file with one-line stubs and delete each as its task lands:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "net/http"

// stubs.go holds the handlers whose screens are later tasks in this plan.
//
// Present so the router compiles and the shell's own tests can run. Each is
// deleted by the task that writes the real one; a stub still here when the plan
// is finished is a screen somebody skipped, which is why they say so out loud
// rather than rendering an empty page.
func (s *Server) todo(w http.ResponseWriter, r *http.Request, screen string) {
	s.render(w, r, page{
		Title: screen,
		Body:  rawHTML(note("warning", "Этот экран ещё не построен: "+screen)),
	})
}

func (s *Server) watchesPage(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Наблюдения") }
func (s *Server) saveWatch(w http.ResponseWriter, r *http.Request)   { s.todo(w, r, "Наблюдения") }
func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Наблюдения") }
func (s *Server) toggleWatch(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Наблюдения") }
func (s *Server) firstRunPage(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Первый запуск") }
func (s *Server) saveFirstRun(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Первый запуск") }
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Настройки") }
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Настройки") }
func (s *Server) checkBlankTrail(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Настройки") }
func (s *Server) checkTelegram(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Настройки") }
func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Прокси") }
func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request)  { s.todo(w, r, "Прокси") }
func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Прокси") }
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request)  { s.todo(w, r, "Прокси") }
func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Прокси") }
```

- [ ] **Step 5: Write the layout**

Create `internal/web/templates/layout.html`:

```html
{{define "layout.html"}}<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{if .Title}}{{.Title}} — {{end}}Avito Monitor</title>
<link rel="stylesheet" href="/static/monitor.css">
</head>
<body>
<header class="top">
  <a class="brand" href="/">Avito Monitor</a>
  <nav class="tabs">
    {{range .Tabs}}<a class="tab{{if .Active}} active{{end}}" href="{{.Href}}">{{.Label}}</a>{{end}}
  </nav>
</header>
{{.Banner}}
<main class="page">
  {{if .Title}}<h1>{{.Title}}</h1>{{end}}
  {{.Body}}
</main>
<footer class="foot">
  <a href="{{.SourceURL}}" rel="noreferrer">Исходный код</a> · AGPL-3.0-or-later
</footer>
<script src="/static/app.js"></script>
</body>
</html>{{end}}
```

- [ ] **Step 6: Write the stylesheet**

Create `internal/web/static/monitor.css`. Written for this markup rather than
lifted: the donor's is 4 956 lines across two files and is tied to screens this
product does not have.

```css
/* SPDX-License-Identifier: AGPL-3.0-or-later */

:root {
  --bg: #12141a; --panel: #1a1d26; --line: #2a2f3c;
  --text: #e6e8ee; --dim: #9aa1b1;
  --ok: #4ec9a0; --warn: #e2b341; --bad: #e2685f; --link: #6ea8fe;
  --radius: 8px;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #f6f7f9; --panel: #fff; --line: #dfe3ea;
    --text: #1b1e26; --dim: #5d6472;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--text);
  font: 15px/1.5 system-ui, "Segoe UI", Roboto, sans-serif;
}
a { color: var(--link); }
.top {
  display: flex; align-items: center; gap: 24px;
  padding: 12px 24px; border-bottom: 1px solid var(--line); background: var(--panel);
}
.brand { font-weight: 600; text-decoration: none; color: var(--text); }
.tabs { display: flex; gap: 4px; }
.tab {
  padding: 6px 12px; border-radius: var(--radius);
  text-decoration: none; color: var(--dim);
}
.tab:hover { background: var(--bg); }
.tab.active { background: var(--bg); color: var(--text); font-weight: 600; }
.page { max-width: 1100px; margin: 0 auto; padding: 24px; }
h1 { font-size: 22px; margin: 0 0 20px; }
h2 { font-size: 17px; margin: 28px 0 12px; }

.banner { padding: 12px 24px; border-bottom: 1px solid var(--line); }
.banner.warning { background: #3a3016; color: #f0d493; }
.banner.bad { background: #3a1e1c; color: #f0a9a3; }

.card {
  background: var(--panel); border: 1px solid var(--line);
  border-radius: var(--radius); padding: 16px; margin-bottom: 16px;
}
.row { display: flex; gap: 16px; align-items: flex-start; flex-wrap: wrap; }
.grow { flex: 1 1 240px; }

.field { display: block; margin-bottom: 14px; }
.label { display: block; font-size: 13px; color: var(--dim); margin-bottom: 4px; }
.input, select, textarea {
  width: 100%; padding: 8px 10px; border-radius: 6px;
  border: 1px solid var(--line); background: var(--bg); color: var(--text);
  font: inherit;
}
.hint { display: block; font-size: 12px; color: var(--dim); margin-top: 4px; }
.check { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.check .hint { margin: 0 0 0 4px; }

button, .button {
  padding: 8px 14px; border-radius: 6px; border: 1px solid var(--line);
  background: var(--bg); color: var(--text); font: inherit; cursor: pointer;
}
button.primary { background: var(--link); border-color: var(--link); color: #06101f; font-weight: 600; }
button.danger { color: var(--bad); }

table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--line); }
th { font-size: 12px; color: var(--dim); font-weight: 600; }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }

.note { margin: 8px 0; font-size: 13px; }
.note.neutral { color: var(--dim); }
.note.warning { color: var(--warn); }
.note.bad { color: var(--bad); }
.pill { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 12px; }
.pill.ok { background: #14332a; color: var(--ok); }
.pill.warn { background: #3a3016; color: var(--warn); }
.pill.bad { background: #3a1e1c; color: var(--bad); }

.foot { padding: 24px; color: var(--dim); font-size: 13px; text-align: center; }
.sum { font-variant-numeric: tabular-nums; }
```

- [ ] **Step 7: Write the eighty lines that replace htmx**

Create `internal/web/static/app.js`:

```javascript
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This is here instead of htmx, which spec §7 names.
//
// What this panel actually needs from it is one thing: press a button, fetch a
// fragment, put it in an element. That is the function below. htmx is about
// fifty kilobytes of third-party JavaScript, and vendoring it into a public
// AGPL repository to replace something a reader can audit in a minute is a
// trade this project does not make anywhere else either — see the chart written
// against image/png and the Bot API client written against net/http.
//
// Two attributes:
//   data-load="/url"     fetch on click, replace the target's innerHTML
//   data-into="#id"      where it goes; default is the element itself
//
// The fragment is HTML the server already escaped. Nothing here builds markup
// from a value, which is why there is no escaping in this file: everything it
// inserts came from a handler that did it.

(function () {
  "use strict";

  function target(el) {
    var sel = el.getAttribute("data-into");
    return sel ? document.querySelector(sel) : el;
  }

  function load(el) {
    var url = el.getAttribute("data-load");
    var into = target(el);
    if (!url || !into) return;

    into.setAttribute("aria-busy", "true");
    fetch(url, { headers: { "X-Fragment": "1" } })
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.text();
      })
      .then(function (html) {
        into.innerHTML = html;
      })
      .catch(function (err) {
        // Said out loud rather than left as a spinner that never stops: a panel
        // whose check silently does nothing teaches its user to press twice.
        into.textContent = "Не удалось: " + err.message;
      })
      .finally(function () {
        into.removeAttribute("aria-busy");
      });
  }

  document.addEventListener("click", function (ev) {
    var el = ev.target.closest("[data-load]");
    if (!el) return;
    ev.preventDefault();
    load(el);
  });

  // A form with data-confirm asks before it posts. Used by the delete buttons,
  // where the alternative is an undo this product does not have.
  document.addEventListener("submit", function (ev) {
    var msg = ev.target.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) ev.preventDefault();
  });
})();
```

- [ ] **Step 8: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go vet ./internal/web/ && \
  go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 9: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): the shell, and a panel that stays on this machine"
```

---

## Task 6: The first screen asks which transport, and it asks with numbers

**Files:**
- Create: `internal/web/firstrun.go`, `internal/web/firstrun_test.go`
- Modify: `internal/web/stubs.go` (delete `firstRunPage` and `saveFirstRun`)

Decision 3 of the spec: `settings.transport_mode` starts empty and nothing
collects until it is set. Defaulting to direct would let a user conclude the
product is broken when it is behaving exactly as documented; defaulting to
BlankTrail would contradict mode (b) being first class.

**The numbers on this screen are the soak's and no others.** From
`docs/measurements/2026-08-23-soak.md`: about **250–260 clean requests per hour
direct** and about **360 through BlankTrail**, both from **one shared egress
address**, both **stopped by the operator rather than by the site**. Direct's
real cost is a **74% time tax** — sixty-nine of ninety-three minutes spent
waiting out ten refusals. Nothing on this screen may quote a figure that run did
not produce, and nothing may quote one without its caveat attached.

**Interfaces:**
- Consumes: `store.TransportMode`, `store.SetTransportMode`, `store.ErrNoTransportChosen`, `transport.ModeDirect`, `transport.ModeBlankTrail`.
- Produces: `func (s *Server) firstRunPage(http.ResponseWriter, *http.Request)`, `func (s *Server) saveFirstRun(http.ResponseWriter, *http.Request)`

- [ ] **Step 1: Write the failing test**

Create `internal/web/firstrun_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func post(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestTheFirstScreenOffersNoDefault.
//
// Decision 3. Neither radio starts selected, and the form refuses a post with
// no choice: a pre-selected option is a decision made on somebody's behalf
// about whether their traffic goes through a paid service.
func TestTheFirstScreenOffersNoDefault(t *testing.T) {
	s := newServer(t)
	body := get(t, s, "/first-run").Body.String()

	if strings.Contains(body, "checked") {
		t.Error("one of the two transports is pre-selected")
	}
	for _, want := range []string{"direct", "blanktrail"} {
		if !strings.Contains(body, want) {
			t.Errorf("the form does not offer %q", want)
		}
	}
	if rec := post(t, s, "/first-run", url.Values{}); rec.Code == http.StatusSeeOther {
		t.Error("a post with no choice was accepted")
	}
	if _, err := s.Store.TransportMode(context.Background()); err == nil {
		t.Error("a transport was chosen by a post that chose nothing")
	}
}

// TestTheScreenQuotesTheSoakAndItsCaveat.
//
// Both figures come from one run, from one shared egress address, stopped by
// the operator rather than by the site. A number on a settings screen without
// its sample size is a number somebody plans capacity against.
func TestTheScreenQuotesTheSoakAndItsCaveat(t *testing.T) {
	body := get(t, newServer(t), "/first-run").Body.String()
	for _, want := range []string{"250", "360", "адрес", "оператор"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not mention %q", want)
		}
	}
	// The refuted number must not be back.
	for _, gone := range []string{"300–700", "300-700"} {
		if strings.Contains(body, gone) {
			t.Errorf("the screen quotes %q, which the soak refuted", gone)
		}
	}
}

// TestChoosingDirectSaysWhatStopsIt. Mode (b) is first class, and «бесплатно»
// without «останавливается на капче» is an advertisement rather than a choice.
func TestChoosingDirectSaysWhatStopsIt(t *testing.T) {
	body := get(t, newServer(t), "/first-run").Body.String()
	if !strings.Contains(body, "капч") {
		t.Error("the direct option does not say that a captcha stops it")
	}
	if !strings.Contains(body, "прокси") {
		t.Error("the direct option does not say that a proxy list multiplies it")
	}
}

// TestSavingAChoiceRedirectsToTheWatches, which is what somebody wants next.
func TestSavingAChoiceRedirectsToTheWatches(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/first-run", url.Values{"mode": {"direct"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /first-run = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	mode, err := s.Store.TransportMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mode != transport.ModeDirect {
		t.Errorf("mode = %q", mode)
	}
}

// TestAModeThisBuildDoesNotKnowIsRefused, rather than written into a column
// with a CHECK that would fail later with a message about SQL.
func TestAModeThisBuildDoesNotKnowIsRefused(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/first-run", url.Values{"mode": {"carrier-pigeon"}})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("an unknown transport was accepted")
	}
	if _, err := s.Store.TransportMode(context.Background()); err == nil {
		t.Error("an unknown transport was stored")
	}
}

// TestTheBannerSaysNothingCollectsUntilItIsAnswered, on every screen, because
// somebody who skipped this page would otherwise see an empty watch list and
// conclude the product does not work.
func TestTheBannerSaysNothingCollectsUntilItIsAnswered(t *testing.T) {
	s := newServer(t)
	if body := get(t, s, "/settings").Body.String(); !strings.Contains(body, "Не выбран способ") {
		t.Error("the banner is missing before a transport is chosen")
	}
	if rec := post(t, s, "/first-run", url.Values{"mode": {"blanktrail"}}); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	if body := get(t, s, "/settings").Body.String(); strings.Contains(body, "Не выбран способ") {
		t.Error("the banner is still there after a transport was chosen")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'FirstScreen|Soak|Direct|Choice|Banner|Mode' -v
```

Expected: FAIL — the stub renders «Этот экран ещё не построен».

- [ ] **Step 3: Delete the two stubs**

Remove `firstRunPage` and `saveFirstRun` from `internal/web/stubs.go`.

- [ ] **Step 4: Write `internal/web/firstrun.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// The first screen asks which way out to use, and it asks with numbers.
//
// Spec decision 3: settings.transport_mode starts empty and nothing collects
// until it is set. Defaulting to direct would let a user conclude the product is
// broken when it is behaving exactly as documented; defaulting to BlankTrail
// would contradict mode (b) being first class. Asking once costs one screen and
// is the only honest option.
//
// Every figure below is from docs/measurements/2026-08-23-soak.md and carries
// that run's caveats, because a number on a settings screen is a number
// somebody plans capacity against. The spec's own earlier estimate of «300–700
// requests» is not here: that run refuted it, and neither transport turned out
// to have a ceiling in requests at all.

func (s *Server) firstRunPage(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder

	b.WriteString(`<form method="post" action="/first-run">`)
	b.WriteString(`<div class="row">`)

	b.WriteString(`<div class="card grow">`)
	b.WriteString(`<label class="check"><input type="radio" name="mode" value="direct">` +
		`<span><strong>Напрямую с этой машины</strong></span></label>`)
	b.WriteString(`<p>Бесплатно. Около <strong>250–260 чистых запросов в час</strong> ` +
		`с одного адреса.</p>`)
	b.WriteString(`<p>Останавливается на капче: пройти её этот режим не умеет, ` +
		`и при первой же встрече наблюдение встаёт и само не продолжит.</p>`)
	b.WriteString(`<p>Список прокси умножает первую цифру: сколько адресов — ` +
		`во столько раз больше. Один адрес — один потолок, сколько бы ` +
		`соединений вы ни открыли.</p>`)
	b.WriteString(`<p class="note warning">Три четверти времени уходит на ожидание: ` +
		`в измеренном прогоне 69 минут из 93 машина просто ждала снятия отказов.</p>`)
	b.WriteString(`</div>`)

	b.WriteString(`<div class="card grow">`)
	b.WriteString(`<label class="check"><input type="radio" name="mode" value="blanktrail">` +
		`<span><strong>Через BlankTrail</strong></span></label>`)
	b.WriteString(`<p>Платно, 299 ₽ в месяц. Около <strong>360 чистых запросов в час</strong> ` +
		`с того же одного адреса.</p>`)
	b.WriteString(`<p>Проходит капчу молча: клиент получает обычный ответ, ` +
		`просто дольше. Наблюдение не встаёт.</p>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div>`)

	// The caveat, once, under both — because it applies to both figures and
	// repeating it per card would make it read as fine print rather than as the
	// condition the numbers hold under.
	b.WriteString(note("neutral",
		"Обе цифры — из одного измерения 23–24 августа 2026 года: один общий "+
			"адрес выхода, около 1200 запросов, и в обоих случаях прогон "+
			"остановил оператор, а не площадка. С другого адреса или с "+
			"нескольких числа будут другие."))
	b.WriteString(note("neutral",
		"Это можно поменять позже в настройках."))

	b.WriteString(`<p><button class="primary" type="submit">Сохранить</button></p>`)
	b.WriteString(`</form>`)

	s.render(w, r, page{Title: "Как выходить в сеть", Body: rawHTML(b.String())})
}

func (s *Server) saveFirstRun(w http.ResponseWriter, r *http.Request) {
	mode, err := parseMode(r.FormValue("mode"))
	if err != nil {
		s.render(w, r, page{
			Title: "Как выходить в сеть",
			Body:  rawHTML(note("bad", err.Error())),
		})
		return
	}
	if err := s.Store.SetTransportMode(r.Context(), mode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// parseMode refuses anything this build cannot run.
//
// Refused here rather than at the column's CHECK constraint, so the person
// reading the message is told which choices exist instead of being shown an SQL
// error about a table they have never heard of.
func parseMode(v string) (transport.Mode, error) {
	switch v {
	case string(transport.ModeDirect):
		return transport.ModeDirect, nil
	case string(transport.ModeBlankTrail):
		return transport.ModeBlankTrail, nil
	case "":
		return "", errors.New("выберите один из двух способов")
	}
	return "", fmt.Errorf("способ %q эта сборка не знает", v)
}

// transportChosen reports whether decision 3 has been answered.
var _ = store.ErrNoTransportChosen
```

Delete the `var _ =` line and the `store` import if `go vet` says they are
unused — `banner` in `server.go` is the only reader of `ErrNoTransportChosen`.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): the first screen asks which transport, with the soak's own numbers"
```

---

## Task 7: The budget arithmetic

**Files:**
- Create: `internal/web/budget.go`, `internal/web/budget_test.go`

Split from the screen because this is the one thing on it that can be wrong in a
way nobody notices. A schedule accepted that quietly exceeds the address ceiling
is a ban three days later, and by then nobody connects the two.

**The two limits, and which one binds.** `internal/transport/pace.go` holds both:
a per-identity floor of 3 500 ms and a per-address ceiling of 60 requests per
10 minutes. Six a minute is the tighter of the two — one identity could manage
seventeen — and the spec says so explicitly about `requests_per_minute`
defaulting to 6: «the window is the binding constraint and quoting the looser of
two limits is how a budget gets overspent».

**Addresses multiply it.** `transport.Caps.Addresses` is what the budget
multiplies, and it is why «I added ports and it got banned faster» has an answer:
ports are not addresses. One address is one ceiling however many are open.

**Interfaces:**
- Produces:

```go
// In internal/web:
type Budget struct {
    Addresses        int
    PerAddressPerMin float64
    CapacityPerMin   float64
    UsedPerMin       float64
}
func (b Budget) FreePerMin() float64
func (b Budget) Fits(costPerMin float64) bool
func CostPerMinute(w store.Watch) float64
func BudgetFor(requestsPerMinute, addresses int, watches []store.Watch, exclude int64) Budget
func (b Budget) Explain(costPerMin float64) string
```

- [ ] **Step 1: Write the failing test**

Create `internal/web/budget_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"math"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func watch(id int64, intervalSec, pages int, enabled bool) store.Watch {
	return store.Watch{ID: id, Name: "наблюдение", MaxPages: pages,
		IntervalSec: intervalSec, Enabled: enabled}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestOnePageEveryFiveMinutesCostsAFifthOfARequestAMinute.
func TestOnePageEveryFiveMinutesCostsAFifthOfARequestAMinute(t *testing.T) {
	if got := CostPerMinute(watch(1, 300, 1, true)); !near(got, 0.2) {
		t.Errorf("CostPerMinute = %v, want 0.2", got)
	}
	// Three pages costs three times as much: a pass is one request per page.
	if got := CostPerMinute(watch(1, 300, 3, true)); !near(got, 0.6) {
		t.Errorf("CostPerMinute = %v, want 0.6", got)
	}
}

// TestAWatchWithNoIntervalCostsNothingRatherThanInfinity.
//
// Zero is what an unsaved form and a hand-edited row both hold. Divided into
// sixty it is +Inf, which would make every budget full and every save refused —
// a product that cannot be configured because one row is malformed.
func TestAWatchWithNoIntervalCostsNothingRatherThanInfinity(t *testing.T) {
	if got := CostPerMinute(watch(1, 0, 1, true)); got != 0 {
		t.Errorf("CostPerMinute = %v, want 0", got)
	}
	if got := CostPerMinute(watch(1, -5, 1, true)); got != 0 {
		t.Errorf("CostPerMinute = %v, want 0", got)
	}
}

// TestAWatchWithNoPagesStillCostsOne. max_pages is how many pages past the
// first, in some readings; here it is how many pages, and zero means one — a
// pass that fetched nothing would not be a pass.
func TestAWatchWithNoPagesStillCostsOne(t *testing.T) {
	if got := CostPerMinute(watch(1, 60, 0, true)); !near(got, 1) {
		t.Errorf("CostPerMinute = %v, want 1", got)
	}
}

// TestADisabledWatchCostsNothing.
func TestADisabledWatchCostsNothing(t *testing.T) {
	b := BudgetFor(6, 1, []store.Watch{watch(1, 60, 1, false)}, 0)
	if b.UsedPerMin != 0 {
		t.Errorf("a disabled watch used %v", b.UsedPerMin)
	}
}

// TestTheCeilingIsTheTighterOfTheTwoLimits.
//
// transport.DefaultCeiling is 60 in a 10-minute window, which is six a minute.
// A person who typed 60 into requests_per_minute meant "as fast as possible"
// and must still get six: quoting the looser of two limits is how a budget gets
// overspent.
func TestTheCeilingIsTheTighterOfTheTwoLimits(t *testing.T) {
	b := BudgetFor(60, 1, nil, 0)
	if !near(b.PerAddressPerMin, 6) {
		t.Errorf("PerAddressPerMin = %v, want 6", b.PerAddressPerMin)
	}
	// And a lower setting is honoured, because that direction is the user
	// choosing to be gentler than the measurement requires.
	if b := BudgetFor(2, 1, nil, 0); !near(b.PerAddressPerMin, 2) {
		t.Errorf("PerAddressPerMin = %v, want 2", b.PerAddressPerMin)
	}
}

// TestAddressesMultiplyTheCeiling.
//
// This is why «I added ports and it got banned faster» has an answer: ports are
// not addresses. Four addresses is four ceilings; four ports on one address is
// one ceiling reached four times as fast.
func TestAddressesMultiplyTheCeiling(t *testing.T) {
	b := BudgetFor(6, 4, nil, 0)
	if !near(b.CapacityPerMin, 24) {
		t.Errorf("CapacityPerMin = %v, want 24", b.CapacityPerMin)
	}
	// Zero addresses is one: the host's own. A budget of nothing would refuse
	// every watch on an install that has not configured a proxy, which is most
	// of them.
	if b := BudgetFor(6, 0, nil, 0); b.Addresses != 1 || !near(b.CapacityPerMin, 6) {
		t.Errorf("with no channels: %+v, want one address and six a minute", b)
	}
}

// TestAScheduleOverTheBudgetDoesNotFit.
func TestAScheduleOverTheBudgetDoesNotFit(t *testing.T) {
	// One address, six a minute. Six watches at one page a minute is exactly
	// the ceiling; a seventh is over it.
	ws := []store.Watch{}
	for i := int64(1); i <= 6; i++ {
		ws = append(ws, watch(i, 60, 1, true))
	}
	b := BudgetFor(6, 1, ws, 0)
	if !near(b.UsedPerMin, 6) {
		t.Errorf("UsedPerMin = %v, want 6", b.UsedPerMin)
	}
	if !near(b.FreePerMin(), 0) {
		t.Errorf("FreePerMin = %v, want 0", b.FreePerMin())
	}
	if b.Fits(1) {
		t.Error("a seventh watch fitted into a full budget")
	}
	// Exactly at the ceiling fits: a person who filled it deliberately must not
	// be told their own arithmetic is wrong.
	if b := BudgetFor(6, 1, ws[:5], 0); !b.Fits(1) {
		t.Error("the sixth watch did not fit into a budget with room for it")
	}
}

// TestEditingAWatchDoesNotCountItTwice.
//
// The save posts the new schedule for a watch that is already in the list. Not
// excluding it makes every edit look like an addition, so tightening an
// interval from ten minutes to five is refused for exceeding a budget that the
// watch's own old cost was filling.
func TestEditingAWatchDoesNotCountItTwice(t *testing.T) {
	ws := []store.Watch{watch(1, 60, 6, true)} // exactly six a minute
	full := BudgetFor(6, 1, ws, 0)
	if full.Fits(6) {
		t.Fatal("the budget is not full; the fixture is wrong")
	}
	editing := BudgetFor(6, 1, ws, 1)
	if !near(editing.UsedPerMin, 0) {
		t.Errorf("UsedPerMin = %v with the edited watch excluded, want 0", editing.UsedPerMin)
	}
	if !editing.Fits(6) {
		t.Error("re-saving a watch with its own schedule was refused")
	}
}

// TestExplainShowsTheSumAndNotJustAVerdict.
//
// Spec: a schedule over the ceiling is refused «with the sum shown». «Слишком
// часто» tells a person nothing about which number to change.
func TestExplainShowsTheSumAndNotJustAVerdict(t *testing.T) {
	ws := []store.Watch{watch(1, 60, 5, true)}
	got := BudgetFor(6, 2, ws, 0).Explain(4)
	for _, want := range []string{"5", "4", "12", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain = %q; it does not carry %q", got, want)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'Budget|CostPerMinute|Ceiling|Addresses|Schedule|Editing|Explain' -v
```

Expected: FAIL to build — `undefined: CostPerMinute`.

- [ ] **Step 3: Write `internal/web/budget.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	"github.com/BlankTrail/avito-monitor/internal/ru"
)

// The arithmetic that decides whether a schedule can be kept.
//
// In its own file because it is the one thing on the watches screen that can be
// wrong without anybody noticing. A schedule accepted that quietly exceeds the
// address ceiling is a ban three days later, and by then nobody connects the
// two — so this is testable as arithmetic rather than through a form post.

// hardCeilingPerMin is the measured per-address ceiling, a minute at a time.
//
// transport.DefaultCeiling requests in transport.DefaultWindow — sixty in ten
// minutes. It is the binding limit and not the per-identity floor of 3.5 s,
// which would allow seventeen a minute: quoting the looser of two limits is how
// a budget gets overspent.
func hardCeilingPerMin() float64 {
	return float64(transport.DefaultCeiling) / transport.DefaultWindow.Minutes()
}

// Budget is what the addresses allow against what the watches ask for.
type Budget struct {
	// Addresses is how many distinct egress addresses are configured, never
	// fewer than one — the host's own counts.
	Addresses int
	// PerAddressPerMin is the effective ceiling for one address.
	PerAddressPerMin float64
	CapacityPerMin   float64
	UsedPerMin       float64
}

func (b Budget) FreePerMin() float64 { return b.CapacityPerMin - b.UsedPerMin }

// Fits reports whether one more cost can be added.
//
// Inclusive of the ceiling: a person who filled their budget exactly meant to,
// and telling them their own arithmetic is wrong at the last request would be
// the product arguing with them over a rounding.
func (b Budget) Fits(costPerMin float64) bool {
	return b.UsedPerMin+costPerMin <= b.CapacityPerMin+1e-9
}

// CostPerMinute is what one watch asks of the budget.
//
// One request per page per pass. A zero or negative interval costs nothing
// rather than infinity: zero is what an unsaved form and a hand-edited row both
// hold, and sixty divided by it is +Inf — which would make every budget full and
// every save refused, a product that cannot be configured because one row is
// malformed.
func CostPerMinute(w store.Watch) float64 {
	if !w.Enabled || w.IntervalSec <= 0 {
		return 0
	}
	pages := w.MaxPages
	if pages <= 0 {
		// A pass that fetched nothing would not be a pass.
		pages = 1
	}
	return float64(pages) * 60 / float64(w.IntervalSec)
}

// BudgetFor is the arithmetic for a set of watches.
//
// exclude is the id of the watch being edited, whose current cost must not be
// counted against the schedule replacing it. Without it every edit looks like an
// addition, and tightening an interval from ten minutes to five is refused for
// exceeding a budget the watch's own old cost was filling.
func BudgetFor(requestsPerMinute, addresses int, watches []store.Watch, exclude int64) Budget {
	if addresses < 1 {
		// The host's own address. A budget of nothing would refuse every watch
		// on an install that has not configured a proxy, which is most of them.
		addresses = 1
	}
	per := hardCeilingPerMin()
	if v := float64(requestsPerMinute); v > 0 && v < per {
		// Lower than the measurement is the user choosing to be gentler, and
		// that direction is theirs to choose. Higher is not.
		per = v
	}

	b := Budget{
		Addresses:        addresses,
		PerAddressPerMin: per,
		CapacityPerMin:   per * float64(addresses),
	}
	for _, w := range watches {
		if w.ID == exclude {
			continue
		}
		b.UsedPerMin += CostPerMinute(w)
	}
	return b
}

// Explain is the sum, written out.
//
// The spec says a schedule over the ceiling is refused «with the sum shown».
// «Слишком часто» tells a person nothing about which of their numbers to
// change, so this names every term: what is already spent, what this schedule
// asks for, and what the addresses allow.
func (b Budget) Explain(costPerMin float64) string {
	var s strings.Builder
	fmt.Fprintf(&s, "Уже занято %s запр./мин, это наблюдение просит %s, ",
		num(b.UsedPerMin), num(costPerMin))
	fmt.Fprintf(&s, "потолок %s — %d %s × %s запр./мин.",
		num(b.CapacityPerMin), b.Addresses,
		ru.Plural(b.Addresses, "адрес", "адреса", "адресов"),
		num(b.PerAddressPerMin))
	return s.String()
}

// num renders a rate without trailing zeroes, because «0.20 запр./мин» reads as
// a measurement and this is a division.
func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	// A comma, like every other number this product shows a Russian reader.
	return strings.Replace(s, ".", ",", 1)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): the budget arithmetic, with the tighter of the two limits"
```

---

## Task 8: Наблюдения

**Files:**
- Create: `internal/web/watches.go`, `internal/web/watches_test.go`
- Modify: `internal/web/stubs.go` (delete the four watch handlers)

**Interfaces:**
- Consumes: `BudgetFor`, `CostPerMinute`, `Budget.Fits`, `Budget.Explain` from Task 7; `store.Watches`, `store.AddWatch`, `store.UpdateWatch`, `store.DeleteWatch` from Task 2; `store.Channels`, `store.CountProxies` from Task 3; `rules.DefaultsForWatch`, `rules.GlobalDefaults`, `rules.All`, `rules.Save`.
- Produces: the four handlers, and `func (s *Server) addressCount(ctx context.Context) (int, error)`.

**The interval floor is 120 seconds, and the screen says why.** Measured
2026-08-24 (`docs/measurements/2026-08-24-how-fast-the-window-turns-over.md`):
within about a minute Avito returns the same page byte for byte — three passes
one to four seconds apart came back with the same list AND the same
`total_count`, as did a pair forty-seven seconds apart. Polling faster than that
spends requests and collects nothing.

- [ ] **Step 1: Write the failing test**

Create `internal/web/watches_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func addWatchForm(name, u string, intervalSec int) url.Values {
	return url.Values{
		"name": {name}, "url": {u},
		"interval": {strconv.Itoa(intervalSec)},
		"pages":    {"1"}, "surface": {"mobile"},
	}
}

// TestAWatchIsAddedFromTheBrowser, which is the whole point of this screen.
func TestAWatchIsAddedFromTheBrowser(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /watches = %d (%s)", rec.Code, rec.Body.String())
	}
	ws, err := s.Store.Watches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Name != "iphone" || ws[0].IntervalSec != 300 {
		t.Fatalf("stored %+v", ws)
	}
	if !strings.Contains(get(t, s, "/").Body.String(), "iphone") {
		t.Error("the new watch is not on the screen")
	}
}

// TestAScheduleOverTheBudgetIsRefusedWithTheSumShown.
//
// The spec's own words. One address gives six requests a minute; a watch every
// ten seconds asks for six on its own, so a second one cannot fit.
func TestAScheduleOverTheBudgetIsRefusedWithTheSumShown(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/watches",
		addWatchForm("первое", "https://m.avito.ru/all?q=a", 120)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the first watch was refused: %s", rec.Body.String())
	}
	// Twelve watches at one page every two minutes is exactly the ceiling.
	for i := 2; i <= 12; i++ {
		post(t, s, "/watches",
			addWatchForm("n"+strconv.Itoa(i), "https://m.avito.ru/all?q="+strconv.Itoa(i), 120))
	}
	rec := post(t, s, "/watches",
		addWatchForm("лишнее", "https://m.avito.ru/all?q=z", 120))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a schedule over the ceiling was accepted")
	}
	body := rec.Body.String()
	for _, want := range []string{"потолок", "адрес", "запр./мин"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not carry %q: %s", want, body)
		}
	}
}

// TestAnIntervalUnderTheFloorIsRefusedWithTheReason.
//
// Measured 2026-08-24: within about a minute Avito returns the same page byte
// for byte, total_count included. Polling faster spends requests and collects
// nothing, so the refusal says that rather than «слишком часто».
func TestAnIntervalUnderTheFloorIsRefusedWithTheReason(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/watches",
		addWatchForm("часто", "https://m.avito.ru/all?q=a", 30))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a 30-second interval was accepted")
	}
	if !strings.Contains(rec.Body.String(), "тот же ответ") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
}

// TestAnUrlThatIsNotAvitoIsRefused, before a request is ever made to it.
func TestAnUrlThatIsNotAvitoIsRefused(t *testing.T) {
	s := newServer(t)
	for _, bad := range []string{
		"", "не ссылка", "https://example.com/all?q=a", "ftp://avito.ru/x",
	} {
		rec := post(t, s, "/watches", addWatchForm("плохое", bad, 300))
		if rec.Code == http.StatusSeeOther {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestAddingAWatchCreatesItsDefaultRulesWhenThereIsSomebodyToTell.
//
// The command-line path already does this (cmd/avitomon/seed.go). Adding one
// from the browser must not be the path that silently produces a watch nobody
// hears about.
func TestAddingAWatchCreatesItsDefaultRulesWhenThereIsSomebodyToTell(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.Store.SaveTarget(ctx, store.TargetRow{
		Name: "мне", Kind: "telegram", Address: "42", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300)); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	rows, err := s.Store.Rules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 9 {
		t.Errorf("%d rules created, want the four per-watch and the five global", len(rows))
	}
}

// TestWithNobodyToTellTheScreenSaysSoRatherThanFailing.
//
// A rule with no target cannot pass Validate, deliberately. The watch is still
// worth creating — collection is useful without notification — so the screen
// says what is missing instead of refusing the save.
func TestWithNobodyToTellTheScreenSaysSoRatherThanFailing(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/watches",
		addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the watch was refused for having no recipient: %s", rec.Body.String())
	}
	if !strings.Contains(get(t, s, "/").Body.String(), "получател") {
		t.Error("the screen does not say that nobody will be told")
	}
}

// TestTheScreenShowsWhatEachWatchCostsAndWhatIsLeft.
func TestTheScreenShowsWhatEachWatchCostsAndWhatIsLeft(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	body := get(t, s, "/").Body.String()
	for _, want := range []string{"0,2", "запр./мин", "потолок"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestTogglingAWatchOffFreesItsBudget, which is what somebody does when they
// want room for another one without deleting this.
func TestTogglingAWatchOffFreesItsBudget(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	ws, err := s.Store.Watches(context.Background())
	if err != nil || len(ws) != 1 {
		t.Fatalf("watches = %+v (%v)", ws, err)
	}
	id := strconv.FormatInt(ws[0].ID, 10)
	if rec := post(t, s, "/watches/toggle", url.Values{"id": {id}}); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Code)
	}
	ws, _ = s.Store.Watches(context.Background())
	if ws[0].Enabled {
		t.Error("the watch is still enabled")
	}
}

// TestDeletingAWatchAsks. The confirmation is on the form, because this product
// has no undo for it.
func TestDeletingAWatchAsks(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches", addWatchForm("iphone", "https://m.avito.ru/all?q=iphone", 300))
	if !strings.Contains(get(t, s, "/").Body.String(), "data-confirm") {
		t.Error("the delete button does not ask")
	}
}

// TestAWatchNameIsEscaped, because it is a string a person pastes in and the
// panel renders back on the machine that holds their proxy key.
func TestAWatchNameIsEscaped(t *testing.T) {
	s := newServer(t)
	post(t, s, "/watches",
		addWatchForm(`<script>alert(1)</script>`, "https://m.avito.ru/all?q=a", 300))
	if strings.Contains(get(t, s, "/").Body.String(), "<script>alert(1)</script>") {
		t.Error("a watch name was rendered as markup")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'Watch|Schedule|Interval|Url|Toggling|Deleting' -v
```

Expected: FAIL — the stub renders «Этот экран ещё не построен».

- [ ] **Step 3: Delete the four stubs**

Remove `watchesPage`, `saveWatch`, `deleteWatch` and `toggleWatch` from
`internal/web/stubs.go`.

- [ ] **Step 4: Write `internal/web/watches.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// minIntervalSec is the floor on how often a watch may be polled.
//
// Measured 2026-08-24: within about a minute Avito returns the same page byte
// for byte — three passes one to four seconds apart came back with the same
// list AND the same total_count, as did a pair forty-seven seconds apart. Two
// minutes leaves room either side of that without spending requests on a page
// that has not moved. See docs/measurements/2026-08-24-how-fast-the-window-turns-over.md.
const minIntervalSec = 120

// maxPagesPerPass bounds what one watch may ask for in one pass.
//
// Not a technical limit: it is the number above which a single watch can eat a
// whole address's budget in one tick, and the person who typed it would learn
// that as a ban rather than as a refusal.
const maxPagesPerPass = 10

func (s *Server) watchesPage(w http.ResponseWriter, r *http.Request) {
	s.renderWatches(w, r, "", store.Watch{Surface: avito.SurfaceMobile,
		MaxPages: 1, IntervalSec: 300})
}

// renderWatches draws the list, the budget and the form.
//
// problem is a refusal to show above the form, already escaped by its producer.
// draft is what the form is filled with — an empty watch normally, and the
// rejected input when there is a problem, so a person does not retype it.
func (s *Server) renderWatches(w http.ResponseWriter, r *http.Request, problem string, draft store.Watch) {
	ctx := r.Context()
	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	addresses, err := s.addressCount(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rpm, err := s.requestsPerMinute(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b := BudgetFor(rpm, addresses, watches, 0)

	var body strings.Builder

	// The budget first: it is the constraint every row below is measured
	// against, and putting it under the table would make it read as a footnote.
	body.WriteString(`<div class="card">`)
	fmt.Fprintf(&body, `<p class="sum">Потолок <strong>%s</strong> запр./мин — `+
		`%d %s × %s. Занято <strong>%s</strong>, свободно <strong>%s</strong>.</p>`,
		esc(num(b.CapacityPerMin)), b.Addresses,
		esc(ru.Plural(b.Addresses, "адрес", "адреса", "адресов")),
		esc(num(b.PerAddressPerMin)), esc(num(b.UsedPerMin)), esc(num(b.FreePerMin())))
	if b.Addresses == 1 {
		body.WriteString(note("neutral",
			"Один адрес — один потолок, сколько бы соединений ни было открыто. "+
				"Список прокси на вкладке «Прокси» умножает его."))
	}
	body.WriteString(`</div>`)

	if len(watches) > 0 {
		body.WriteString(s.watchTable(ctx, watches))
	}

	if problem != "" {
		body.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}
	body.WriteString(s.watchForm(draft))

	s.render(w, r, page{Title: "Наблюдения", Body: rawHTML(body.String())})
}

func (s *Server) watchTable(ctx context.Context, watches []store.Watch) string {
	// Said once, under the table, when there is nobody configured: a watch with
	// no recipient collects and tells nobody, and that is a state a person
	// would otherwise discover as silence.
	targets, _ := s.Store.EnabledTargets(ctx)

	var b strings.Builder
	b.WriteString(`<div class="card"><table>`)
	b.WriteString(`<tr><th>Название</th><th>Интервал</th><th class="num">Стр.</th>` +
		`<th class="num">Запр./мин</th><th>Состояние</th><th></th></tr>`)
	for _, w := range watches {
		state := `<span class="pill ok">идёт</span>`
		switch {
		case w.State == "halted":
			state = fmt.Sprintf(`<span class="pill bad">остановлено: %s</span>`,
				esc(w.HaltedReason))
		case !w.Enabled:
			state = `<span class="pill warn">выключено</span>`
		case w.SeededAt == nil:
			state = `<span class="pill warn">ещё не засеяно</span>`
		}
		id := strconv.FormatInt(w.ID, 10)
		fmt.Fprintf(&b, `<tr><td>%s<div class="hint">%s</div></td>`+
			`<td>%d с</td><td class="num">%d</td><td class="num">%s</td><td>%s</td>`,
			esc(w.Name), esc(w.URL), w.IntervalSec, w.MaxPages,
			esc(num(CostPerMinute(w))), state)
		fmt.Fprintf(&b, `<td><form method="post" action="/watches/toggle">`+
			`<input type="hidden" name="id" value="%s">`+
			`<button type="submit">%s</button></form>`,
			attr(id), map[bool]string{true: "Выключить", false: "Включить"}[w.Enabled])
		fmt.Fprintf(&b, `<form method="post" action="/watches/delete" `+
			`data-confirm="Удалить наблюдение «%s»? Историю это не тронет.">`+
			`<input type="hidden" name="id" value="%s">`+
			`<button class="danger" type="submit">Удалить</button></form></td></tr>`,
			attr(w.Name), attr(id))
	}
	b.WriteString(`</table>`)
	if len(targets) == 0 {
		b.WriteString(note("warning",
			"Получателей нет — эти наблюдения собирают данные, но никому о них "+
				"не сообщают. Добавьте получателя, и правила по умолчанию "+
				"создадутся при следующем добавлении наблюдения."))
	}
	b.WriteString(`</div>`)
	return b.String()
}

func (s *Server) watchForm(d store.Watch) string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Добавить наблюдение</h2>`)
	b.WriteString(`<form method="post" action="/watches">`)
	b.WriteString(field("Название", "name", d.Name, "text",
		"Как оно будет называться в сообщениях."))
	b.WriteString(field("Ссылка на поиск", "url", d.URL, "text",
		"Страница выдачи avito.ru — скопируйте её из адресной строки."))
	b.WriteString(field("Интервал, секунды", "interval",
		strconv.Itoa(d.IntervalSec), "number",
		fmt.Sprintf("Не меньше %d. Чаще минуты Авито отдаёт тот же ответ, "+
			"так что запросы тратятся впустую.", minIntervalSec)))
	b.WriteString(field("Страниц за проход", "pages",
		strconv.Itoa(d.MaxPages), "number",
		fmt.Sprintf("Один запрос на страницу. Не больше %d.", maxPagesPerPass)))
	b.WriteString(`<label class="field"><span class="label">Версия сайта</span>` +
		`<select name="surface">` +
		`<option value="mobile">мобильная (больше карточек на странице)</option>` +
		`<option value="desktop">десктопная</option>` +
		`</select></label>`)
	b.WriteString(`<button class="primary" type="submit">Добавить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func (s *Server) saveWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	draft, err := watchFromForm(r)
	if err != nil {
		s.renderWatches(w, r, err.Error(), draft)
		return
	}

	watches, err := s.Store.Watches(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	addresses, err := s.addressCount(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rpm, err := s.requestsPerMinute(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b := BudgetFor(rpm, addresses, watches, draft.ID)
	cost := CostPerMinute(draft)
	if !b.Fits(cost) {
		// Refused here rather than discovered as a ban three days later, and
		// with the sum shown rather than a verdict — «слишком часто» tells a
		// person nothing about which number to change.
		s.renderWatches(w, r,
			"Это расписание не помещается в бюджет. "+b.Explain(cost), draft)
		return
	}

	now := s.now().Unix()
	draft.Kind = "search"
	draft.AbsenceConfirmations = 2
	draft.AbsenceMinSpanSec = 1200
	// Due immediately. A watch somebody has just added and that does nothing
	// for five minutes reads as broken.
	draft.NextDueAt = now
	draft.Enabled = true
	draft.CreatedAt, draft.UpdatedAt = now, now

	id, err := s.Store.AddWatch(ctx, draft)
	if err != nil {
		s.renderWatches(w, r, err.Error(), draft)
		return
	}
	// The same seeding the command line does. A watch added from the browser
	// must not be the path that silently produces one nobody hears about.
	if err := s.seedRules(ctx, id); err != nil {
		s.renderWatches(w, r, "Наблюдение создано, но правила — нет: "+err.Error(), store.Watch{})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// seedRules creates the four per-watch defaults and, once, the five global
// ones. Nothing is created when there is nobody to tell: a rule with no target
// cannot pass Validate, deliberately, and the watch is still worth having.
func (s *Server) seedRules(ctx context.Context, watchID int64) error {
	targets, err := s.Store.EnabledTargets(ctx)
	if err != nil || len(targets) == 0 {
		return err
	}
	existing, err := rules.All(ctx, s.Store)
	if err != nil {
		return err
	}
	made := rules.DefaultsForWatch(watchID, targets)
	global := true
	for _, r := range existing {
		if r.FromDefaults && r.Scope.Kind == rules.ScopeFilter {
			global = false
			break
		}
	}
	if global {
		made = append(made, rules.GlobalDefaults(targets)...)
	}
	for _, r := range made {
		if _, err := rules.Save(ctx, s.Store, r); err != nil {
			return fmt.Errorf("правило «%s»: %w", r.Name, err)
		}
	}
	return nil
}

func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого наблюдения", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteWatch(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) toggleWatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого наблюдения", http.StatusBadRequest)
		return
	}
	cur, err := s.Store.Watch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	cur.Enabled = !cur.Enabled
	if err := s.Store.UpdateWatch(r.Context(), cur); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// watchFromForm reads and checks one form, reporting every fault it can see.
func watchFromForm(r *http.Request) (store.Watch, error) {
	w := store.Watch{
		Name:    strings.TrimSpace(r.FormValue("name")),
		URL:     strings.TrimSpace(r.FormValue("url")),
		Surface: avito.Surface(r.FormValue("surface")),
	}
	w.IntervalSec, _ = strconv.Atoi(r.FormValue("interval"))
	w.MaxPages, _ = strconv.Atoi(r.FormValue("pages"))
	w.Enabled = true
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		w.ID = id
	}

	var bad []string
	if w.Name == "" {
		bad = append(bad, "не задано название")
	}
	if err := checkSearchURL(w.URL); err != nil {
		bad = append(bad, err.Error())
	}
	if w.IntervalSec < minIntervalSec {
		bad = append(bad, fmt.Sprintf(
			"интервал меньше %d секунд: чаще минуты Авито отдаёт тот же ответ "+
				"с точностью до байта, так что запросы тратятся впустую",
			minIntervalSec))
	}
	if w.MaxPages < 1 || w.MaxPages > maxPagesPerPass {
		bad = append(bad, fmt.Sprintf("страниц за проход должно быть от 1 до %d",
			maxPagesPerPass))
	}
	switch w.Surface {
	case avito.SurfaceMobile, avito.SurfaceDesktop:
	default:
		bad = append(bad, "неизвестная версия сайта")
	}
	if len(bad) > 0 {
		return w, errors.New(strings.Join(bad, "; "))
	}
	return w, nil
}

// checkSearchURL refuses anything this program will not fetch.
//
// Before a request is ever made rather than after: a watch pointed at another
// site would spend a pass and a pacing slot to arrive at a page the extractor
// cannot read, and report it as a collection failure.
func checkSearchURL(raw string) error {
	if raw == "" {
		return errors.New("не задана ссылка")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("ссылку не удалось разобрать")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("ссылка должна начинаться с http:// или https://")
	}
	host := strings.ToLower(u.Hostname())
	if host != "avito.ru" && !strings.HasSuffix(host, ".avito.ru") {
		return errors.New("это не ссылка на avito.ru")
	}
	return nil
}

// addressCount is how many distinct egress addresses are configured.
//
// Never fewer than one: the host's own address is an address, and a budget of
// nothing would refuse every watch on an install that has not configured a
// proxy — which is most of them.
func (s *Server) addressCount(ctx context.Context) (int, error) {
	channels, err := s.Store.Channels(ctx)
	if err != nil {
		return 1, err
	}
	counts, err := s.Store.CountProxies(ctx)
	if err != nil {
		return 1, err
	}
	total := 0
	for _, c := range channels {
		if !c.Enabled {
			continue
		}
		switch c.Kind {
		case "proxy-list":
			total += counts[c.ID]
		case "rotating", "gateway", "direct":
			// One address each. A rotating channel is one address at a time —
			// that is what makes it rotating — and a gateway is one exit
			// however many ports point at it.
			total++
		}
	}
	if total < 1 {
		return 1, nil
	}
	return total, nil
}

// requestsPerMinute is the user's own ceiling, from settings.
//
// Through the store rather than a query here: every other package in this
// program reaches SQLite through internal/store, and a handler holding SQL
// would be the one place a schema change could hide.
func (s *Server) requestsPerMinute(ctx context.Context) (int, error) {
	return s.Store.RequestsPerMinute(ctx)
}
```

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): Наблюдения, and a schedule refused with the sum shown"
```

---

## Task 9: Настройки

**Files:**
- Create: `internal/web/settings.go`, `internal/web/settings_test.go`
- Modify: `internal/web/stubs.go` (delete the four settings handlers)

The spec enumerates this screen rather than leaving it implied, because it is
where decision 3 lands and what every other decision quietly assumes: the
transport choice and its credentials, the Telegram token beside the MTProto
pair, quiet hours, the panel password and the LAN tick, autostart, and the
retention and free-space numbers **with their current values rather than their
names**.

**Three rules this screen must not break.**

1. **A secret already set is never rendered back.** The BlankTrail key, the bot
   token and the api_hash are shown as «задан» and an empty input. A page that
   re-rendered them puts them in the browser's history, in a screenshot and in
   whatever a person pastes into an issue.
2. **An empty secret field means «не менять», not «стереть».** Otherwise saving
   quiet hours wipes the bot token.
3. **The MTProto pair stays hidden until the Bot API has actually failed.** The
   schema says so: «a machine where the direct route works is never sent to
   my.telegram.org».

**Interfaces:**
- Consumes: `HashPassword` (Task 4), `store.PanelSettings`/`SetPanelSettings`, `store.StoredRetention`/`SetStoredRetention`, `store.SpaceFloorBytes`/`SetSpaceFloorBytes`, `store.RequestsPerMinute`/`SetRequestsPerMinute` (Task 2), `store.BlankTrailConfig`/`SetBlankTrailConfig`, `store.TelegramConfig`/`SetTelegramConfig`, `store.TransportMode`/`SetTransportMode`, `parseMode` (Task 6), `s.CheckBlankTrail`, `s.CheckTelegram`, `s.Autostart`.
- Produces: `settingsPage`, `saveSettings`, `checkBlankTrail`, `checkTelegram`.

- [ ] **Step 1: Write the failing test**

Create `internal/web/settings_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

const secretKey = "0000000000000000000000000000000000000000"

func settingsForm(over url.Values) url.Values {
	f := url.Values{
		"mode": {"direct"}, "requests_per_minute": {"6"},
		"quiet_from": {""}, "quiet_to": {""},
		"retention_body_days": {"30"}, "retention_body_mb": {"512"},
		"space_floor_gb": {"5"},
	}
	for k, v := range over {
		f[k] = v
	}
	return f
}

// TestASecretIsNeverRenderedBack.
//
// The page goes into a browser's history, into screenshots and into whatever a
// person pastes when they ask for help. «Задан» plus an empty box says the same
// thing without handing the key over.
func TestASecretIsNeverRenderedBack(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetBlankTrailConfig(ctx, store.BlankTrailConfig{
		ControlURL: "http://127.0.0.1:8891", APIKey: secretKey, Ports: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret", APIID: 42, APIHash: "hashhash",
	}); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/settings").Body.String()
	for _, secret := range []string{secretKey, "AAHsecretsecret", "hashhash"} {
		if strings.Contains(body, secret) {
			t.Errorf("the page carries a secret: %q", secret)
		}
	}
	if !strings.Contains(body, "задан") {
		t.Error("the page does not say that a secret is already set")
	}
	// The non-secret half is shown, because it is what somebody checks.
	if !strings.Contains(body, "127.0.0.1:8891") {
		t.Error("the control URL is not shown")
	}
}

// TestAnEmptySecretFieldMeansLeaveItAlone.
//
// Otherwise saving quiet hours wipes the bot token, and the person finds out
// when their alerts stop.
func TestAnEmptySecretFieldMeansLeaveItAlone(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret",
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"quiet_from": {"22:30"}, "quiet_to": {"07:00"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.BotToken != "123456:AAHsecretsecret" {
		t.Errorf("the bot token was cleared by a save that did not mention it")
	}
}

// TestASecretCanStillBeCleared, deliberately, by a control that says so.
func TestASecretCanStillBeCleared(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret",
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"telegram_token_clear": {"1"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	got, _ := s.Store.TelegramConfig(ctx)
	if got.BotToken != "" {
		t.Error("an explicit clear did not clear the token")
	}
}

// TestTheMTProtoPairStaysHiddenUntilTheBotApiHasFailed.
//
// The schema's own words: «a machine where the direct route works is never sent
// to my.telegram.org». Offering it up front asks somebody to register an
// application they will never need.
func TestTheMTProtoPairStaysHiddenUntilTheBotApiHasFailed(t *testing.T) {
	s := newServer(t)
	if strings.Contains(get(t, s, "/settings").Body.String(), "my.telegram.org") {
		t.Error("the MTProto pair is offered before anything has failed")
	}
	// It appears once a check has failed.
	s.CheckTelegram = func(context.Context, string) (string, error) {
		return "", errors.New("api.telegram.org недоступен")
	}
	if rec := get(t, s, "/settings/telegram"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(get(t, s, "/settings").Body.String(), "my.telegram.org") {
		t.Error("the MTProto pair is still hidden after the Bot API failed")
	}
}

// TestSettingAPasswordStoresAHashAndNotThePassword.
func TestSettingAPasswordStoresAHashAndNotThePassword(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"password": {"мой пароль"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	p, err := s.Store.PanelSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.PasswordHash, "мой пароль") {
		t.Fatal("the password is stored as itself")
	}
	if !VerifyPassword(p.PasswordHash, "мой пароль") {
		t.Error("the stored hash does not verify")
	}
}

// TestTickingTheLanBoxWithNoPasswordIsRefusedOnTheScreenToo.
//
// ListenAddress refuses it at the port, which is the guarantee. Refusing it
// here as well is what stops a person ticking the box, restarting, and finding
// the panel did not come up — with the reason in a log they do not read.
func TestTickingTheLanBoxWithNoPasswordIsRefusedOnTheScreenToo(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/settings", settingsForm(url.Values{"lan": {"1"}}))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("the LAN was opened with no password")
	}
	if !strings.Contains(rec.Body.String(), "парол") {
		t.Errorf("the refusal does not say what is missing: %s", rec.Body.String())
	}
	p, _ := s.Store.PanelSettings(context.Background())
	if p.LAN {
		t.Error("the LAN tick was stored anyway")
	}
}

// TestRetentionIsShownWithItsCurrentValuesRatherThanItsNames.
//
// The spec asks for exactly this. «Срок хранения тел» with no number is a
// setting nobody can tell they have already changed.
func TestRetentionIsShownWithItsCurrentValuesRatherThanItsNames(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	r, err := s.Store.StoredRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.BodyMaxAge = 7 * 24 * 60 * 60 * 1e9 // seven days
	if err := s.Store.SetStoredRetention(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(t, s, "/settings").Body.String(), `value="7"`) {
		t.Error("the current retention is not in the form")
	}
}

// TestQuietHoursAreStoredAsMinutes, so 22:30 survives being typed.
func TestQuietHoursAreStoredAsMinutes(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"quiet_from": {"22:30"}, "quiet_to": {"07:00"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	var from, to int
	if err := s.Store.DB().QueryRow(
		`SELECT quiet_from_min, quiet_to_min FROM settings WHERE id = 1`).
		Scan(&from, &to); err != nil {
		t.Fatal(err)
	}
	if from != 22*60+30 || to != 7*60 {
		t.Errorf("quiet hours = %d..%d, want 1350..420", from, to)
	}
}

// TestTheBlankTrailCheckDoesNotNeedALiveOne, which is what the field is for.
func TestTheBlankTrailCheckDoesNotNeedALiveOne(t *testing.T) {
	s := newServer(t)
	var sawKey string
	s.CheckBlankTrail = func(_ context.Context, url, key string) error {
		sawKey = key
		return nil
	}
	if err := s.Store.SetBlankTrailConfig(context.Background(),
		store.BlankTrailConfig{ControlURL: "http://127.0.0.1:8891",
			APIKey: secretKey, Ports: 2}); err != nil {
		t.Fatal(err)
	}
	rec := get(t, s, "/settings/blanktrail")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if sawKey != secretKey {
		t.Error("the check was not given the stored key")
	}
	if strings.Contains(rec.Body.String(), secretKey) {
		t.Error("the answer carries the key")
	}
}

// TestAFailedCheckSaysWhatWentWrongWithoutTheKeyInIt.
func TestAFailedCheckSaysWhatWentWrongWithoutTheKeyInIt(t *testing.T) {
	s := newServer(t)
	s.CheckBlankTrail = func(context.Context, string, string) error {
		return errors.New("ключ " + secretKey + " отклонён")
	}
	if err := s.Store.SetBlankTrailConfig(context.Background(),
		store.BlankTrailConfig{ControlURL: "http://x", APIKey: secretKey, Ports: 2}); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/settings/blanktrail").Body.String()
	if strings.Contains(body, secretKey) {
		t.Errorf("a key leaked through an error: %s", body)
	}
	if !strings.Contains(body, "отклонён") {
		t.Errorf("the reason was lost with the key: %s", body)
	}
}

// TestAutostartIsAskedRatherThanRemembered.
//
// An entry can be removed by anything. A checkbox showing a stored intention
// would tell somebody their monitor starts at login when it does not.
func TestAutostartIsAskedRatherThanRemembered(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetPanelSettings(context.Background(),
		store.PanelSettings{Autostart: true}); err != nil {
		t.Fatal(err)
	}
	asked := false
	s.Autostart = fakeAutostart{on: false, asked: &asked}
	body := get(t, s, "/settings").Body.String()
	if !asked {
		t.Error("the operating system was not asked")
	}
	if strings.Contains(body, `name="autostart" value="1" checked`) {
		t.Error("the box is ticked from the stored intention rather than from the truth")
	}
}

type fakeAutostart struct {
	on    bool
	asked *bool
}

func (f fakeAutostart) Enabled() (bool, error) { *f.asked = true; return f.on, nil }
func (f fakeAutostart) Enable() error          { return nil }
func (f fakeAutostart) Disable() error         { return nil }
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'Secret|MTProto|Password|Lan|Retention|Quiet|BlankTrail|Autostart' -v
```

Expected: FAIL — the stub renders «Этот экран ещё не построен».

- [ ] **Step 3: Delete the four stubs and write `internal/web/settings.go`**

Remove `settingsPage`, `saveSettings`, `checkBlankTrail` and `checkTelegram`
from `internal/web/stubs.go`, then:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Настройки: the screen decision 3 lands on, and the one every other decision
// quietly assumes.
//
// Three rules run through all of it.
//
// A secret already set is never rendered back — the page goes into a browser's
// history, into screenshots and into whatever a person pastes when they ask for
// help, so it says «задан» beside an empty box. An empty secret box therefore
// means «не менять» and not «стереть», or saving quiet hours would wipe the bot
// token and the person would find out when their alerts stopped; clearing one
// is its own tick. And no error from a check is shown without the credential
// being taken out of it first.

// botAPIFailed records that a Telegram check has failed on this run.
//
// The MTProto pair is offered only after it has. The schema says why: «a machine
// where the direct route works is never sent to my.telegram.org», and offering
// it up front asks somebody to register an application they will never need.
//
// Per process rather than stored: it is a fact about now, and a stored one would
// keep offering the pair long after the network came back.
var botAPIFailed atomic.Bool

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, problem string) {
	ctx := r.Context()

	mode, modeErr := s.Store.TransportMode(ctx)
	bt, err := s.Store.BlankTrailConfig(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tg, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	panel, err := s.Store.PanelSettings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ret, err := s.Store.StoredRetention(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	floor, err := s.Store.SpaceFloorBytes(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rpm, err := s.Store.RequestsPerMinute(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	quietFrom, quietTo, err := s.quietHours(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	if problem != "" {
		b.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}
	b.WriteString(`<form method="post" action="/settings">`)

	// — Transport
	b.WriteString(`<div class="card"><h2>Как выходить в сеть</h2>`)
	b.WriteString(`<label class="field"><span class="label">Способ</span><select name="mode">`)
	for _, o := range []struct{ v, label string }{
		{"direct", "Напрямую с этой машины"},
		{"blanktrail", "Через BlankTrail"},
	} {
		sel := ""
		if modeErr == nil && string(mode) == o.v {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, attr(o.v), sel, esc(o.label))
	}
	b.WriteString(`</select></label>`)
	b.WriteString(field("Адрес управления BlankTrail", "bt_url", bt.ControlURL, "text",
		"Например http://127.0.0.1:8891"))
	b.WriteString(secretField("Ключ BlankTrail", "bt_key", bt.APIKey != ""))
	b.WriteString(field("Портов", "bt_ports", strconv.Itoa(bt.Ports), "number",
		"Сколько портов открывать. Порты — это не адреса: один адрес остаётся "+
			"одним потолком, сколько бы портов ни было открыто."))
	b.WriteString(field("Запросов в минуту на адрес", "requests_per_minute",
		strconv.Itoa(rpm), "number",
		"Измеренный потолок — 6 (шестьдесят запросов за десять минут). "+
			"Больше поставить можно, но действовать будет меньшее из двух."))
	b.WriteString(`<p><button type="button" data-load="/settings/blanktrail" ` +
		`data-into="#bt-check">Проверить</button> <span id="bt-check"></span></p>`)
	b.WriteString(`</div>`)

	// — Telegram
	b.WriteString(`<div class="card"><h2>Телеграм</h2>`)
	b.WriteString(secretField("Токен бота", "telegram_token", tg.BotToken != ""))
	b.WriteString(`<p><button type="button" data-load="/settings/telegram" ` +
		`data-into="#tg-check">Проверить</button> <span id="tg-check"></span></p>`)
	if botAPIFailed.Load() || tg.APIID != 0 {
		b.WriteString(note("warning",
			"Bot API не отвечает. Через MTProto сообщения идут собственным "+
				"протоколом телеграма — для этого нужна пара с my.telegram.org."))
		b.WriteString(field("api_id (my.telegram.org)", "telegram_api_id",
			strconv.Itoa(tg.APIID), "number", ""))
		b.WriteString(secretField("api_hash (my.telegram.org)",
			"telegram_api_hash", tg.APIHash != ""))
	}
	b.WriteString(field("Тихие часы, с", "quiet_from", quietFrom, "text",
		"Например 22:30. Пусто — тихих часов нет."))
	b.WriteString(field("Тихие часы, до", "quiet_to", quietTo, "text", ""))
	b.WriteString(`</div>`)

	// — The panel itself
	b.WriteString(`<div class="card"><h2>Панель</h2>`)
	b.WriteString(secretField("Пароль", "password", panel.PasswordHash != ""))
	b.WriteString(checkbox("Открыть панель для локальной сети", "lan", panel.LAN,
		"Без своего пароля панель за пределы этой машины не выходит: здесь "+
			"лежат ключ прокси, токен бота и всё, что собрано."))
	on := panel.Autostart
	if s.Autostart != nil {
		// Asked, not remembered: an entry can be removed by anything, and a box
		// ticked from a stored intention would tell somebody their monitor
		// starts at login when it does not.
		if actual, err := s.Autostart.Enabled(); err == nil {
			on = actual
		}
	}
	b.WriteString(checkbox("Запускать при входе в систему", "autostart", on, ""))
	b.WriteString(`</div>`)

	// — Storage
	b.WriteString(`<div class="card"><h2>Хранение</h2>`)
	b.WriteString(field("Хранить тела страниц, дней", "retention_body_days",
		strconv.FormatInt(int64(ret.BodyMaxAge/(24*time.Hour)), 10), "number",
		"Архив ответов, по которым можно разобрать, что случилось."))
	b.WriteString(field("…и не больше, МБ", "retention_body_mb",
		strconv.FormatInt(ret.BodyMaxBytes>>20, 10), "number",
		"Ограничение по возрасту без ограничения по размеру на сломанной "+
			"установке не ограничивает ничего."))
	b.WriteString(field("Не собирать, если свободно меньше, ГБ", "space_floor_gb",
		strconv.FormatInt(floorGB(floor), 10), "number",
		"Действует большее из этого числа и двух процентов тома."))
	if v, err := s.Store.Volume(ctx); err == nil {
		fmt.Fprintf(&b, `<p class="note neutral">Сейчас база занимает %.1f МБ.</p>`,
			float64(v.FileBytes)/(1<<20))
	}
	b.WriteString(`</div>`)

	b.WriteString(`<p><button class="primary" type="submit">Сохранить</button></p>`)
	b.WriteString(`</form>`)

	s.render(w, r, page{Title: "Настройки", Body: rawHTML(b.String())})
}

// secretField is a credential's control: whether one is set, a box to replace
// it, and a tick to clear it.
//
// The value is never in the markup. «Задан» plus an empty box says the same
// thing without putting the key in the browser's history.
func secretField(label, name string, set bool) string {
	state := "не задан"
	if set {
		state = "задан"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<label class="field"><span class="label">%s — %s</span>`,
		esc(label), esc(state))
	fmt.Fprintf(&b, `<input class="input" type="password" name="%s" value="" `+
		`autocomplete="new-password">`, attr(name))
	b.WriteString(`<span class="hint">Пустое поле — оставить как есть.</span></label>`)
	if set {
		b.WriteString(checkbox("Стереть", name+"_clear", false, ""))
	}
	return b.String()
}

func floorGB(bytes int64) int64 { return bytes >> 30 }

func (s *Server) quietHours(ctx context.Context) (from, to string, err error) {
	f, t, err := s.Store.QuietHours(ctx)
	return minutesToClock(f), minutesToClock(t), err
}

func minutesToClock(m int) string {
	if m < 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// clockToMinutes reads «22:30». -1 means «not set», which is what the schema's
// default of -1 says and what an empty box means.
func clockToMinutes(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return -1, nil
	}
	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, fmt.Errorf("время %q не разобрать: нужно, например, 22:30", v)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("время %q не разобрать: нужно, например, 22:30", v)
	}
	return hh*60 + mm, nil
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var bad []string

	mode, err := parseMode(r.FormValue("mode"))
	if err != nil {
		bad = append(bad, err.Error())
	}

	// The panel half first, because the LAN refusal depends on the password
	// that may be arriving in this same post.
	panel, err := s.Store.PanelSettings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.FormValue("password_clear") == "1" {
		panel.PasswordHash = ""
	} else if v := r.FormValue("password"); v != "" {
		hash, err := HashPassword(v)
		if err != nil {
			bad = append(bad, err.Error())
		} else {
			panel.PasswordHash = hash
		}
	}
	panel.LAN = r.FormValue("lan") == "1"
	panel.Autostart = r.FormValue("autostart") == "1"
	if panel.LAN && panel.PasswordHash == "" {
		// Refused here as well as at the port. ListenAddress is the guarantee;
		// this is what stops a person ticking the box, restarting, and finding
		// the panel did not come up — with the reason in a log nobody reads.
		bad = append(bad,
			"чтобы открыть панель для сети, сначала задайте пароль")
	}

	quietFrom, err := clockToMinutes(r.FormValue("quiet_from"))
	if err != nil {
		bad = append(bad, err.Error())
	}
	quietTo, err := clockToMinutes(r.FormValue("quiet_to"))
	if err != nil {
		bad = append(bad, err.Error())
	}

	if len(bad) > 0 {
		s.renderSettings(w, r, strings.Join(bad, "; "))
		return
	}

	if err := s.Store.SetTransportMode(ctx, mode); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	if err := s.saveBlankTrail(ctx, r); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	if err := s.saveTelegram(ctx, r); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	if err := s.Store.SetPanelSettings(ctx, panel); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	if err := s.applyAutostart(panel.Autostart); err != nil {
		s.renderSettings(w, r, "Автозапуск не удалось изменить: "+err.Error())
		return
	}
	if err := s.Store.SetQuietHours(ctx, quietFrom, quietTo); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	if err := s.saveStorage(ctx, r); err != nil {
		s.renderSettings(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// secretFrom applies the three-way rule: clear, replace, or leave alone.
func secretFrom(r *http.Request, name, current string) string {
	if r.FormValue(name+"_clear") == "1" {
		return ""
	}
	if v := strings.TrimSpace(r.FormValue(name)); v != "" {
		return v
	}
	return current
}

func (s *Server) saveBlankTrail(ctx context.Context, r *http.Request) error {
	cur, err := s.Store.BlankTrailConfig(ctx)
	if err != nil {
		return err
	}
	cur.ControlURL = strings.TrimSpace(r.FormValue("bt_url"))
	cur.APIKey = secretFrom(r, "bt_key", cur.APIKey)
	if v, err := strconv.Atoi(r.FormValue("bt_ports")); err == nil && v > 0 {
		cur.Ports = v
	}
	if err := s.Store.SetBlankTrailConfig(ctx, cur); err != nil {
		return err
	}
	if v, err := strconv.Atoi(r.FormValue("requests_per_minute")); err == nil && v > 0 {
		return s.Store.SetRequestsPerMinute(ctx, v)
	}
	return nil
}

func (s *Server) saveTelegram(ctx context.Context, r *http.Request) error {
	cur, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		return err
	}
	cur.BotToken = secretFrom(r, "telegram_token", cur.BotToken)
	cur.APIHash = secretFrom(r, "telegram_api_hash", cur.APIHash)
	if v, err := strconv.Atoi(r.FormValue("telegram_api_id")); err == nil {
		cur.APIID = v
	}
	return s.Store.SetTelegramConfig(ctx, cur)
}

func (s *Server) saveStorage(ctx context.Context, r *http.Request) error {
	ret, err := s.Store.StoredRetention(ctx)
	if err != nil {
		return err
	}
	if v, err := strconv.Atoi(r.FormValue("retention_body_days")); err == nil && v > 0 {
		ret.BodyMaxAge = time.Duration(v) * 24 * time.Hour
	}
	if v, err := strconv.ParseInt(r.FormValue("retention_body_mb"), 10, 64); err == nil && v > 0 {
		ret.BodyMaxBytes = v << 20
	}
	if err := s.Store.SetStoredRetention(ctx, ret); err != nil {
		return err
	}
	s.Store.SetRetention(ret)

	if v, err := strconv.ParseInt(r.FormValue("space_floor_gb"), 10, 64); err == nil && v > 0 {
		if err := s.Store.SetSpaceFloorBytes(ctx, v<<30); err != nil {
			return err
		}
		s.Store.SetSpaceFloor(v << 30)
	}
	return nil
}

func (s *Server) applyAutostart(want bool) error {
	if s.Autostart == nil {
		return nil
	}
	on, err := s.Autostart.Enabled()
	if err != nil {
		return err
	}
	switch {
	case want && !on:
		return s.Autostart.Enable()
	case !want && on:
		return s.Autostart.Disable()
	}
	return nil
}

// checkBlankTrail asks the control API who it is, and answers with a fragment.
func (s *Server) checkBlankTrail(w http.ResponseWriter, r *http.Request) {
	if s.CheckBlankTrail == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет проверять BlankTrail."))
		return
	}
	cfg, err := s.Store.BlankTrailConfig(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	if cfg.APIKey == "" || cfg.ControlURL == "" {
		writeFragment(w, note("warning", "Не заданы адрес управления или ключ."))
		return
	}
	if err := s.CheckBlankTrail(r.Context(), cfg.ControlURL, cfg.APIKey); err != nil {
		// The key is taken out of whatever came back. An SDK that quoted the
		// request quoted the key, and this fragment goes on a screen.
		writeFragment(w, note("bad", "Не отвечает: "+redactSecret(err.Error(), cfg.APIKey)))
		return
	}
	writeFragment(w, note("neutral", "Отвечает."))
}

func (s *Server) checkTelegram(w http.ResponseWriter, r *http.Request) {
	if s.CheckTelegram == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет проверять телеграм."))
		return
	}
	cfg, err := s.Store.TelegramConfig(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	if cfg.BotToken == "" {
		writeFragment(w, note("warning", "Не задан токен бота."))
		return
	}
	name, err := s.CheckTelegram(r.Context(), cfg.BotToken)
	if err != nil {
		botAPIFailed.Store(true)
		writeFragment(w, note("bad",
			"Не отвечает: "+redactSecret(err.Error(), cfg.BotToken)+
				" Перезагрузите страницу — появятся поля для MTProto."))
		return
	}
	writeFragment(w, note("neutral", "Отвечает: @"+name))
}

// redactSecret takes a credential out of a message that may quote it.
//
// Belt and braces over internal/telegram's own redaction: this fragment goes
// onto a screen, and the one error nobody anticipated is the one that carries
// the key.
func redactSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "<ключ>")
}

func writeFragment(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

var _ = errors.New // kept if the import lands unused after edits
```

Delete the `var _ = errors.New` line and its import if `go vet` flags it.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go vet ./internal/web/ && \
  go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS. `TestRetentionIsShownWithItsCurrentValuesRatherThanItsNames`
sets `BodyMaxAge` with a raw nanosecond literal — if that reads badly, change it
to `7 * 24 * time.Hour` and add the `time` import; the assertion is the same.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): Настройки, with no secret ever rendered back"
```

---

## Task 10: Прокси

**Files:**
- Create: `internal/web/channels.go`, `internal/web/channels_test.go`
- Modify: `internal/web/stubs.go` (delete the five channel handlers)

The spec calls this «the screen the pool is worthless without». Four kinds of
entry — a list from a file or a URL, a rotating address with its change link, a
BlankTrail gateway, and the host's own address — in any mix; ports are spread
over whatever is ticked, and an entry that starts producing blocks loses weight
on its own through `blanktrail.Mixer`.

**Two things this screen must state rather than leave to be inferred.**

- **N addresses is N times the ceiling, and one address is one ceiling however
  many ports are open.** This is the sentence that answers «I added ports and it
  got banned faster».
- **A rotating channel's change link is never pulled by a test.** The provider
  limits how often it may be, and a test that broke what it was testing would be
  worse than no test at all.

**Interfaces:**
- Consumes: `store.Channels`, `store.SaveChannel`, `store.DeleteChannel`, `store.ReplaceProxies`, `store.CountProxies` (Task 3); `blanktrail.Parse`, `blanktrail.Source`; `s.Gateways`, `s.CheckChannel`; `addressCount`, `BudgetFor` (Tasks 7–8).
- Produces: `channelsPage`, `saveChannel`, `deleteChannel`, `testChannel`, `listGateways`.

- [ ] **Step 1: Write the failing test**

Create `internal/web/channels_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// TestAListIsReadAndCountedWithTheFirstBadLineQuoted.
//
// WB_GO's rule about what a free test means, kept: a list is read and counted,
// and the first line that would not parse is shown. «12 адресов, 1 строка не
// разобрана» without saying which line leaves somebody scrolling a file.
func TestAListIsReadAndCountedWithTheFirstBadLineQuoted(t *testing.T) {
	s := newServer(t)
	body := "10.0.0.1:1080\nне адрес\n10.0.0.2:1080\n"
	rec := post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"}, "list": {body},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /channels = %d: %s", rec.Code, rec.Body.String())
	}
	page := get(t, s, "/channels").Body.String()
	if !strings.Contains(page, "2") {
		t.Error("the address count is not shown")
	}
	if !strings.Contains(page, "не адрес") {
		t.Error("the bad line is not quoted")
	}
}

// TestTheScreenStatesWhatTheMixMeans.
//
// Not left to be inferred: this is the sentence that answers «я добавил порты, и
// забанило быстрее».
func TestTheScreenStatesWhatTheMixMeans(t *testing.T) {
	s := newServer(t)
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"10.0.0.1:1080\n10.0.0.2:1080\n10.0.0.3:1080\n"},
	})
	page := get(t, s, "/channels").Body.String()
	for _, want := range []string{"3", "потолок", "порт"} {
		if !strings.Contains(page, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestARotatingChannelsChangeLinkIsNeverPulledByATest.
//
// The provider limits how often it may be pulled, and a test that broke what it
// was testing would be worse than no test at all.
func TestARotatingChannelsChangeLinkIsNeverPulledByATest(t *testing.T) {
	s := newServer(t)
	pulled := false
	s.CheckChannel = func(context.Context, int64) (string, error) {
		pulled = true
		return "", nil
	}
	rec := post(t, s, "/channels", url.Values{
		"kind": {"rotating"}, "name": {"ротация"},
		"rotate_url": {"https://provider.example/change?token=x"},
		"upstream":   {"10.0.0.9:1080"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	chs, err := s.Store.Channels(context.Background())
	if err != nil || len(chs) != 1 {
		t.Fatalf("channels = %+v (%v)", chs, err)
	}
	frag := get(t, s, "/channels/test?id="+strconv.FormatInt(chs[0].ID, 10))
	if pulled {
		t.Error("the change link was pulled by a test")
	}
	if !strings.Contains(frag.Body.String(), "не проверяется") {
		t.Errorf("the fragment does not say why: %s", frag.Body.String())
	}
}

// TestAGatewayIsPickedFromWhatExists, not typed from memory.
func TestAGatewayIsPickedFromWhatExists(t *testing.T) {
	s := newServer(t)
	s.Gateways = func(context.Context) (blanktrail.GatewayList, error) {
		return blanktrail.GatewayList{Available: []blanktrail.Gateway{{Name: "ru-msk"}}}, nil
	}
	frag := get(t, s, "/channels/gateways")
	if !strings.Contains(frag.Body.String(), "ru-msk") {
		t.Errorf("the gateway list is not offered: %s", frag.Body.String())
	}
}

// TestWithNoLicenceTheGatewayListSaysSoRatherThanBeingEmpty.
func TestWithNoLicenceTheGatewayListSaysSoRatherThanBeingEmpty(t *testing.T) {
	s := newServer(t)
	s.Gateways = func(context.Context) (blanktrail.GatewayList, error) {
		return blanktrail.GatewayList{Reason: "нет действующей лицензии"}, nil
	}
	if !strings.Contains(get(t, s, "/channels/gateways").Body.String(), "лицензии") {
		t.Error("the reason is not shown")
	}
}

// TestAChannelCredentialIsNotRenderedBack. A proxy password is a credential
// like any other, and this page is one somebody screenshots.
func TestAChannelCredentialIsNotRenderedBack(t *testing.T) {
	s := newServer(t)
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"с паролем"},
		"list": {"socks5://user:s3cr3t@10.0.0.1:1080\n"},
	})
	page := get(t, s, "/channels").Body.String()
	if strings.Contains(page, "s3cr3t") {
		t.Error("a proxy password is on the page")
	}
	if !strings.Contains(page, "10.0.0.1") {
		t.Error("the address is not shown at all")
	}
}

// TestRefreshingAListReplacesItRatherThanAddingToIt.
func TestRefreshingAListReplacesItRatherThanAddingToIt(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"10.0.0.1:1080\n10.0.0.2:1080\n"},
	})
	chs, _ := s.Store.Channels(ctx)
	id := strconv.FormatInt(chs[0].ID, 10)

	post(t, s, "/channels", url.Values{
		"id": {id}, "kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"10.0.0.3:1080\n"},
	})
	ps, err := s.Store.Proxies(ctx, chs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Host != "10.0.0.3" {
		t.Errorf("after the refresh: %+v", ps)
	}
}

// TestDeletingAChannelAsksAndThenChangesTheStatedCeiling.
func TestDeletingAChannelAsksAndThenChangesTheStatedCeiling(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"10.0.0.1:1080\n10.0.0.2:1080\n10.0.0.3:1080\n"},
	})
	if !strings.Contains(get(t, s, "/channels").Body.String(), "data-confirm") {
		t.Error("the delete button does not ask")
	}
	chs, _ := s.Store.Channels(ctx)
	post(t, s, "/channels/delete", url.Values{"id": {strconv.FormatInt(chs[0].ID, 10)}})

	n, err := s.addressCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("addresses = %d after deleting the only list, want the host's own", n)
	}
}

// TestATestThatFailsSaysWhyWithoutTheCredential.
func TestATestThatFailsSaysWhyWithoutTheCredential(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"socks5://user:s3cr3t@10.0.0.1:1080\n"},
	})
	chs, _ := s.Store.Channels(ctx)
	s.CheckChannel = func(context.Context, int64) (string, error) {
		return "", errors.New("socks5://user:s3cr3t@10.0.0.1:1080 не отвечает")
	}
	body := get(t, s, "/channels/test?id="+strconv.FormatInt(chs[0].ID, 10)).Body.String()
	if strings.Contains(body, "s3cr3t") {
		t.Errorf("a credential leaked through an error: %s", body)
	}
	if !strings.Contains(body, "не отвечает") {
		t.Errorf("the reason was lost with the credential: %s", body)
	}
}

// TestTheHostsOwnAddressIsAChannelLikeAnyOther, so a mix of direct and proxied
// is expressible — which is what «in any mix» in the spec means.
func TestTheHostsOwnAddressIsAChannelLikeAnyOther(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	post(t, s, "/channels", url.Values{"kind": {"direct"}, "name": {"эта машина"}})
	post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"list": {"10.0.0.1:1080\n10.0.0.2:1080\n"},
	})
	n, err := s.addressCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("addresses = %d, want two proxied plus this machine", n)
	}
	_ = store.Channel{}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'List|Mix|Rotating|Gateway|Channel|Refreshing|Host' -v
```

Expected: FAIL — the stub renders «Этот экран ещё не построен».

- [ ] **Step 3: Delete the five stubs and write `internal/web/channels.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Прокси: where the requests go out from, and the screen the pool is worthless
// without.
//
// Four kinds in any mix — a list from a file or a URL, a rotating address with
// its change link, a BlankTrail gateway, and this machine's own address. Ports
// are spread over whatever is ticked, and an entry that starts producing blocks
// loses weight on its own through blanktrail.Mixer.
//
// Each entry has a test that costs nothing, and what «nothing» means is the
// donor's reasoning kept exactly. A list is read and counted with the first bad
// line quoted. A gateway name is checked against the ones BlankTrail actually
// has. A rotating channel's change link is deliberately NOT pulled — the
// provider limits how often it may be, and a test that broke what it was testing
// would be worse than no test at all.

func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) {
	s.renderChannels(w, r, "")
}

func (s *Server) renderChannels(w http.ResponseWriter, r *http.Request, problem string) {
	ctx := r.Context()
	channels, err := s.Store.Channels(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	counts, err := s.Store.CountProxies(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	addresses, err := s.addressCount(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rpm, err := s.Store.RequestsPerMinute(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b := BudgetFor(rpm, addresses, nil, 0)

	var body strings.Builder

	// The consequence of the mix, stated rather than left to be inferred.
	body.WriteString(`<div class="card">`)
	fmt.Fprintf(&body, `<p class="sum">Настроено <strong>%d</strong> %s — `+
		`потолок <strong>%s</strong> запр./мин.</p>`,
		addresses, esc(ru.Plural(addresses, "адрес", "адреса", "адресов")),
		esc(num(b.CapacityPerMin)))
	body.WriteString(note("neutral",
		"Сколько адресов — во столько раз выше потолок. Один адрес остаётся "+
			"одним потолком, сколько бы портов на него ни было открыто: порты — "+
			"это не адреса, и лишние порты только доводят до предела быстрее."))
	body.WriteString(`</div>`)

	if problem != "" {
		body.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}

	for _, c := range channels {
		body.WriteString(s.channelCard(c, counts[c.ID]))
	}
	body.WriteString(channelForm())

	s.render(w, r, page{Title: "Прокси", Body: rawHTML(body.String())})
}

func (s *Server) channelCard(c store.Channel, addresses int) string {
	id := strconv.FormatInt(c.ID, 10)
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h2>%s <span class="pill %s">%s</span></h2>`,
		esc(c.Name), map[bool]string{true: "ok", false: "warn"}[c.Enabled],
		esc(kindLabel(c.Kind)))

	switch c.Kind {
	case "proxy-list":
		fmt.Fprintf(&b, `<p>%d %s.</p>`, addresses,
			esc(ru.Plural(addresses, "адрес", "адреса", "адресов")))
	case "rotating":
		b.WriteString(`<p>Один адрес за раз, меняется по ссылке провайдера.</p>`)
	case "gateway":
		fmt.Fprintf(&b, `<p>Шлюз BlankTrail: %s</p>`, esc(c.Source))
	case "direct":
		b.WriteString(`<p>Собственный адрес этой машины.</p>`)
	}
	// The addresses themselves, host and port only. A proxy password is a
	// credential like any other and this page is one somebody screenshots.
	if c.Kind == "proxy-list" && c.Source != "" {
		fmt.Fprintf(&b, `<p class="hint">Источник: %s</p>`, esc(c.Source))
	}

	fmt.Fprintf(&b, `<p><button type="button" data-load="/channels/test?id=%s" `+
		`data-into="#test-%s">Проверить</button> <span id="test-%s"></span></p>`,
		attr(id), attr(id), attr(id))
	fmt.Fprintf(&b, `<form method="post" action="/channels/delete" `+
		`data-confirm="Удалить «%s»? Потолок запросов станет ниже.">`+
		`<input type="hidden" name="id" value="%s">`+
		`<button class="danger" type="submit">Удалить</button></form>`,
		attr(c.Name), attr(id))
	b.WriteString(`</div>`)
	return b.String()
}

func kindLabel(kind string) string {
	switch kind {
	case "proxy-list":
		return "список"
	case "rotating":
		return "ротация"
	case "gateway":
		return "шлюз"
	case "direct":
		return "эта машина"
	}
	return kind
}

func channelForm() string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Добавить выход</h2>`)
	b.WriteString(`<form method="post" action="/channels">`)
	b.WriteString(field("Название", "name", "", "text", ""))
	b.WriteString(`<label class="field"><span class="label">Вид</span>` +
		`<select name="kind">` +
		`<option value="proxy-list">Список адресов</option>` +
		`<option value="rotating">Ротация по ссылке</option>` +
		`<option value="gateway">Шлюз BlankTrail</option>` +
		`<option value="direct">Собственный адрес этой машины</option>` +
		`</select></label>`)
	b.WriteString(`<label class="field"><span class="label">Список адресов</span>` +
		`<textarea class="input" name="list" rows="6" ` +
		`placeholder="socks5://user:pass@10.0.0.1:1080&#10;10.0.0.2:1080"></textarea>` +
		`<span class="hint">По одному в строке. Схема по умолчанию — socks5. ` +
		`Можно вместо этого указать ссылку на файл ниже.</span></label>`)
	b.WriteString(field("Или ссылка на список", "source", "", "text",
		"http(s):// — список будет перечитываться."))
	b.WriteString(field("Адрес для ротации", "upstream", "", "text",
		"Один адрес, который провайдер меняет по ссылке."))
	b.WriteString(field("Ссылка смены адреса", "rotate_url", "", "text",
		"Эта ссылка не дёргается при проверке: провайдеры ограничивают, "+
			"как часто её можно вызывать."))
	b.WriteString(`<p><button type="button" data-load="/channels/gateways" ` +
		`data-into="#gateways">Показать шлюзы BlankTrail</button> ` +
		`<span id="gateways"></span></p>`)
	b.WriteString(field("Имя шлюза", "gateway", "", "text", ""))
	b.WriteString(`<button class="primary" type="submit">Добавить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind := r.FormValue("kind")
	c := store.Channel{
		Name:      strings.TrimSpace(r.FormValue("name")),
		Kind:      kind,
		Source:    strings.TrimSpace(r.FormValue("source")),
		RotateURL: strings.TrimSpace(r.FormValue("rotate_url")),
		Weight:    100,
		Enabled:   true,
	}
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		c.ID = id
	}
	if c.Name == "" {
		s.renderChannels(w, r, "не задано название")
		return
	}

	var parsed []blanktrail.Upstream
	var bad []string
	switch kind {
	case "proxy-list":
		parsed, bad = blanktrail.Parse(r.FormValue("list"), "socks5")
		if len(parsed) == 0 && c.Source == "" {
			s.renderChannels(w, r, listProblem(nil, bad))
			return
		}
	case "rotating":
		parsed, bad = blanktrail.Parse(r.FormValue("upstream"), "socks5")
		if len(parsed) != 1 {
			s.renderChannels(w, r, "для ротации нужен ровно один адрес")
			return
		}
		if c.RotateURL == "" {
			s.renderChannels(w, r, "не задана ссылка смены адреса")
			return
		}
	case "gateway":
		c.Source = strings.TrimSpace(r.FormValue("gateway"))
		if c.Source == "" {
			s.renderChannels(w, r, "не выбран шлюз")
			return
		}
	case "direct":
		// Nothing to parse: this machine's own address is one address.
	default:
		s.renderChannels(w, r, fmt.Sprintf("вид %q эта сборка не знает", kind))
		return
	}

	id, err := s.Store.SaveChannel(ctx, c)
	if err != nil {
		s.renderChannels(w, r, err.Error())
		return
	}
	if len(parsed) > 0 {
		ps := make([]store.Proxy, 0, len(parsed))
		for _, u := range parsed {
			port, _ := strconv.Atoi(u.Port)
			ps = append(ps, store.Proxy{
				ChannelID: id, Scheme: u.Scheme, Host: u.Host, Port: port,
				Username: u.Username, Password: u.Password, Enabled: true,
			})
		}
		if err := s.Store.ReplaceProxies(ctx, id, ps); err != nil {
			s.renderChannels(w, r, err.Error())
			return
		}
	}
	// The count and the first unreadable line are kept for the next render, so
	// a person who pasted a file with one broken line is told which one rather
	// than being left to scroll it.
	s.lastList.Store(&listReport{ChannelID: id, Good: len(parsed), Bad: bad})
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

// listProblem is what a list that produced nothing usable says.
func listProblem(good []blanktrail.Upstream, bad []string) string {
	if len(bad) == 0 {
		return "список пуст"
	}
	return fmt.Sprintf("ни одного адреса разобрать не удалось; первая "+
		"нечитаемая строка: %q", bad[0])
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого выхода", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteChannel(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

// testChannel is the per-entry test that costs nothing.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeFragment(w, note("bad", "нет такого выхода"))
		return
	}
	channels, err := s.Store.Channels(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	var c store.Channel
	for _, ch := range channels {
		if ch.ID == id {
			c = ch
		}
	}
	if c.ID == 0 {
		writeFragment(w, note("bad", "нет такого выхода"))
		return
	}

	if c.Kind == "rotating" {
		// Deliberately nothing. The provider limits how often the change link
		// may be pulled, and a test that broke what it was testing would be
		// worse than no test at all.
		writeFragment(w, note("neutral",
			"Ссылка смены адреса не проверяется: провайдеры ограничивают, "+
				"как часто её можно вызывать, и проверка потратила бы одну "+
				"из этих попыток."))
		return
	}
	if s.CheckChannel == nil {
		counts, _ := s.Store.CountProxies(r.Context())
		writeFragment(w, note("neutral", fmt.Sprintf("%d %s в списке.",
			counts[id], ru.Plural(counts[id], "адрес", "адреса", "адресов"))))
		return
	}
	summary, err := s.CheckChannel(r.Context(), id)
	if err != nil {
		writeFragment(w, note("bad", s.redactChannel(r, id, err.Error())))
		return
	}
	writeFragment(w, note("neutral", summary))
}

// redactChannel takes every stored credential of this channel out of a message.
//
// The engine's error may quote the URL it dialled, and the URL carries the
// password. This fragment goes onto a screen.
func (s *Server) redactChannel(r *http.Request, id int64, msg string) string {
	ps, err := s.Store.Proxies(r.Context(), id)
	if err != nil {
		return msg
	}
	for _, p := range ps {
		if p.Password != "" {
			msg = strings.ReplaceAll(msg, p.Password, "<пароль>")
		}
	}
	return msg
}

// listGateways offers what BlankTrail actually has configured, so a gateway is
// picked rather than typed from memory.
func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) {
	if s.Gateways == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет спрашивать шлюзы."))
		return
	}
	list, err := s.Gateways(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	if len(list.Available) == 0 {
		reason := list.Reason
		if reason == "" {
			reason = "ни одного шлюза не настроено"
		}
		writeFragment(w, note("warning", reason))
		return
	}
	var b strings.Builder
	b.WriteString(`<span>`)
	for i, g := range list.Available {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(esc(g.Name))
	}
	b.WriteString(`</span>`)
	writeFragment(w, b.String())
}
```

`s.lastList` is one field and one type on `Server`, so the bad line survives the
redirect. Add to `server.go`:

```go
// listReport is what the last saved proxy list parsed to, held for the redirect
// that follows the save.
//
// One value and not a map: a person saves one list at a time, and a map keyed by
// channel would be a cache with no eviction on a long-running process.
type listReport struct {
	ChannelID int64
	Good      int
	Bad       []string
}
```

and the field `lastList atomic.Pointer[listReport]`, with `"sync/atomic"` in
the imports. `renderChannels` reads it once and shows the first bad line beside
the matching card:

```go
	if rep := s.lastList.Load(); rep != nil && rep.ChannelID == c.ID && len(rep.Bad) > 0 {
		fmt.Fprintf(&b, `<p class="note warning">%d %s не разобрано, первая: %s</p>`,
			len(rep.Bad), esc(ru.Plural(len(rep.Bad), "строка", "строки", "строк")),
			esc(rep.Bad[0]))
	}
```

Put that inside `channelCard`, which therefore takes `s` as a receiver — it
already does.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go vet ./internal/web/ && \
  go test ./internal/web/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/web && git commit -m "feat(web): Прокси, and the sentence that answers why more ports made it worse"
```

---

## Task 11: `internal/autostart`, `avitomon panel`, and the whole thing end to end

**Files:**
- Create: `internal/autostart/autostart.go`, `autostart_test.go`, `unix.go`, `unix_test.go`, `windows.go`, `windows_test.go`, `cmd/avitomon/panel.go`
- Modify: `cmd/avitomon/main.go` (register `panel`, add it to `usage`)

`internal/autostart` is a straight lift from the donor's, 530 lines including
tests, with the module path and the binary's name changed. Its shape:
`Command{Path, Args}`, `Self(args ...string) (Command, error)`, and per-platform
`Enable(Command) error`, `Disable() error`, `Enabled() (bool, error)` — a
registry value under `HKCU\…\Run` on Windows, a `.desktop` file or a LaunchAgent
plist elsewhere.

**Interfaces:**
- Consumes: everything above.
- Produces: `func panelCmd(args []string) error`, and an `Autostart` implementation satisfying `web.Autostart`.

- [ ] **Step 1: Lift `internal/autostart`**

```bash
cd S:/Projects/Avito_Go && mkdir -p internal/autostart && \
  cp S:/Projects/WB_GO/internal/autostart/*.go internal/autostart/ && \
  sed -i 's|wildberries-monitor|avito-monitor|g' internal/autostart/*.go && \
  gofmt -w ./internal/autostart && go test ./internal/autostart/
```

Then read every file and change what is about the other product rather than
about autostart: the entry name, the `.desktop` `Name=`, the LaunchAgent label,
and any comment naming Wildberries. The rules the package enforces —
`Validate` refusing a relative path or one that is not there — are about
autostart and are kept unchanged.

Expected: PASS. If `windows_test.go` writes to a real registry key, keep it —
the donor's writes under `HKCU` and removes what it wrote, which is a test that
costs a registry round trip and proves the one thing that matters.

- [ ] **Step 2: Write the failing test for the command**

Create `cmd/avitomon/panel_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/web"
)

func openPanelStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestTheWiringSuppliesEveryCheckTheScreensNeed.
//
// The Server's function fields default to nil, and a nil one is a button that
// answers «эта сборка не умеет». That is right for a test and wrong for the
// product, so this asserts the wiring fills them in.
func TestTheWiringSuppliesEveryCheckTheScreensNeed(t *testing.T) {
	s := buildPanel(openPanelStore(t))
	if s.CheckBlankTrail == nil {
		t.Error("CheckBlankTrail is not wired")
	}
	if s.CheckTelegram == nil {
		t.Error("CheckTelegram is not wired")
	}
	if s.Gateways == nil {
		t.Error("Gateways is not wired")
	}
	if s.CheckChannel == nil {
		t.Error("CheckChannel is not wired")
	}
	if s.Autostart == nil {
		t.Error("Autostart is not wired")
	}
}

// TestThePanelRefusesTheLanBeforeItOpensAPort.
//
// The one thing this command must not get wrong: the refusal has to happen
// before Listen, not after.
func TestThePanelRefusesTheLanBeforeItOpensAPort(t *testing.T) {
	st := openPanelStore(t)
	ctx := context.Background()
	if err := st.SetPanelSettings(ctx, store.PanelSettings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	s := buildPanel(st)
	if _, err := s.ListenAddress(ctx, 8080, true); !errors.Is(err, web.ErrWouldExposeUnprotectedPanel) {
		t.Errorf("err = %v, want the refusal", err)
	}
}
```

- [ ] **Step 3: Write `cmd/avitomon/panel.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/autostart"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/telegram"
	"github.com/BlankTrail/avito-monitor/internal/web"
)

func panelCmd(args []string) error {
	fs := flag.NewFlagSet("panel", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "файл базы")
	port := fs.Int("port", 8765, "порт")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	srv := buildPanel(s)

	panel, err := s.PanelSettings(ctx)
	if err != nil {
		return err
	}
	addr, err := srv.ListenAddress(ctx, *port, panel.LAN)
	if err != nil {
		// Named rather than logged: somebody who ticked the box and restarted
		// needs to know why nothing came up, on the terminal they are looking at.
		return err
	}

	// Retention and the free-space floor are settings, and the process has to be
	// told them: store.SetRetention holds them in memory, and nothing reads the
	// columns on its own.
	if r, err := s.StoredRetention(ctx); err == nil {
		s.SetRetention(r)
	}
	if floor, err := s.SpaceFloorBytes(ctx); err == nil && floor > 0 {
		s.SetSpaceFloor(floor)
	}

	if panel.PasswordHash == "" && addr[0] == '1' {
		fmt.Printf("Панель на http://%s — без пароля, только с этой машины.\n", addr)
	} else {
		fmt.Printf("Панель на http://%s\n", addr)
	}
	if _, err := s.TransportMode(ctx); errors.Is(err, store.ErrNoTransportChosen) {
		fmt.Println("Способ выхода в сеть ещё не выбран — сбор не идёт. " +
			"Откройте панель и ответьте на первый вопрос.")
	}

	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return hs.ListenAndServe()
}

// buildPanel wires the live third parties the screens need.
//
// Every one is a field on the Server rather than a call inside internal/web, so
// that package's tests need no network, no licensed proxy and no registry. This
// function is where the product differs from the suite, and it is deliberately
// the only place.
func buildPanel(s *store.Store) *web.Server {
	srv := &web.Server{Store: s, Now: time.Now}

	srv.CheckBlankTrail = func(ctx context.Context, url, apiKey string) error {
		c, err := blanktrail.NewClient(url, apiKey)
		if err != nil {
			return err
		}
		return c.Health(ctx)
	}

	srv.CheckTelegram = func(ctx context.Context, token string) (string, error) {
		return telegram.NewBot(token, nil).GetMe(ctx)
	}

	srv.Gateways = func(ctx context.Context) (blanktrail.GatewayList, error) {
		cfg, err := s.BlankTrailConfig(ctx)
		if err != nil {
			return blanktrail.GatewayList{}, err
		}
		c, err := blanktrail.NewClient(cfg.ControlURL, cfg.APIKey)
		if err != nil {
			return blanktrail.GatewayList{}, err
		}
		return c.Gateways(ctx)
	}

	// The list is read and counted, and nothing is dialled. Dialling every
	// address of a thousand-line list from a settings screen is a thousand
	// connections a person did not ask for; the pool finds out which ones work
	// by using them, and Mixer demotes the ones that do not.
	srv.CheckChannel = func(ctx context.Context, id int64) (string, error) {
		ps, err := s.Proxies(ctx, id)
		if err != nil {
			return "", err
		}
		if len(ps) == 0 {
			return "", errors.New("в этом выходе нет ни одного адреса")
		}
		return fmt.Sprintf("%d адресов, первый %s:%d", len(ps), ps[0].Host, ps[0].Port), nil
	}

	srv.Autostart = osAutostart{}
	return srv
}

// osAutostart is this platform's start-at-login entry, as internal/web wants it.
type osAutostart struct{}

func (osAutostart) Enabled() (bool, error) { return autostart.Enabled() }

func (osAutostart) Enable() error {
	// The panel command, not the bare binary: an entry that started the process
	// with no arguments would start something that prints usage and exits.
	c, err := autostart.Self("panel")
	if err != nil {
		return err
	}
	return autostart.Enable(c)
}

func (osAutostart) Disable() error { return autostart.Disable() }

var _ = os.Getenv // remove if unused after edits
```

Check `blanktrail.NewClient`'s real signature before writing this — if it is
`NewClient(base, key string) (*Client, error)`, the above is right; if it takes
options, pass none. Run `go doc ./blanktrail Client` to see.

- [ ] **Step 4: Register the command**

In `cmd/avitomon/main.go`, add to the switch:

```go
	case "panel":
		err = panelCmd(os.Args[2:])
```

and to `usage`:

```go
	fmt.Fprintln(os.Stderr, "  avitomon panel [-db avito.db] [-port 8765]")
```

- [ ] **Step 5: Run everything**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./cmd ./internal && go vet ./... && \
  go build ./... && go test ./... 2>&1 | grep -v "no test files"
```

Expected: `gofmt -l` prints nothing, vet is clean, every package passes.

- [ ] **Step 6: The demonstrable, by hand**

This is the increment's own claim for 6A, and it is checked by a person because
what it asserts is that a person can do it.

```bash
cd S:/Projects/Avito_Go && go run ./cmd/avitomon panel -db /tmp/panel-demo.db
```

Then, in a browser at `http://127.0.0.1:8765`:

1. The banner says nothing collects until a transport is chosen. Follow it.
2. The first screen offers two options, neither pre-selected, with the soak's
   numbers and its caveat. Choose «напрямую».
3. On Наблюдения, add `https://m.avito.ru/all?q=iphone+15` with an interval of
   300 seconds. It is accepted, and the budget line moves from 0 to 0,2.
4. Add the same watch eleven more times, then once more. The last is refused,
   and the refusal shows the sum: what is spent, what this one asks for, and the
   ceiling with its arithmetic.
5. Try an interval of 30 seconds. Refused, saying Avito returns the same page
   byte for byte inside a minute.
6. On Прокси, paste three addresses with one broken line. The screen says three
   addresses and quotes the broken line, and the stated ceiling triples.
7. On Настройки, set a password. Reload: the browser asks for it.
8. Tick «открыть для локальной сети», save, restart the command. It listens on
   every interface. Now clear the password and restart: the command refuses to
   start and says why.

Record what actually happened — including anything that did not — in
`docs/measurements/2026-08-XX-panel-6a.md`. A demonstrable nobody performed is a
demonstrable that does not hold.

- [ ] **Step 7: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd internal docs && git commit -m "$(cat <<'MSG'
feat(cmd): avitomon panel, and the whole collector configurable from a browser

The wiring, and the one place this product differs from its own suite:
every live third party — BlankTrail's control API, Telegram, the
operating system's autostart entry — is a function field on web.Server,
so internal/web's tests need no network, no licensed proxy and no
registry.

The channel test reads the list and counts it and dials nothing. Dialling
every address of a thousand-line list from a settings screen is a
thousand connections nobody asked for; the pool finds out which addresses
work by using them, and Mixer demotes the ones that do not.

The LAN refusal happens before Listen and is printed on the terminal
rather than logged: somebody who ticked the box and restarted needs to
know why nothing came up, where they are looking.

Retention and the free-space floor are read out of settings and handed to
the process at start. store.SetRetention holds them in memory and nothing
reads the columns on its own, so a panel that saved them and a collector
that ignored them was the state before this.

MSG
)"
```

---

## Done when

- `gofmt -l ./cmd ./internal` prints nothing; `go vet ./...` is clean;
  `go test ./...` passes.
- `avitomon panel` opens on `127.0.0.1:8765` with no password and no
  configuration, and says on the terminal that nothing collects until a
  transport is chosen.
- The first screen offers both transports with **neither pre-selected**, quotes
  250–260 and 360 requests an hour, and carries the one-address,
  stopped-by-the-operator caveat. It quotes no figure the soak did not produce.
- A watch is added, edited, disabled and deleted from the browser.
- A schedule over the budget is refused **with the sum shown** — what is spent,
  what this one asks for, and the ceiling with its arithmetic.
- An interval under 120 seconds is refused, saying that Avito returns the same
  page byte for byte inside a minute.
- Adding a watch from the browser creates the same nine default rules the
  command line creates, when there is a recipient, and says so plainly when
  there is not.
- Прокси shows the address count, quotes the first unreadable line of a pasted
  list, states that N addresses is N ceilings and that ports are not addresses,
  and **never pulls a rotating channel's change link**.
- No secret is ever rendered back into a page: not the BlankTrail key, not the
  bot token, not the api_hash, not a proxy password. An empty secret box leaves
  the stored value alone; clearing one is its own tick.
- No check's error reaches a screen with a credential still in it.
- The panel password is stored as a PBKDF2 hash, and an unreadable hash refuses
  rather than accepts.
- `ListenAddress` refuses the LAN while there is no password, and the command
  refuses to start rather than opening the port.
- The autostart checkbox reflects whether an entry **actually exists**, not what
  was stored.
- The demonstrable in Task 11 Step 6 has been performed by a person and written
  up, including anything that did not work.

## What this plan deliberately does not build

Named, because each appears in the spec's §The panel and a reader who found it
missing would reasonably assume it was forgotten.

**Здоровье сбора, История, Правила and Результаты.** They are 6B and 6C. Their
tabs are absent rather than present-and-404: a tab that answers 404 is a tab a
person clicks once and stops trusting.

**Перепубликации.** Increment 7 builds the linker; there is nothing to list
until there is.

**The tray binary and the service units.** «Windows tray binary built
`-H windowsgui` plus a headless binary; systemd and launchd units» is packaging,
and the increment's own sentence is about screens. `internal/autostart` is here
because the Настройки checkbox needs it; the tray is not, and M1 is not finished
without it.

**Editing a watch in place.** The screen adds, toggles and deletes.
`store.UpdateWatch` exists and is tested — Task 2 wrote it, and `toggleWatch`
uses it — but the form posts only new watches. An edit form is a second copy of
the validation and the budget check, and the budget check has to exclude the
watch being edited, which is why `BudgetFor` takes an `exclude` id already. 6B
adds the form; the arithmetic is ready for it.

**A live view.** The donor has `/live` over an event bus. Nothing in this
product's spec asks for one, and a page that updates itself is a page whose
tests need a clock.

**Any use of `channels` by the collector.** This plan stores the configuration
and states its consequence. Wiring `blanktrail.Mixer` over the stored channels
belongs where the pool is opened — `cmd/avitomon/run.go`'s `openLeaser` — and
doing it here would mean a screen that changes collection behaviour with no test
that collection changed. The address count the budget uses is read from the same
rows, so the arithmetic on screen and the arithmetic in the pool will agree when
that lands.

## What 6B and 6C inherit

A `web.Server` with routing, the password gate, the LAN refusal, the layout,
the stylesheet and the fragment loader; `esc`/`attr`/`field`/`checkbox`/`note`
so a new screen is a function that returns a string; `page` and `render` so it
gets the header and the banner for free; and `s.tabs` with two lines to add.

`store.Watches`, `store.Channels`, `store.PanelSettings` and persisted retention.
`BudgetFor` with its `exclude` parameter, ready for the edit form. `internal/ru`
for anything that has to count in Russian.

6B adds `internal/chart` and the two reading screens; 6C adds `internal/export`
and the two event screens. Neither needs anything from the other.

## Self-review

Run against the spec's §The panel, and against increment 6's own sentence.

**1. Spec coverage.** §The panel names eight screens plus the first-run
question. This plan covers the first-run question, Наблюдения with the budget
arithmetic, Прокси with the mix and the per-entry tests, and Настройки with the
BlankTrail check and the LAN refusal — four of the nine. Здоровье сбора,
История, Правила and Результаты are 6B and 6C by the split declared at the top;
Перепубликации is increment 7 by the spec's own cut. The tray and the service
units are named above as not built and not covered by any of the three plans,
which is a gap in **increment 6 as a whole** and is stated rather than hidden.

**2. Placeholder scan.** No step says «add validation», «handle edge cases» or
«similar to Task N». Every code step carries the code. Three steps say «delete
this line if `go vet` flags it» — `surfaceOf`, `var _ = store.ErrNoTransportChosen`
and `var _ = os.Getenv` — which is a real instruction about a real import, not a
placeholder, and each names the exact symbol.

**3. Type consistency.** `store.PanelSettings`, `store.Channel`, `store.Proxy`,
`Budget`, `Tab`, `page` and `Autostart` are declared once and used with the same
field names throughout. `BudgetFor(requestsPerMinute, addresses int, watches
[]store.Watch, exclude int64)` has the same signature in Tasks 7, 8 and 10.
`s.addressCount(ctx)` is declared in Task 8 and used in Task 10, which is the
one cross-task call in the plan and is why it is listed in Task 8's Produces.
`writeFragment`, `redactSecret` and `num` are declared in Tasks 7 and 9 and used
in Task 10.

**Two things found and fixed while reviewing.** `requestsPerMinute` and the
quiet hours were being read with raw SQL inside `internal/web`, which contradicts
the rule this codebase has held since increment 5 — every package reaches SQLite
through `internal/store`, so a schema change has one place to hide. Both now go
through `store.RequestsPerMinute` and `store.QuietHours`, added to Task 2.

## Execution handoff

Plan complete and saved to
`docs/superpowers/plans/2026-08-24-m1-increment-6a-panel-shell.md`.

