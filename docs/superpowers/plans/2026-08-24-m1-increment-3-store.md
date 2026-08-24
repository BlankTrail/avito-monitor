# M1 Increment 3 — The store and one pass

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One SQLite file that a pass can be written into atomically, thinned without losing the evidence that collection failed, and reported on before the disk fills.

**Architecture:** `internal/store` owns the file: an embedded numbered migration, a single writer under WAL, and one `SavePass` transaction that writes a `passes` row, its `pass_items`, the `listings` it saw, a `listing_snapshots` row per listing whose fingerprint moved, and the `watch_members` set. Retention is three separate rules with different biases — snapshots thinned by period, healthy passes thinned by hour and day, bodies swept by age and by size — and `Reclaim` runs them in a fixed order that reaches failed passes last. Nothing in this increment knows what a change is; increment 4 computes those from what this one stores.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (no CGO — house rule, and the only SQLite that satisfies it), `database/sql`, stdlib. No ORM, no query builder, no test framework.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md` — §Storage (line 302), §Schema — migration 0001 (line 611), §Packages (line 1435), increment 3 (line 1491). Read §Storage before Task 1; every retention decision in this plan argues from it.

**Donor:** `S:\Projects\WB_GO`. Four things are lifted rather than written: `internal/store/migrate.go` (the numbered-file runner with its checksum guard), `internal/store/vacuum.go`'s `Volume`, `internal/store/retention.go`'s `thinSnapshotsSQL`, and the `Open` pragma set. Each lift names its donor file in a comment, as increment 2's lifts do.

## Global Constraints

- **Go 1.26, no CGO.** `go build` must work with `CGO_ENABLED=0`, which is why `modernc.org/sqlite` and not `mattn/go-sqlite3`.
- **`go test -race` does not run on this machine** — the race detector needs cgo and there is no gcc on PATH. Run `go test ./...` locally; `-race` is CI's job. Do not add a Makefile: this project has no task runner, matching WB_GO.
- **Every table is `STRICT`.** Every timestamp is whole Unix seconds, with exactly two exceptions named for what they are: `sort_ts_ms` and `allow_ts_ms`, which are Avito's own absolute epoch milliseconds kept as sent.
- **Money is integer minor units beside a currency, never a float.** `avito.RoublesToMinor` already exists from increment 1 and is the only conversion; the store never does arithmetic on money.
- **`price_kind` is three-valued** — `exact`, `negotiable`, `absent`. The store never collapses `negotiable` and `absent` into a null price.
- **No `ON DELETE RESTRICT` anywhere**, so a cascade cannot deadlock on its own foreign keys.
- **`avito/` must not learn about the database.** `TestPackageReachesNoNetwork` guards the network; nothing in this increment may add an import of `internal/store` to `avito/`.
- **Comments carry the reason, not the restatement.** Match the surrounding style: a comment says why a line is the way it is, or it is deleted.
- **The API key is never written to any file this program creates** — carried forward from increment 2 and re-checked here because `settings` is the first table that could hold one.

---

## File structure

| file | responsibility |
|---|---|
| `internal/store/store.go` | `Open`, the pragma set, `Close`, the `*sql.DB` handle and the single-writer mutex |
| `internal/store/migrate.go` | the numbered-file runner, lifted from WB_GO |
| `internal/store/migrations/0001_avito.sql` | the whole schema, copied verbatim from the spec |
| `internal/store/schema_test.go` | invariants proved against the applied schema rather than against the file |
| `internal/store/settings.go` | `Setting`/`SetSetting`, and the transport-mode accessor Decision 3 needs |
| `internal/store/watches.go` | `AddWatch`, `Watch`, `DueWatches`, `MarkHalted` |
| `internal/store/fingerprint.go` | pure: what a snapshot fingerprint is, and when an anchor is due |
| `internal/store/pass.go` | `SavePass` — the one transaction |
| `internal/store/volume.go` | `Volume`, lifted; and `FreeSpace` with the precheck |
| `internal/store/retention.go` | `ThinSnapshots`, `ThinPasses`, `SweepBodies`, and `Reclaim`'s fixed order |
| `cmd/avitomon/watch.go` | `avitomon watch add` |
| `cmd/avitomon/run.go` | `avitomon run --once` — the first end-to-end pass |
| `cmd/avitomon/admin.go` | `avitomon volume`, `avitomon transport set` |

---

## Task 1: Open the file, run the migration, prove the schema

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/migrations/0001_avito.sql`
- Test: `internal/store/store_test.go`, `internal/store/schema_test.go`

**Interfaces:**
- Produces: `type Store struct{}`; `func Open(ctx context.Context, path string) (*Store, error)`; `func (s *Store) Close() error`; `func (s *Store) DB() *sql.DB`; `var ErrClosed error`.

- [ ] **Step 1: Take the dependency**

```bash
cd S:/Projects/Avito_Go && go get modernc.org/sqlite@latest && go mod tidy
```

Expected: `modernc.org/sqlite` appears in `go.mod`'s first require block. It brings indirect modules; that is known and is the price of no CGO.

- [ ] **Step 2: Write the failing test for Open and the pragmas**

Create `internal/store/store_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenSetsThePragmasThatHoldOneWriterAndManyReadersTogether.
//
// Without WAL a chart drawn while a pass is committing is a SQLITE_BUSY in the
// browser, which is a bug a user meets and cannot explain. The other three are
// the same argument: a busy_timeout of zero turns a concurrent read into an
// error, and foreign_keys defaults to OFF in SQLite, so every ON DELETE
// CASCADE in the schema would be decoration.
func TestOpenSetsThePragmasThatHoldOneWriterAndManyReadersTogether(t *testing.T) {
	s := openTemp(t)

	want := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"foreign_keys": "1",
		"busy_timeout": "5000",
	}
	for pragma, expected := range want {
		var got string
		if err := s.DB().QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != expected {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, expected)
		}
	}
}

// TestOpenIsIdempotent. A second Open of the same file must find the migration
// already applied and not attempt it again.
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "avito.db")

	s1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	var n int
	if err := s2.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("schema_migrations has %d rows after two Opens, want 1", n)
	}
}

// openTemp is the fixture every test in this package uses.
func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "avito.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
```

- [ ] **Step 3: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run TestOpen -v
```

Expected: FAIL to build — `undefined: Open`.

- [ ] **Step 4: Lift the migration runner**

Copy `S:\Projects\WB_GO\internal\store\migrate.go` to `internal/store/migrate.go` verbatim, then make exactly these changes and no others:

- keep `loadMigrations`, `checksum` and `migrate` as they are — the gap check, the duplicate check and the missing-prefix check are the reason to lift rather than write;
- add a comment at the top of the file naming the donor, in the form increment 2's lifts use:

```go
// Lifted from WB_GO's internal/store/migrate.go, unchanged except this
// comment. The rules it enforces are the reason to lift it rather than write
// one: a version gap, a duplicate number or a file that does not start with
// digits fails the load, and an edit to an already-applied migration is caught
// by its checksum instead of being believed.
```

- [ ] **Step 5: Write the migration**

Create `internal/store/migrations/0001_avito.sql`. Its content is the spec's §Schema — migration 0001, lines 611 to 1433, **copied verbatim**, comments included. Do not retype it and do not summarise it:

The schema is **not** in a fenced code block. It is raw text between the heading
and the `---` that closes the section, so looking for a fence finds the one
belonging to §Packages and extracts thirty-two lines of nothing. Anchor on the
SPDX line that opens the SQL and on the section rule that ends it:

```bash
cd S:/Projects/Avito_Go && python - <<'PY'
import io
lines = io.open('docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md', encoding='utf-8').read().split('\n')
start = next(i for i, l in enumerate(lines) if l.startswith('# Schema'))
begin = next(i for i in range(start, len(lines)) if lines[i].startswith('-- SPDX'))
end   = next(i for i in range(begin, len(lines)) if lines[i].strip() == '---')
body = '\n'.join(lines[begin:end]).rstrip() + '\n'
io.open('internal/store/migrations/0001_avito.sql', 'w', encoding='utf-8', newline='\n').write(body)
print('lines', body.count('\n'), '| tables', body.count('CREATE TABLE'), '| indexes', body.count('CREATE INDEX'))
PY
```

Expected: **818 lines, 21 tables, 23 indexes.** If any of the three differs,
stop — the extraction took the wrong range, and a partial schema is worse than
none.

- [ ] **Step 6: Write `store.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is the SQLite file: the schema, the pass transaction, the
// reads, retention and the numbers that say what it all costs.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// ErrClosed is returned by every method on a Store that has been closed.
var ErrClosed = errors.New("store: closed")

// Store is one database file.
//
// One connection writes and the panel's readers do not block it. The write
// mutex is here rather than left to SQLite because a pass is several
// statements that must not interleave with another pass's, and SQLITE_BUSY
// under WAL is a retry loop nobody wants in the hot path.
type Store struct {
	db     *sql.DB
	wmu    sync.Mutex
	closed bool
	mu     sync.RWMutex
}

// pragmas are set on every connection in the pool, which is why they travel in
// the DSN rather than being executed once after Open: database/sql opens
// connections lazily and a PRAGMA run on one of them binds to that connection
// alone.
//
//	journal_mode=WAL   one writer, many readers. Without it a chart drawn while
//	                   a pass is committing is a SQLITE_BUSY in the browser.
//	synchronous=NORMAL the WAL-appropriate setting. The trade is named in the
//	                   spec: a power cut can lose the last transactions, which
//	                   for a monitor means one pass, and a pass is re-collected
//	                   on the next tick.
//	busy_timeout=5000  wait rather than fail.
//	foreign_keys=ON    SQLite defaults this OFF, which would make every ON
//	                   DELETE CASCADE in the schema decoration.
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)"

// Open opens or creates the database and brings it to the latest schema.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One writer. More would not make writes faster — SQLite serialises them —
	// and would turn a contended pass into a SQLITE_BUSY instead of a wait.
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(ctx, db, migrationFS); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// DB is the handle, for readers and for tests. Writers go through the methods
// on Store, which take the write lock.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}
```

Adjust `migrate`'s signature to whatever WB_GO's copy expects; if its is `migrate(ctx, db)` with a package-level `migrationFS`, call it that way and delete the third argument here.

- [ ] **Step 7: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go build ./... && go test ./internal/store/ -run TestOpen -v
```

Expected: both PASS.

- [ ] **Step 8: Write the schema invariant tests**

These prove the *applied* schema rather than the file, so a bad copy is caught. Create `internal/store/schema_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"strings"
	"testing"
)

// TestEveryTableIsStrict. A non-STRICT table accepts a string into an INTEGER
// column and stores it, so a unit error or a nil that should have been a zero
// becomes a row nobody notices until a chart is wrong.
func TestEveryTableIsStrict(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(
		`SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		if name == "schema_migrations" {
			continue // the runner's own bookkeeping, lifted as it stands
		}
		n++
		if !strings.Contains(strings.ToUpper(ddl), "STRICT") {
			t.Errorf("table %s is not STRICT", name)
		}
	}
	if n != 21 {
		t.Errorf("found %d tables, want the 21 the spec's migration 0001 defines", n)
	}
}

// TestNoOnDeleteRestrict. The emergency reclaim deletes in a fixed order, and
// a RESTRICT anywhere would let a cascade deadlock on its own foreign keys at
// exactly the moment the disk is full.
func TestNoOnDeleteRestrict(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(`SELECT name, sql FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToUpper(ddl), "ON DELETE RESTRICT") {
			t.Errorf("table %s uses ON DELETE RESTRICT", name)
		}
	}
}

// TestOnlyTwoColumnsAreMilliseconds. Every timestamp in this schema is whole
// Unix seconds except Avito's own two, which are kept as sent. A third
// millisecond column would make the retention arithmetic — written in 86400
// and 604800 literals — silently wrong for that column, and a wrongly rescaled
// bucket fails as a wrong chart and never as a compile error.
func TestOnlyTwoColumnsAreMilliseconds(t *testing.T) {
	s := openTemp(t)
	rows, err := s.DB().Query(
		`SELECT m.name, p.name FROM sqlite_master m
		 JOIN pragma_table_info(m.name) p
		 WHERE m.type='table' AND p.name LIKE '%\_ms' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		got[col] = true
	}
	for _, want := range []string{"sort_ts_ms", "allow_ts_ms"} {
		if !got[want] {
			t.Errorf("column %s is missing", want)
		}
		delete(got, want)
	}
	for col := range got {
		t.Errorf("unexpected millisecond column %q — every other timestamp in "+
			"this schema is whole seconds, and the retention arithmetic assumes it", col)
	}
}

// TestPriceKindIsThreeValuedEverywhereItAppears. A null price is ambiguous
// between «Цена договорная», which is the seller's own statement and a real
// event, and a parse gap, which is our defect. Collapsing them means that the
// first time Avito withholds the hydration state, thirty listings
// simultaneously "become negotiable".
func TestPriceKindIsThreeValuedEverywhereItAppears(t *testing.T) {
	s := openTemp(t)
	var ddl string
	if err := s.DB().QueryRow(
		`SELECT sql FROM sqlite_master WHERE name='listing_snapshots'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"'exact'", "'negotiable'", "'absent'"} {
		if !strings.Contains(ddl, kind) {
			t.Errorf("listing_snapshots.price_kind does not admit %s", kind)
		}
	}
}
```

- [ ] **Step 9: Run them and fix whatever the copy got wrong**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: all PASS. If `TestEveryTableIsStrict` reports a count other than 21, the extraction in step 5 took the wrong block — fix that rather than the test.

- [ ] **Step 10: Commit**

```bash
cd S:/Projects/Avito_Go && git add go.mod go.sum internal/store/ && git commit -m "$(cat <<'MSG'
feat(store): the file, the pragmas and migration 0001

Twenty-one STRICT tables, applied by WB_GO's numbered-file runner lifted
unchanged — its gap check, duplicate check and checksum guard are the
reason to lift rather than write one.

The pragmas travel in the DSN rather than being executed after Open,
because database/sql opens connections lazily and a PRAGMA run on one of
them binds to that connection alone.

The schema tests prove the applied schema rather than the file: every
table STRICT, no ON DELETE RESTRICT anywhere, and exactly two millisecond
columns. That last one is not pedantry — the retention arithmetic is
written in 86400 and 604800 literals, and a third millisecond column
would fail as a wrong chart rather than as a compile error.

MSG
)"
```

---

## Task 2: Settings, and `avitomon transport set`

Decision 3 forbids a transport default, and increments 3, 4 and 5 all collect before the panel that would ask exists. This is the command-line half of that decision — the same one column the first screen will write later — and not a default sneaking in by another door.

**Files:**
- Create: `internal/store/settings.go`, `internal/store/settings_test.go`, `cmd/avitomon/admin.go`
- Modify: `cmd/avitomon/main.go` — route the new subcommands

**Interfaces:**
- Consumes: `Store` from Task 1.
- Produces: `func (s *Store) Setting(ctx context.Context, key string) (string, bool, error)`; `func (s *Store) SetSetting(ctx context.Context, key, value string) error`; `func (s *Store) TransportMode(ctx context.Context) (transport.Mode, error)`; `func (s *Store) SetTransportMode(ctx context.Context, m transport.Mode) error`; `var ErrNoTransportChosen error`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/settings_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// TestTransportModeHasNoDefault is decision 3, enforced where it can actually
// be enforced.
//
// A default here would be a choice made on the user's behalf about whether
// their traffic goes through a paid proxy. The first run asks; until it has
// been answered, collection refuses to start rather than picking one.
func TestTransportModeHasNoDefault(t *testing.T) {
	s := openTemp(t)
	_, err := s.TransportMode(context.Background())
	if !errors.Is(err, ErrNoTransportChosen) {
		t.Fatalf("TransportMode on a fresh database = %v, want ErrNoTransportChosen — "+
			"a default here decides for the user whether their traffic is paid for", err)
	}
}

func TestTransportModeRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for _, m := range []transport.Mode{transport.ModeDirect, transport.ModeBlankTrail} {
		if err := s.SetTransportMode(ctx, m); err != nil {
			t.Fatalf("SetTransportMode(%v): %v", m, err)
		}
		got, err := s.TransportMode(ctx)
		if err != nil {
			t.Fatalf("TransportMode: %v", err)
		}
		if got != m {
			t.Errorf("TransportMode = %v, want %v", got, m)
		}
	}
}

// TestSetTransportModeRefusesAnUnknownMode. The column has no CHECK — the
// catalogue lives in Go — so the guard has to be here.
func TestSetTransportModeRefusesAnUnknownMode(t *testing.T) {
	s := openTemp(t)
	if err := s.SetTransportMode(context.Background(), transport.Mode("carrier-pigeon")); err == nil {
		t.Error("an unknown transport mode was accepted")
	}
}

func TestSettingRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if _, ok, err := s.Setting(ctx, "absent"); err != nil || ok {
		t.Errorf("Setting(absent) = ok %v, err %v; want false, nil", ok, err)
	}
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := s.Setting(ctx, "k")
	if err != nil || !ok || v != "v2" {
		t.Errorf("Setting(k) = %q, %v, %v; want \"v2\", true, nil", v, ok, err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Transport|Setting' -v
```

Expected: FAIL to build — `undefined: ErrNoTransportChosen`.

- [ ] **Step 3: Write `settings.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// transportModeKey is the one settings row that collection refuses to start
// without.
const transportModeKey = "transport.mode"

// ErrNoTransportChosen says nobody has answered the question yet.
//
// There is deliberately no default. Choosing one would decide on the user's
// behalf whether their traffic goes through a paid proxy, and the first run
// asks instead — on the command line here, and on the first screen once the
// panel exists in increment 6. Both write this same column.
var ErrNoTransportChosen = errors.New("store: no transport mode has been chosen")

func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("store: setting %q: %w", key, err)
	}
	return v, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}

func (s *Store) TransportMode(ctx context.Context) (transport.Mode, error) {
	v, ok, err := s.Setting(ctx, transportModeKey)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoTransportChosen
	}
	return transport.Mode(v), nil
}

func (s *Store) SetTransportMode(ctx context.Context, m transport.Mode) error {
	// The column has no CHECK — the catalogue lives in Go and a released
	// database must not refuse a value because a new mode exists — so the
	// guard is here, where the catalogue is.
	if m != transport.ModeDirect && m != transport.ModeBlankTrail {
		return fmt.Errorf("store: %q is not a transport mode", m)
	}
	return s.SetSetting(ctx, transportModeKey, string(m))
}
```

If `settings` in migration 0001 does not have `key` as its primary key, read the spec's DDL at line 1405 and match the real column names rather than changing the migration.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -run 'Transport|Setting' -v
```

Expected: four PASS.

- [ ] **Step 5: Add the `transport set` and `volume` subcommands**

Create `cmd/avitomon/admin.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// transportCmd is the command-line half of decision 3. Increments 3, 4 and 5
// all collect before the panel that would ask exists, so the question has to
// be answerable here — and it writes the same one column the first screen
// will write later, rather than a second source of truth.
func transportCmd(args []string) error {
	fs := flag.NewFlagSet("transport", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 || fs.Arg(0) != "set" {
		return errors.New("usage: avitomon transport set direct|blanktrail")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := s.SetTransportMode(ctx, transport.Mode(fs.Arg(1))); err != nil {
		return err
	}
	fmt.Printf("transport mode is now %s\n", fs.Arg(1))
	return nil
}

func volumeCmd(args []string) error {
	fs := flag.NewFlagSet("volume", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	v, err := s.Volume(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, v.String())
	return nil
}
```

`volumeCmd` will not compile until Task 6. Write it now anyway and add `volume` to the router in the same step, so the router is touched once; Task 6's step 1 test is what makes it build.

- [ ] **Step 6: Route the subcommands**

In `cmd/avitomon/main.go`, replace the current `os.Args[1] != "fetch"` check with a switch:

```go
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetchCmd(os.Args[2:])
	case "transport":
		err = transportCmd(os.Args[2:])
	case "volume":
		err = volumeCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		var halt *transport.HaltError
		if errors.As(err, &halt) {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "STOPPED  %s\n", halt.Verdict.Class)
			fmt.Fprintf(os.Stderr, "  status  %d\n", halt.Status)
			if halt.Verdict.Wall.Marker != "" {
				fmt.Fprintf(os.Stderr, "  marker  %q (%s)\n",
					halt.Verdict.Wall.Marker, halt.Verdict.Wall.Label)
			}
			fmt.Fprintf(os.Stderr, "  url     %s\n\n", halt.URL)
			fmt.Fprintln(os.Stderr, wrap(halt.Boundary(), 76))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  avitomon fetch [flags] <url>")
	fmt.Fprintln(os.Stderr, "  avitomon transport set direct|blanktrail")
	fmt.Fprintln(os.Stderr, "  avitomon watch add [flags] <url>")
	fmt.Fprintln(os.Stderr, "  avitomon run --once [flags]")
	fmt.Fprintln(os.Stderr, "  avitomon volume [flags]")
}
```

Move the existing halt-printing block out of the old `main` and into this one unchanged — it is increment 2's boundary message and its wording was measured.

- [ ] **Step 7: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/settings.go internal/store/settings_test.go cmd/avitomon/ && git commit -m "$(cat <<'MSG'
feat(store): settings, and the transport choice with no default

Decision 3 forbids a transport default, and increments 3, 4 and 5 all
collect before the panel that would ask exists. So the question is
answerable on the command line, writing the same one column the first
screen will write later — the command-line half of the decision, not a
default arriving by another door.

TransportMode on a fresh database returns ErrNoTransportChosen. A default
would decide for the user whether their traffic goes through a paid
proxy.

The mode is validated in Go because the column deliberately has no CHECK:
the catalogue lives in Go and grows there, and a released database must
not refuse a value because a new mode exists.

MSG
)"
```

---

## Task 3: Watches

**Files:**
- Create: `internal/store/watches.go`, `internal/store/watches_test.go`

**Interfaces:**
- Consumes: `Store`, `avito.Surface`.
- Produces:

```go
type Watch struct {
    ID                   int64
    Name                 string
    Kind                 string // "search" | "seller" | "listing"
    URL                  string
    Surface              avito.Surface
    MaxPages             int
    IntervalSec          int
    AbsenceConfirmations int
    AbsenceMinSpanSec    int
    State                string // "idle" | "running" | "halted" | "disabled"
    HaltedReason         string
    SeededAt             *int64
    NextDueAt            int64
    LastTotalCount       *int64
    TooBroad             bool
    Enabled              bool
    CreatedAt            int64
    UpdatedAt            int64
}

func (s *Store) AddWatch(ctx context.Context, w Watch) (int64, error)
func (s *Store) Watch(ctx context.Context, id int64) (Watch, error)
func (s *Store) DueWatches(ctx context.Context, now int64, limit int) ([]Watch, error)
func (s *Store) MarkHalted(ctx context.Context, id int64, reason string, at int64) error
```

- [ ] **Step 1: Write the failing test**

Create `internal/store/watches_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aWatch() Watch {
	return Watch{
		Name: "iphone", Kind: "search",
		URL:     "https://m.avito.ru/all?q=iphone+15&s=104",
		Surface: avito.SurfaceMobile, MaxPages: 1, IntervalSec: 300,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		Enabled: true, CreatedAt: 1000, UpdatedAt: 1000,
	}
}

func TestAddWatchRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	id, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	got, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if got.URL != aWatch().URL || got.Surface != avito.SurfaceMobile ||
		got.IntervalSec != 300 || !got.Enabled {
		t.Errorf("Watch = %+v; the round trip lost something", got)
	}
	if got.State != "idle" {
		t.Errorf("State = %q, want idle", got.State)
	}
}

// TestMaxPagesIsRefusedAboveTwo. Deep pagination is what escalates a 429 into
// a 403, and a 403 costs the cookie jar and therefore the clearance it was
// carrying. The spec refuses it in three independent places; this is the one
// the database owns.
func TestMaxPagesIsRefusedAboveTwo(t *testing.T) {
	s := openTemp(t)
	w := aWatch()
	w.MaxPages = 3
	if _, err := s.AddWatch(context.Background(), w); err == nil {
		t.Error("max_pages 3 was accepted; the CHECK is one of three independent " +
			"refusals and it must be the one that cannot be argued around")
	}
}

// TestIntervalIsRefusedBelowTwoMinutes.
func TestIntervalIsRefusedBelowTwoMinutes(t *testing.T) {
	s := openTemp(t)
	w := aWatch()
	w.IntervalSec = 60
	if _, err := s.AddWatch(context.Background(), w); err == nil {
		t.Error("a 60-second interval was accepted; the floor is 120")
	}
}

// TestDueWatchesAsksTheSchedulersOnlyQuestion.
func TestDueWatchesAsksTheSchedulerSOnlyQuestion(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	due := aWatch()
	due.Name, due.NextDueAt = "due", 500
	dueID, _ := s.AddWatch(ctx, due)

	later := aWatch()
	later.Name, later.NextDueAt = "later", 5000
	s.AddWatch(ctx, later)

	off := aWatch()
	off.Name, off.NextDueAt, off.Enabled = "disabled", 500, false
	s.AddWatch(ctx, off)

	got, err := s.DueWatches(ctx, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != dueID {
		names := make([]string, len(got))
		for i, w := range got {
			names[i] = w.Name
		}
		t.Errorf("DueWatches = %v, want only the enabled watch whose next_due_at "+
			"has passed", names)
	}
}

// TestAHaltedWatchIsNeverDue, and the reason is never cleared by the program.
// A monitor that decides on its own that the wall it hit has gone away is a
// monitor that resumes into it all night.
func TestAHaltedWatchIsNeverDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	w := aWatch()
	w.NextDueAt = 500
	id, _ := s.AddWatch(ctx, w)
	if err := s.MarkHalted(ctx, id, "captcha marker geetest", 900); err != nil {
		t.Fatal(err)
	}
	got, err := s.DueWatches(ctx, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a halted watch came back due: %+v", got)
	}
	after, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.HaltedReason != "captcha marker geetest" || after.State != "halted" {
		t.Errorf("state=%q reason=%q; both must survive for a person to read",
			after.State, after.HaltedReason)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run Watch -v
```

Expected: FAIL to build — `undefined: Watch`.

- [ ] **Step 3: Write `watches.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
)

// ErrNoSuchWatch is returned by Watch for an id that is not there.
var ErrNoSuchWatch = errors.New("store: no such watch")

// Watch is one thing being monitored.
type Watch struct {
	ID                   int64
	Name                 string
	Kind                 string
	URL                  string
	Surface              avito.Surface
	MaxPages             int
	IntervalSec          int
	AbsenceConfirmations int
	AbsenceMinSpanSec    int
	State                string
	HaltedReason         string
	SeededAt             *int64
	NextDueAt            int64
	LastTotalCount       *int64
	TooBroad             bool
	Enabled              bool
	CreatedAt            int64
	UpdatedAt            int64
}

const watchColumns = `id, name, kind, url, surface, max_pages, interval_sec,
	absence_confirmations, absence_min_span_sec, state, halted_reason,
	seeded_at, next_due_at, last_total_count, too_broad, enabled,
	created_at, updated_at`

func scanWatch(sc interface{ Scan(...any) error }) (Watch, error) {
	var w Watch
	var surface string
	err := sc.Scan(&w.ID, &w.Name, &w.Kind, &w.URL, &surface, &w.MaxPages,
		&w.IntervalSec, &w.AbsenceConfirmations, &w.AbsenceMinSpanSec,
		&w.State, &w.HaltedReason, &w.SeededAt, &w.NextDueAt,
		&w.LastTotalCount, &w.TooBroad, &w.Enabled, &w.CreatedAt, &w.UpdatedAt)
	w.Surface = avito.Surface(surface)
	return w, err
}

func (s *Store) AddWatch(ctx context.Context, w Watch) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO watches
		  (name, kind, url, surface, max_pages, interval_sec,
		   absence_confirmations, absence_min_span_sec, next_due_at,
		   enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.Name, w.Kind, w.URL, string(w.Surface), w.MaxPages, w.IntervalSec,
		w.AbsenceConfirmations, w.AbsenceMinSpanSec, w.NextDueAt,
		w.Enabled, w.CreatedAt, w.UpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("store: add watch: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) Watch(ctx context.Context, id int64) (Watch, error) {
	w, err := scanWatch(s.db.QueryRowContext(ctx,
		`SELECT `+watchColumns+` FROM watches WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, ErrNoSuchWatch
	}
	if err != nil {
		return Watch{}, fmt.Errorf("store: watch %d: %w", id, err)
	}
	return w, nil
}

// DueWatches is the scheduler's only question, and idx_watches_due is the
// index that answers it: enabled, not halted, and past its time.
//
// 'running' is excluded as well as 'halted'. A watch already in flight must
// not be picked up twice by a scheduler that ticked while the first pass was
// still going — that would be two requests where the budget accounted for one.
func (s *Store) DueWatches(ctx context.Context, now int64, limit int) ([]Watch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+watchColumns+` FROM watches
		WHERE enabled = 1 AND state IN ('idle') AND next_due_at <= ?
		ORDER BY next_due_at LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("store: due watches: %w", err)
	}
	defer rows.Close()
	var out []Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MarkHalted stops a watch and records why.
//
// halted_reason is never cleared by this program. A monitor that decides on
// its own that the wall it hit has gone away is a monitor that resumes into it
// all night; clearing it is a person's act, in the panel.
func (s *Store) MarkHalted(ctx context.Context, id int64, reason string, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		UPDATE watches SET state = 'halted', halted_reason = ?, halted_at = ?,
		       updated_at = ?
		WHERE id = ?`, reason, at, at, id)
	if err != nil {
		return fmt.Errorf("store: halt watch %d: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -run Watch -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: five PASS. `TestMaxPagesIsRefusedAboveTwo` and `TestIntervalIsRefusedBelowTwoMinutes` pass because the CHECK constraints in migration 0001 do the refusing — that is the point of them being in the schema.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/watches.go internal/store/watches_test.go && git commit -m "$(cat <<'MSG'
feat(store): watches, and the scheduler's only question

DueWatches reads idx_watches_due: enabled, idle, past its time. 'running'
is excluded alongside 'halted' — a watch already in flight picked up
twice is two requests where the budget accounted for one.

MarkHalted records the reason and this program never clears it. A monitor
that decides on its own that the wall it hit has gone away is a monitor
that resumes into it all night.

max_pages and interval_sec are refused by the schema rather than by Go.
Deep pagination is what escalates a 429 into a 403, and the spec refuses
it in three independent places precisely so no single argument can move
all three.

MSG
)"
```

---

## Task 4: The fingerprint and the anchor rule

Pure, no database, no clock beyond what is passed in. This is the rule that makes volume sublinear in the age of a watch, and it is worth its own task because a reviewer can reject it without touching anything else.

**Files:**
- Create: `internal/store/fingerprint.go`, `internal/store/fingerprint_test.go`

**Interfaces:**
- Consumes: `avito.Listing` from increment 1.
- Produces: `func Fingerprint(l avito.Listing) string`; `type Retention struct{...}`; `func DefaultRetention() Retention`; `func AnchorDue(lastTS, now int64, every time.Duration) bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/fingerprint_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aListing() avito.Listing {
	return avito.Listing{
		ItemID: 7800000001, Title: "iPhone 15, 128 ГБ",
		URLPath:   "/m/x_7800000001",
		PriceKind: avito.PriceExact,
		Price:     avito.Money{Minor: 4000000, Currency: "RUB"},
		PriceLabel: "40 000 ₽",
	}
}

// TestFingerprintMovesWhenSomethingWorthARowMoves.
//
// A snapshot is written only when the fingerprint moves, plus one anchor per
// period. That is what makes growth sublinear in the age of a watch, and it
// means the fingerprint decides what history exists: a field outside it can
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

// TestAnchorDue. An anchor is what makes "unchanged since March" and "unread
// since March" different sentences. Without one, a listing whose price has not
// moved in six weeks is indistinguishable from a watch that stopped running.
func TestAnchorDue(t *testing.T) {
	const day = 86400
	cases := []struct {
		name           string
		lastTS, now    int64
		every          time.Duration
		want           bool
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
	if r.SnapshotDailyAfter != 30*24*time.Hour {
		t.Errorf("SnapshotDailyAfter = %v, want 30 days", r.SnapshotDailyAfter)
	}
	if r.SnapshotWeeklyAfter != 365*24*time.Hour {
		t.Errorf("SnapshotWeeklyAfter = %v, want a year", r.SnapshotWeeklyAfter)
	}
	if r.PassHourlyAfter != 7*24*time.Hour {
		t.Errorf("PassHourlyAfter = %v, want 7 days", r.PassHourlyAfter)
	}
	if r.PassDailyAfter != 30*24*time.Hour {
		t.Errorf("PassDailyAfter = %v, want 30 days", r.PassDailyAfter)
	}
	if r.BodyMaxAge != 30*24*time.Hour {
		t.Errorf("BodyMaxAge = %v, want 30 days", r.BodyMaxAge)
	}
	if r.BodyMaxBytes != 500<<20 {
		t.Errorf("BodyMaxBytes = %d, want 500 MB", r.BodyMaxBytes)
	}
	if r.AnchorEvery != 24*time.Hour {
		t.Errorf("AnchorEvery = %v, want a day", r.AnchorEvery)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Fingerprint|Anchor|Retention' -v
```

Expected: FAIL to build — `undefined: Fingerprint`.

- [ ] **Step 3: Write `fingerprint.go`**

```go
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
// Position is deliberately absent. Rank belongs to the watch and not to the
// listing — a listing pushed down by arrivals has not changed — and including
// it would make the series grow with churn instead of with events.
func Fingerprint(l avito.Listing) string {
	var b strings.Builder
	write := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
			b.WriteByte(0x1f) // a separator no field can contain
		}
	}
	write(string(l.PriceKind))
	if l.PriceKind == avito.PriceExact {
		write(strconv.FormatInt(l.Price.Minor, 10), l.Price.Currency)
	}
	// The site's own rendering, kept in the fingerprint as well as the row: a
	// parsed integer that stops agreeing with the string it came from is how a
	// change of unit would be noticed, and it cannot be noticed if the string
	// never moves the fingerprint.
	write(l.PriceLabel)
	write(l.Title)
	write(l.URLPath)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Retention is every threshold this package thins by. They are settings rather
// than constants because the spec says both body bounds are settings and
// neither may be unset; the rest travel with them so there is one place to
// look.
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
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -run 'Fingerprint|Anchor|Retention' -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every subtest PASS. If `avito.Listing`'s field names differ, read `avito/model.go` and use the real ones — do not add fields to `avito`.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/fingerprint.go internal/store/fingerprint_test.go && git commit -m "$(cat <<'MSG'
feat(store): the fingerprint and the anchor rule

A snapshot is written only when the fingerprint moves, plus one anchor
per period. That is what makes the file grow sublinearly in the age of a
watch — and it means this function decides what history exists, because a
field left out of it can change a hundred times and leave no trace.

The hash input is built field by field in a fixed order rather than from
a struct or from JSON: a map iteration or an added field would silently
change every fingerprint in the database, and every listing in it would
then appear to have changed on the day of the upgrade.

Position is deliberately absent. Rank belongs to the watch, not to the
listing, and including it would make the series grow with churn instead
of with events.

MSG
)"
```

---

## Task 5: `SavePass` — the one transaction

The centre of the increment. One `BEGIN` covers the pass row, its items, the listings it saw, the snapshots whose fingerprint moved, and the membership set. A partial pass is worse than no pass: increment 4 differences consecutive passes, and a pass whose `pass_items` are missing reads as every listing having vanished.

**Files:**
- Create: `internal/store/pass.go`, `internal/store/pass_test.go`

**Interfaces:**
- Consumes: `Fingerprint`, `AnchorDue`, `DefaultRetention` from Task 4; `avito.Page`, `avito.Listing`; `transport.Result` from increment 2.
- Produces:

```go
type Pass struct {
    WatchID     int64
    StartedAt   int64
    FinishedAt  int64
    Page        int
    Surface     avito.Surface
    Class       string
    HTTPStatus  int
    Path        string
    Mode        string
    Session     string
    Egress      string
    ElapsedMS   int64
    Bytes       int64
    TotalCount  *int64
    IsSeed      bool
    Evidential  bool
    DegradedWhy string
    Error       string
}

type SaveResult struct {
    PassID    int64
    Arrivals  int
    Snapshots int
}

func (s *Store) SavePass(ctx context.Context, p Pass, listings []avito.Listing) (SaveResult, error)
```

- [ ] **Step 1: Write the failing test**

Create `internal/store/pass_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aPass(watchID int64, at int64) Pass {
	return Pass{
		WatchID: watchID, StartedAt: at, FinishedAt: at + 2,
		Page: 1, Surface: avito.SurfaceMobile, Class: "ok",
		HTTPStatus: 200, Path: "dom", Mode: "direct",
		Session: "chrome-133-android#0/abc", Egress: "direct",
		ElapsedMS: 2000, Bytes: 1170000, Evidential: true,
	}
}

// listingsFor builds a result set in rank order. Rank is the slice index and
// not a field: avito.Listing deliberately has none, because a listing's rank
// belongs to the watch that saw it and not to the listing — the same reason
// Fingerprint leaves position out.
func listingsFor(ids ...int64) []avito.Listing {
	out := make([]avito.Listing, 0, len(ids))
	for _, id := range ids {
		l := aListing()
		l.ItemID = id
		out = append(out, l)
	}
	return out
}

func TestSavePassWritesThePassItsItemsAndItsListings(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	res, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))
	if err != nil {
		t.Fatalf("SavePass: %v", err)
	}
	if res.PassID == 0 {
		t.Fatal("SavePass returned no pass id")
	}

	count := func(q string, args ...any) int {
		var n int
		if err := s.DB().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM passes WHERE id = ?`, res.PassID); n != 1 {
		t.Errorf("passes rows = %d, want 1", n)
	}
	if n := count(`SELECT count(*) FROM pass_items WHERE pass_id = ?`, res.PassID); n != 3 {
		t.Errorf("pass_items rows = %d, want 3", n)
	}
	if n := count(`SELECT count(*) FROM listings`); n != 3 {
		t.Errorf("listings rows = %d, want 3", n)
	}
	if n := count(`SELECT count(*) FROM listing_snapshots`); n != 3 {
		t.Errorf("listing_snapshots rows = %d, want 3 — the first reading of "+
			"anything is a change from nothing", n)
	}
	if n := count(`SELECT count(*) FROM watch_members WHERE watch_id = ?`, wid); n != 3 {
		t.Errorf("watch_members rows = %d, want 3", n)
	}
	if res.Arrivals != 3 {
		t.Errorf("Arrivals = %d, want 3 on a first pass", res.Arrivals)
	}
}

// TestASecondIdenticalPassWritesNoSnapshots is the whole volume argument. If
// this test ever fails, the file grows linearly in the number of passes and
// the product becomes one somebody deletes.
func TestASecondIdenticalPassWritesNoSnapshots(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	res, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(1, 2, 3))
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM listing_snapshots`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("listing_snapshots = %d after two identical passes, want 3 — "+
			"a row is written only when the fingerprint moves", n)
	}
	if res.Arrivals != 0 {
		t.Errorf("Arrivals = %d on an unchanged pass, want 0", res.Arrivals)
	}
	if res.Snapshots != 0 {
		t.Errorf("Snapshots = %d, want 0", res.Snapshots)
	}
}

// TestAPriceChangeWritesExactlyOneSnapshot.
func TestAPriceChangeWritesExactlyOneSnapshot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	moved := listingsFor(1, 2, 3)
	moved[1].Price.Minor = 3500000
	moved[1].PriceLabel = "35 000 ₽"

	res, err := s.SavePass(ctx, aPass(wid, 1300), moved)
	if err != nil {
		t.Fatal(err)
	}
	if res.Snapshots != 1 {
		t.Errorf("Snapshots = %d, want exactly 1", res.Snapshots)
	}
	var n int
	s.DB().QueryRow(`SELECT count(*) FROM listing_snapshots WHERE item_id = 2`).Scan(&n)
	if n != 2 {
		t.Errorf("item 2 has %d snapshots, want 2 — the reading and the change", n)
	}
}

// TestSavePassIsAtomic. A pass whose pass_items are missing reads, to
// increment 4, as every listing having vanished. So a failure must leave
// nothing rather than half of it.
func TestSavePassIsAtomic(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	// watch 999 does not exist, so the passes insert violates its foreign key.
	_, err := s.SavePass(ctx, aPass(999, 1000), listingsFor(1, 2, 3))
	if err == nil {
		t.Fatal("SavePass accepted a pass for a watch that does not exist")
	}
	for _, table := range []string{"passes", "pass_items", "listings", "listing_snapshots"} {
		var n int
		if err := s.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows after a failed SavePass, want 0", table, n)
		}
	}
}

// TestWatchMembersTracksPositionAndAbsence. The counter is per watch and not
// per listing: a listing belongs to several watches and absence from one is
// not absence from the listing. One counter for both is the shortest path to
// announcing removals that never happened.
func TestWatchMembersTracksPositionAndAbsence(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))
	s.SavePass(ctx, aPass(wid, 1300), listingsFor(1, 3)) // 2 is gone

	var streak, lastPos int
	err := s.DB().QueryRowContext(ctx,
		`SELECT missing_streak, last_position FROM watch_members
		 WHERE watch_id = ? AND item_id = 2`, wid).Scan(&streak, &lastPos)
	if err != nil {
		t.Fatalf("member row for the absent listing is gone; it is the evidence "+
			"behind every disappearance: %v", err)
	}
	if streak != 1 {
		t.Errorf("missing_streak = %d, want 1", streak)
	}
	if lastPos != 2 {
		t.Errorf("last_position = %d, want 2 — the rank it was last seen at is "+
			"what makes displacement computable", lastPos)
	}

	// And a listing that came back resets the streak.
	s.SavePass(ctx, aPass(wid, 1600), listingsFor(1, 2, 3))
	s.DB().QueryRowContext(ctx,
		`SELECT missing_streak FROM watch_members WHERE watch_id = ? AND item_id = 2`,
		wid).Scan(&streak)
	if streak != 0 {
		t.Errorf("missing_streak = %d after the listing came back, want 0", streak)
	}
}

// TestAnAnchorRowIsWrittenWhenOneIsDue.
func TestAnAnchorRowIsWrittenWhenOneIsDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1))
	// A day and a second later, with nothing changed.
	res, err := s.SavePass(ctx, aPass(wid, 1000+86401), listingsFor(1))
	if err != nil {
		t.Fatal(err)
	}
	if res.Snapshots != 1 {
		t.Fatalf("Snapshots = %d a day later with nothing changed, want 1 anchor", res.Snapshots)
	}
	var anchor int
	s.DB().QueryRow(
		`SELECT anchor FROM listing_snapshots WHERE item_id = 1 ORDER BY ts DESC LIMIT 1`).Scan(&anchor)
	if anchor != 1 {
		t.Error("the row written a day later is not marked as an anchor; retention " +
			"orders anchors after real changes, and an unmarked one would " +
			"survive a period at the expense of the change it duplicates")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'SavePass|Snapshot|WatchMembers|Anchor' -v
```

Expected: FAIL to build — `undefined: Pass`.

- [ ] **Step 3: Write `pass.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Pass is one fetch of one page of one watch, as the collector saw it.
type Pass struct {
	WatchID     int64
	StartedAt   int64
	FinishedAt  int64
	Page        int
	Surface     avito.Surface
	Class       string
	HTTPStatus  int
	Path        string
	Mode        string
	Session     string
	Egress      string
	ElapsedMS   int64
	Bytes       int64
	TotalCount  *int64
	IsSeed      bool
	Evidential  bool
	DegradedWhy string
	Error       string
}

// SaveResult is what the caller needs back: the id to hang changes off, and
// the two counts the panel reports.
type SaveResult struct {
	PassID    int64
	Arrivals  int
	Snapshots int
}

// SavePass writes one pass and everything it implies, in one transaction.
//
// One BEGIN covers the pass row, its pass_items, the listings it saw, a
// listing_snapshots row for each whose fingerprint moved or whose anchor is
// due, and the watch_members set. A partial pass is worse than no pass:
// increment 4 differences consecutive passes, and a pass whose pass_items are
// missing reads as every listing in the watch having vanished at once.
func (s *Store) SavePass(ctx context.Context, p Pass, listings []avito.Listing) (SaveResult, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SaveResult{}, fmt.Errorf("store: save pass: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx's Rollback is a no-op

	res, err := savePassTx(ctx, tx, p, listings, s.retention())
	if err != nil {
		return SaveResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SaveResult{}, fmt.Errorf("store: save pass: commit: %w", err)
	}
	return res, nil
}

func savePassTx(ctx context.Context, tx *sql.Tx, p Pass, listings []avito.Listing, r Retention) (SaveResult, error) {
	var out SaveResult

	// The listings this watch believed were present before this pass. Read
	// first, because the membership update below overwrites it.
	previous := map[int64]bool{}
	rows, err := tx.QueryContext(ctx,
		`SELECT item_id FROM watch_members WHERE watch_id = ? AND missing_streak = 0`,
		p.WatchID)
	if err != nil {
		return out, fmt.Errorf("store: save pass: previous members: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		previous[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	for _, l := range listings {
		if !previous[l.ItemID] {
			out.Arrivals++
		}
	}

	passID, err := insertPass(ctx, tx, p, len(listings), out.Arrivals)
	if err != nil {
		return out, err
	}
	out.PassID = passID

	// Rank is the index, one-based. avito.Listing carries no position field on
	// purpose: rank belongs to the watch that saw the listing, not to the
	// listing, and two watches can see the same one at different ranks.
	for i, l := range listings {
		if err := upsertListing(ctx, tx, l, p.StartedAt); err != nil {
			return out, err
		}
		wrote, err := maybeSnapshot(ctx, tx, l, p, r)
		if err != nil {
			return out, err
		}
		if wrote {
			out.Snapshots++
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO pass_items (pass_id, position, item_id) VALUES (?, ?, ?)`,
			passID, i+1, l.ItemID); err != nil {
			return out, fmt.Errorf("store: save pass: pass_items: %w", err)
		}
	}

	if err := updateMembers(ctx, tx, p, listings); err != nil {
		return out, err
	}
	return out, nil
}
```

Then the four helpers in the same file:

```go
func insertPass(ctx context.Context, tx *sql.Tx, p Pass, itemsSeen, arrivals int) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, http_status,
		   path, mode, session, egress, elapsed_ms, bytes, items_seen,
		   total_count, arrivals, is_seed, evidential, degraded_why, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.WatchID, p.StartedAt, p.FinishedAt, p.Page, string(p.Surface), p.Class,
		p.HTTPStatus, p.Path, p.Mode, p.Session, p.Egress, p.ElapsedMS, p.Bytes,
		itemsSeen, p.TotalCount, arrivals, p.IsSeed, p.Evidential,
		p.DegradedWhy, p.Error)
	if err != nil {
		return 0, fmt.Errorf("store: save pass: passes: %w", err)
	}
	return res.LastInsertId()
}

// upsertListing writes the stable half.
//
// repost_key is written once at insert and never updated: a title edited after
// a link was made must not move the key that link was made against. The
// ON CONFLICT clause therefore names its columns rather than being a blanket
// update — a blanket one is how that rule gets lost in a later edit.
func upsertListing(ctx context.Context, tx *sql.Tx, l avito.Listing, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO listings (item_id, thread_id, url_path, title, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET
		    url_path     = excluded.url_path,
		    title        = excluded.title,
		    last_seen_at = excluded.last_seen_at`,
		l.ItemID, l.ItemID, l.URLPath, l.Title, now, now)
	if err != nil {
		return fmt.Errorf("store: save pass: listings: %w", err)
	}
	return nil
}

// maybeSnapshot writes a row only when the fingerprint moved or an anchor is
// due, and reports whether it wrote one.
func maybeSnapshot(ctx context.Context, tx *sql.Tx, l avito.Listing, p Pass, r Retention) (bool, error) {
	fp := Fingerprint(l)

	var lastFP string
	var lastTS int64
	err := tx.QueryRowContext(ctx, `
		SELECT fingerprint, ts FROM listing_snapshots
		WHERE item_id = ? AND surface = ?
		ORDER BY ts DESC, id DESC LIMIT 1`,
		l.ItemID, string(p.Surface)).Scan(&lastFP, &lastTS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No row has ever been written: the first reading of anything is a
		// change from nothing.
	case err != nil:
		return false, fmt.Errorf("store: save pass: last snapshot: %w", err)
	}

	changed := fp != lastFP
	anchor := !changed && AnchorDue(lastTS, p.StartedAt, r.AnchorEvery)
	if !changed && !anchor {
		return false, nil
	}

	var minor *int64
	if l.PriceKind == avito.PriceExact {
		v := l.Price.Minor
		minor = &v
	}
	currency := l.Price.Currency
	if currency == "" {
		currency = "RUB"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, anchor, fingerprint, source, path,
		   price_kind, price_minor, currency, price_label, title)
		VALUES (?, ?, ?, ?, ?, 'search', ?, ?, ?, ?, ?, ?)`,
		l.ItemID, string(p.Surface), p.StartedAt, anchor, fp, p.Path,
		string(l.PriceKind), minor, currency, l.PriceLabel, l.Title)
	if err != nil {
		return false, fmt.Errorf("store: save pass: listing_snapshots: %w", err)
	}
	return true, nil
}

// updateMembers moves the watch's membership set forward.
//
// Present listings reset their streak and record the rank they were seen at;
// absent ones advance theirs. last_position and window_size together are the
// displacement account: in a date-sorted window of k slots, an item at rank r
// is pushed out by k − r arrivals, so an absence explained by arrivals is not
// evidence of anything. Increment 4 does that arithmetic; this writes what it
// needs to do it with.
//
// Rows are never deleted here. A member row is the evidence behind every
// disappearance, and deleting it is how "why was I told this was gone" becomes
// unanswerable a month later.
func updateMembers(ctx context.Context, tx *sql.Tx, p Pass, listings []avito.Listing) error {
	seen := make([]int64, 0, len(listings))
	for i, l := range listings {
		seen = append(seen, l.ItemID)
		position := i + 1
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO watch_members
			  (watch_id, item_id, first_seen_at, last_seen_at, last_position,
			   window_size, missing_streak)
			VALUES (?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT(watch_id, item_id) DO UPDATE SET
			    last_seen_at   = excluded.last_seen_at,
			    last_position  = excluded.last_position,
			    window_size    = excluded.window_size,
			    missing_streak = 0`,
			p.WatchID, l.ItemID, p.StartedAt, p.StartedAt, position, len(listings)); err != nil {
			return fmt.Errorf("store: save pass: watch_members: %w", err)
		}
	}

	// Everything else in this watch is one pass more absent. Only an
	// evidential pass may advance the counter: a soft block or a mangled body
	// is our failure to read, not the market's decision to remove, and letting
	// it count is exactly how a monitor announces removals that never
	// happened.
	if !p.Evidential {
		return nil
	}
	q := `UPDATE watch_members SET missing_streak = missing_streak + 1
	      WHERE watch_id = ?`
	args := []any{p.WatchID}
	if len(seen) > 0 {
		q += ` AND item_id NOT IN (` + placeholders(len(seen)) + `)`
		for _, id := range seen {
			args = append(args, id)
		}
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store: save pass: absence: %w", err)
	}
	return nil
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
```

Add `"errors"` and `"strings"` to the imports. Add a `retention()` accessor on `Store` returning `DefaultRetention()` for now; Task 7 makes it settable.

If `listings` in migration 0001 has no `first_seen_at`/`last_seen_at`, read the spec's DDL from line 725 and use the real column names.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS. `TestSavePassIsAtomic` is the one to watch: if it fails, the transaction is not covering everything and the increment's central claim is untrue.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/pass.go internal/store/pass_test.go && git commit -m "$(cat <<'MSG'
feat(store): SavePass, one transaction over everything a pass implies

One BEGIN covers the pass row, its pass_items, the listings it saw, a
snapshot for each whose fingerprint moved or whose anchor is due, and the
membership set. A partial pass is worse than no pass: increment 4
differences consecutive passes, and a pass whose pass_items are missing
reads as every listing in the watch having vanished at once.

Two rules that look like details and are not. Only an evidential pass
advances an absence counter — a soft block is our failure to read, not
the market's decision to remove, and letting it count is exactly how a
monitor announces removals that never happened. And member rows are never
deleted: a member row is the evidence behind a disappearance, and
deleting it is how "why was I told this was gone" becomes unanswerable a
month later.

MSG
)"
```

---

## Task 6: `Volume` and the free-space precheck

**Files:**
- Create: `internal/store/volume.go`, `internal/store/volume_test.go`

**Interfaces:**
- Produces: `type Volume struct { Bytes int64; Rows map[string]int64 }`; `func (v Volume) String() string`; `func (s *Store) Volume(ctx context.Context) (Volume, error)`; `func (s *Store) CheckSpace(ctx context.Context) error`; `var ErrDiskLow error`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/volume_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"testing"
)

// TestVolumeCountsEveryTable. Nobody administers this product, and without a
// number they can look at, the first time they learn the history is growing is
// the day the disk fills.
func TestVolumeCountsEveryTable(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3))

	v, err := s.Volume(ctx)
	if err != nil {
		t.Fatalf("Volume: %v", err)
	}
	if v.Bytes <= 0 {
		t.Errorf("Bytes = %d, want the real file size", v.Bytes)
	}
	for table, want := range map[string]int64{
		"watches": 1, "passes": 1, "pass_items": 3,
		"listings": 3, "listing_snapshots": 3, "watch_members": 3,
	} {
		if v.Rows[table] != want {
			t.Errorf("Rows[%s] = %d, want %d", table, v.Rows[table], want)
		}
	}
	if _, ok := v.Rows["changes"]; !ok {
		t.Error("an empty table is missing from the report; a table that only " +
			"appears once it has rows is a table nobody notices growing")
	}
	if s := v.String(); !strings.Contains(s, "passes") {
		t.Errorf("String() = %q; it is what the volume command prints", s)
	}
}

// TestCheckSpaceRefusesBelowTheFloor. Collection stops below 256 MB free or
// below two percent of the volume, whichever is larger — before the write, not
// after SQLITE_FULL.
func TestCheckSpaceRefusesBelowTheFloor(t *testing.T) {
	s := openTemp(t)
	// The real filesystem under a temp dir will not be near full, so the floor
	// is raised instead of the disk being filled.
	s.SetSpaceFloor(1 << 62)
	if err := s.CheckSpace(context.Background()); err == nil {
		t.Error("CheckSpace passed with the floor above the whole volume")
	}
	s.SetSpaceFloor(0)
	if err := s.CheckSpace(context.Background()); err != nil {
		t.Errorf("CheckSpace failed with no floor: %v", err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Volume|CheckSpace' -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write `volume.go`**

Lift `Volume` from `S:\Projects\WB_GO\internal\store\vacuum.go` — read it first and keep its shape, its comment and its table-listing query. Then add the free-space half, which WB_GO does not have:

```go
// spaceFloorBytes is the absolute floor. The rule is the larger of this and
// two percent of the volume: a 256 MB floor on a 4 TB disk is no floor at all,
// and two percent of a 16 GB one is 320 MB, so neither number works alone.
const spaceFloorBytes = 256 << 20

// ErrDiskLow says collection must stop before it writes, rather than after
// SQLITE_FULL says so from the other side.
//
// It is raised where a person will meet it — a persistent banner and a
// collector-silent change in increment 4 — and not as a log line on a machine
// nobody has a terminal open to.
var ErrDiskLow = errors.New("store: not enough free space to collect")

// CheckSpace runs before each pass transaction.
func (s *Store) CheckSpace(ctx context.Context) error {
	free, total, err := diskFree(s.path)
	if err != nil {
		// A filesystem that will not answer is not a reason to stop
		// collecting. It is a reason to say so once and carry on: refusing to
		// work because a statfs failed would be the guard causing the outage
		// it exists to prevent.
		return nil
	}
	floor := s.spaceFloor()
	if pct := total / 50; pct > floor {
		floor = pct
	}
	if free < floor {
		return fmt.Errorf("%w: %d MB free, floor is %d MB",
			ErrDiskLow, free>>20, floor>>20)
	}
	return nil
}
```

`diskFree` needs a per-platform implementation. Create two files:

`internal/store/disk_windows.go`:

```go
//go:build windows

package store

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func diskFree(path string) (free, total int64, err error) {
	p, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return 0, 0, err
	}
	var freeToCaller, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return int64(freeToCaller), int64(totalBytes), nil
}
```

`internal/store/disk_unix.go`:

```go
//go:build !windows

package store

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

func diskFree(path string) (free, total int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
```

`golang.org/x/sys` is already an indirect dependency and the spec budgets it as direct. Add `Store.path` (set in `Open`), `Store.floor atomic.Int64`, `SetSpaceFloor(int64)` and `spaceFloor() int64` returning `spaceFloorBytes` when unset.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && go get golang.org/x/sys && gofmt -w ./internal && go build ./... && go test ./internal/store/ -run 'Volume|CheckSpace' -v
```

Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add go.mod go.sum internal/store/ && git commit -m "$(cat <<'MSG'
feat(store): Volume, and the free-space guard that runs before the write

Volume is WB_GO's, lifted, and its own comment is the argument: nobody
administers this product, and without a number they can look at, the
first time they learn the history is growing is the day the disk fills.

The guard is new. Collection stops below 256 MB free or below two percent
of the volume, whichever is larger — a 256 MB floor on a 4 TB disk is no
floor at all, and two percent of a 16 GB one is 320 MB, so neither number
works alone.

A statfs that fails is not a reason to stop collecting. Refusing to work
because the filesystem would not answer would be the guard causing the
outage it exists to prevent.

MSG
)"
```

---

## Task 7: Retention — three rules with three different biases

**Files:**
- Create: `internal/store/retention.go`, `internal/store/retention_test.go`

**Interfaces:**
- Produces: `func (s *Store) SetRetention(r Retention)`; `func (s *Store) ThinSnapshots(ctx context.Context, now int64) (int64, error)`; `func (s *Store) ThinPasses(ctx context.Context, now int64) (int64, error)`; `func (s *Store) SweepBodies(ctx context.Context, now int64) (int64, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/retention_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

const day = int64(86400)

// TestThinSnapshotsLeavesOnePerDayPastThirtyAndOnePerWeekPastAYear.
func TestThinSnapshotsLeavesOnePerDayPastThirtyAndOnePerWeekPastAYear(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := int64(400 * 24 * 3600)

	// Four snapshots inside one day, 100 days ago: past thirty, inside a year.
	insertSnapshots(t, s, 1, now-100*day, 4, 3600)
	// Four inside one day, 500 days ago: past a year.
	insertSnapshots(t, s, 2, now-500*day, 4, 3600)
	// Four inside one day, yesterday: inside the fresh window, untouched.
	insertSnapshots(t, s, 3, now-day, 4, 3600)

	if _, err := s.ThinSnapshots(ctx, now); err != nil {
		t.Fatalf("ThinSnapshots: %v", err)
	}
	for _, c := range []struct {
		item int64
		want int
		why  string
	}{
		{1, 1, "past thirty days, one row per day"},
		{2, 1, "past a year, one row per week"},
		{3, 4, "inside the fresh window, nothing is thinned"},
	} {
		var n int
		s.DB().QueryRow(`SELECT count(*) FROM listing_snapshots WHERE item_id = ?`, c.item).Scan(&n)
		if n != c.want {
			t.Errorf("item %d has %d rows, want %d (%s)", c.item, n, c.want, c.why)
		}
	}
}

// TestThinningKeepsTheChangeAndDropsTheAnchor.
//
// An anchor carries the same values as the change before it under a later,
// arbitrary timestamp. Keeping it would preserve the value and lose the moment
// it moved — which is the only thing the series is for.
func TestThinningKeepsTheChangeAndDropsTheAnchor(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := int64(400 * 24 * 3600)
	old := now - 100*day

	insertSnapshot(t, s, 1, old, false, "fp-change")
	insertSnapshot(t, s, 1, old+3600, true, "fp-change") // an anchor, same values, later

	if _, err := s.ThinSnapshots(ctx, now); err != nil {
		t.Fatal(err)
	}
	var anchor int
	if err := s.DB().QueryRow(
		`SELECT anchor FROM listing_snapshots WHERE item_id = 1`).Scan(&anchor); err != nil {
		t.Fatalf("both rows are gone: %v", err)
	}
	if anchor != 0 {
		t.Error("thinning kept the anchor and dropped the change; the anchor " +
			"carries the same values under a later timestamp, so keeping it " +
			"preserves the value and loses the moment it moved")
	}
}

// TestFailedPassesAreKeptAndHealthyOnesAreThinned is the rule that is
// deliberately not the obvious one.
//
// Keeping only passes that produced a change preferentially destroys the
// evidence that collection FAILED while preserving the evidence that it
// worked, and makes "was collection healthy in early March" unanswerable three
// weeks later.
func TestFailedPassesAreKeptAndHealthyOnesAreThinned(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	now := int64(400 * 24 * 3600)
	old := now - 60*day

	// Ten healthy passes in one hour, sixty days ago.
	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class = "ok"
		insertBarePass(t, s, p)
	}
	// Ten failed ones in the same hour.
	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class = "throttled"
		p.Evidential = false
		insertBarePass(t, s, p)
	}

	if _, err := s.ThinPasses(ctx, now); err != nil {
		t.Fatalf("ThinPasses: %v", err)
	}
	var ok, bad int
	s.DB().QueryRow(`SELECT count(*) FROM passes WHERE class = 'ok'`).Scan(&ok)
	s.DB().QueryRow(`SELECT count(*) FROM passes WHERE class = 'throttled'`).Scan(&bad)

	if ok != 1 {
		t.Errorf("healthy passes = %d past thirty days, want 1 per day", ok)
	}
	if bad != 10 {
		t.Errorf("failed passes = %d, want all 10 — the health history is the "+
			"only thing that explains what happened, and it must not be the "+
			"first thing retention destroys", bad)
	}
}

// TestEmptyIsThinnedAsHealthy. Reading the rule as "not ok" would keep every
// empty pass for ever, and a watch on a rare query would grow without bound
// precisely because nothing was happening on it.
func TestEmptyIsThinnedAsHealthy(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	now := int64(400 * 24 * 3600)
	old := now - 60*day

	for i := 0; i < 10; i++ {
		p := aPass(wid, old+int64(i)*300)
		p.Class = "empty"
		insertBarePass(t, s, p)
	}
	if _, err := s.ThinPasses(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB().QueryRow(`SELECT count(*) FROM passes WHERE class = 'empty'`).Scan(&n)
	if n != 1 {
		t.Errorf("empty passes = %d, want 1 — a query that legitimately "+
			"returned nothing is as healthy as one that returned fifty", n)
	}
}

// TestSweepBodiesIsBoundedByAgeAndBySize. An archive with an age rule and no
// size rule is unbounded on a broken install, which is exactly when it grows
// fastest.
func TestSweepBodiesIsBoundedByAgeAndBySize(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	now := int64(400 * 24 * 3600)

	insertBody(t, s, "old", now-40*day, 1<<10)
	insertBody(t, s, "recent-big-1", now-day, 400<<20)
	insertBody(t, s, "recent-big-2", now-2*day, 400<<20)

	r := DefaultRetention()
	s.SetRetention(r)
	if _, err := s.SweepBodies(ctx, now); err != nil {
		t.Fatalf("SweepBodies: %v", err)
	}

	var has int
	s.DB().QueryRow(`SELECT count(*) FROM bodies WHERE sha256 = 'old'`).Scan(&has)
	if has != 0 {
		t.Error("a body past BodyMaxAge survived")
	}
	var total int64
	s.DB().QueryRow(`SELECT coalesce(sum(length(body)), 0) FROM bodies`).Scan(&total)
	if total > r.BodyMaxBytes {
		t.Errorf("archive is %d bytes, over the %d-byte bound", total, r.BodyMaxBytes)
	}
	// The oldest of the two big ones is what went.
	s.DB().QueryRow(`SELECT count(*) FROM bodies WHERE sha256 = 'recent-big-1'`).Scan(&has)
	if has != 1 {
		t.Error("the size sweep took the newer body; it must take the oldest first")
	}
}

// TestABodyDeletionKeepsThePassThatReferredToIt. passes.body_sha is nullable
// with ON DELETE SET NULL so the sweep loses the page and keeps the reading. A
// reference that can never be dropped is a garbage collector that can never
// fire.
func TestABodyDeletionKeepsThePassThatReferredToIt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	now := int64(400 * 24 * 3600)

	insertBody(t, s, "doomed", now-40*day, 1<<10)
	p := aPass(wid, now-40*day)
	p.Class = "throttled"
	id := insertBarePassWithBody(t, s, p, "doomed")

	if _, err := s.SweepBodies(ctx, now); err != nil {
		t.Fatal(err)
	}
	var sha *string
	if err := s.DB().QueryRow(`SELECT body_sha FROM passes WHERE id = ?`, id).Scan(&sha); err != nil {
		t.Fatalf("the pass row went with its body: %v", err)
	}
	if sha != nil {
		t.Errorf("body_sha = %q after the body was deleted, want NULL", *sha)
	}
}
```

The helpers go at the bottom of the same file. They are direct `INSERT`s and
not calls to `SavePass` on purpose: retention must be tested against rows a
test placed deliberately, so that a change to `SavePass` cannot silently change
what retention is being tested against.

```go
func insertSnapshot(t *testing.T, s *Store, item, ts int64, anchor bool, fp string) {
	t.Helper()
	_, err := s.DB().Exec(`
		INSERT INTO listing_snapshots
		  (item_id, surface, ts, anchor, fingerprint, source, path, price_kind, currency)
		VALUES (?, 'mobile', ?, ?, ?, 'search', 'dom', 'exact', 'RUB')`,
		item, ts, anchor, fp)
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
}

// insertSnapshots writes n rows for one item, step seconds apart, each with a
// distinct fingerprint so every one of them counts as a real change.
func insertSnapshots(t *testing.T, s *Store, item, start int64, n int, step int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		insertSnapshot(t, s, item, start+int64(i)*step, false,
			"fp-"+strconv.Itoa(int(item))+"-"+strconv.Itoa(i))
	}
}

func insertBarePass(t *testing.T, s *Store, p Pass) int64 {
	t.Helper()
	return insertBarePassWithBody(t, s, p, "")
}

func insertBarePassWithBody(t *testing.T, s *Store, p Pass, sha string) int64 {
	t.Helper()
	var body any
	if sha != "" {
		body = sha
	}
	res, err := s.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, http_status,
		   evidential, body_sha)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, ?, ?)`,
		p.WatchID, p.StartedAt, p.FinishedAt, p.Class, p.HTTPStatus,
		p.Evidential, body)
	if err != nil {
		t.Fatalf("insert pass: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertBody(t *testing.T, s *Store, sha string, ts int64, size int) {
	t.Helper()
	_, err := s.DB().Exec(
		`INSERT INTO bodies (sha256, created_at, body) VALUES (?, ?, ?)`,
		sha, ts, make([]byte, size))
	if err != nil {
		t.Fatalf("insert body: %v", err)
	}
}
```

Add `"strconv"` to the file's imports. If `bodies` in migration 0001 names its
columns differently, read the spec's DDL at line 1224 and match the real ones
rather than changing the migration.

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Thin|Sweep|Body' -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write `retention.go`**

`thinSnapshotsSQL` is lifted from `S:\Projects\WB_GO\internal\store\retention.go` with `(nm_id, dest, app_type)` substituted for `(item_id, surface)` and `snapshots` for `listing_snapshots`. Copy its long comment too — the `ORDER BY anchor ASC, ts DESC, id DESC` explanation is the rule, and a reader who does not have it will "simplify" the ordering.

```go
// thinPassesSQL keeps one healthy pass per watch per period and deletes the
// rest. Only 'ok' and 'empty' are candidates.
//
// The class filter is a whitelist and not "class <> 'ok'", and the difference
// is not stylistic. Reading the rule as "not ok" would keep every empty pass
// for ever, and a watch on a rare query would grow without bound precisely
// because nothing was happening on it. Reading it as a whitelist means a class
// added to the Go catalogue later is kept by default, which is the safe
// direction: keeping too much is a disk-space problem and losing the health
// history is an unanswerable question.
const thinPassesSQL = `
DELETE FROM passes
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY
                       watch_id,
                       CASE WHEN started_at < ? THEN 1 ELSE 0 END,
                       CASE WHEN started_at < ?
                            THEN started_at / 86400
                            ELSE started_at / 3600
                       END
                   ORDER BY started_at DESC, id DESC
               ) AS rn
        FROM passes
        WHERE started_at < ? AND class IN ('ok', 'empty')
    )
    WHERE rn > 1
)`
```

`SweepBodies` runs the age rule first and the size rule second, deleting oldest-first until the archive is under `BodyMaxBytes`. Both bounds are read from `Retention`; neither may be zero — if either is, use `DefaultRetention()`'s value and say so in a comment rather than treating zero as "unbounded".

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/retention.go internal/store/retention_test.go && git commit -m "$(cat <<'MSG'
feat(store): retention — three rules, three different biases

Snapshots thin by period, keeping the change and dropping the anchor: an
anchor carries the same values under a later, arbitrary timestamp, so
keeping it would preserve the value and lose the moment it moved.

Passes thin the OPPOSITE way to the obvious rule. Keeping only passes
that produced a change preferentially destroys the evidence that
collection failed while preserving the evidence that it worked, and makes
"was collection healthy in early March" unanswerable three weeks later.
So every pass that is neither ok nor empty is kept, and only healthy
no-change passes are thinned. The filter is a whitelist rather than
"not ok": the latter would keep every empty pass for ever, and a watch on
a rare query would grow without bound precisely because nothing was
happening on it.

Bodies are bounded twice, by age and by size. An archive with an age rule
and no size rule is unbounded on a broken install, which is exactly when
it grows fastest. passes.body_sha is ON DELETE SET NULL, so the sweep
loses the page and keeps the reading.

thinSnapshotsSQL is WB_GO's, with (item_id, surface) for
(nm_id, dest, app_type).

MSG
)"
```

---

## Task 8: `Reclaim` — the fixed order

**Files:**
- Modify: `internal/store/retention.go`
- Test: `internal/store/reclaim_test.go`

**Interfaces:**
- Produces: `type ReclaimReport struct { Freed int64; ReachedFailedPasses bool; Steps []string }`; `func (s *Store) Reclaim(ctx context.Context, now, target int64) (ReclaimReport, error)`.

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestReclaimReachesFailedPassesLast.
//
// The obvious emergency reclaim — an age cut over passes — is wrong twice
// over. It deletes precisely the failed passes retention keeps for ever, and
// it deletes them at the worst possible moment: a disk filling is CORRELATED
// with collection being broken, because a blocked install is what writes both
// the failure rows and the archived bodies. So the naive sweep destroys the
// health history exactly when it is the only thing that explains what
// happened.
func TestReclaimReachesFailedPassesLast(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	now := int64(400 * 24 * 3600)

	insertBody(t, s, "b1", now-40*day, 8<<20)
	for i := 0; i < 40; i++ {
		p := aPass(wid, now-60*day+int64(i)*300)
		p.Class = "ok"
		insertBarePass(t, s, p)
	}
	for i := 0; i < 5; i++ {
		p := aPass(wid, now-60*day+int64(i)*300)
		p.Class = "throttled"
		insertBarePass(t, s, p)
	}

	// A target small enough that the first step alone reaches it.
	rep, err := s.Reclaim(ctx, now, 1<<20)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if rep.ReachedFailedPasses {
		t.Error("Reclaim reached failed passes on a target the body sweep alone " +
			"could satisfy; they are what it reaches last")
	}
	if len(rep.Steps) == 0 || rep.Steps[0] != "bodies" {
		t.Errorf("Steps = %v, want bodies first", rep.Steps)
	}
	var bad int
	s.DB().QueryRow(`SELECT count(*) FROM passes WHERE class = 'throttled'`).Scan(&bad)
	if bad != 5 {
		t.Errorf("failed passes = %d, want all 5 kept", bad)
	}
}

// TestReclaimStopsAtTheFirstStepThatReachesTheTarget.
func TestReclaimStopsAtTheFirstStepThatReachesTheTarget(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	insertBody(t, s, "big", int64(400*24*3600)-40*day, 32<<20)

	rep, err := s.Reclaim(ctx, int64(400*24*3600), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Steps) != 1 {
		t.Errorf("Steps = %v, want exactly one — the target was reached by the "+
			"first", rep.Steps)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run Reclaim -v
```

Expected: FAIL to build.

- [ ] **Step 3: Implement `Reclaim`**

```go
// ReclaimReport says what was done and, in one field, the thing a person needs
// to be told.
type ReclaimReport struct {
	Freed               int64
	ReachedFailedPasses bool
	Steps               []string
}

// Reclaim frees space in a fixed order and stops at the first step that
// reaches its target.
//
// The order is the whole design. Archived bodies first, because they are the
// largest bytes-per-row and the least irreplaceable. Then thinnable ok and
// empty passes. Then listing_snapshots past their retention windows. Failed
// passes are what it reaches last, and the panel says so when it has had to —
// a disk filling is correlated with collection being broken, so the health
// history is at its most valuable exactly when a naive sweep would destroy it.
//
// WAL means every step must checkpoint, or a DELETE grows the file it was run
// to shrink.
func (s *Store) Reclaim(ctx context.Context, now, target int64) (ReclaimReport, error) {
	var rep ReclaimReport
	before, err := s.fileBytes()
	if err != nil {
		return rep, err
	}
	steps := []struct {
		name string
		run  func() (int64, error)
	}{
		{"bodies", func() (int64, error) { return s.SweepBodies(ctx, now) }},
		{"passes", func() (int64, error) { return s.ThinPasses(ctx, now) }},
		{"snapshots", func() (int64, error) { return s.ThinSnapshots(ctx, now) }},
	}
	for _, st := range steps {
		if _, err := st.run(); err != nil {
			return rep, err
		}
		rep.Steps = append(rep.Steps, st.name)
		if err := s.checkpoint(ctx); err != nil {
			return rep, err
		}
		after, err := s.fileBytes()
		if err != nil {
			return rep, err
		}
		rep.Freed = before - after
		if rep.Freed >= target {
			return rep, nil
		}
	}
	// Every ordinary step has run and the target is still not met. Say so
	// rather than deleting the health history quietly.
	rep.ReachedFailedPasses = true
	return rep, nil
}
```

Note what this does **not** do: it stops short of deleting failed passes and reports that it had to. Deleting them is a person's decision, taken in the panel in increment 6, and the flag is what puts the question in front of them.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/store/ && git commit -m "$(cat <<'MSG'
feat(store): Reclaim, in the order that keeps the health history

Bodies, then thinnable ok and empty passes, then snapshots past their
windows — stopping at the first step that reaches its target.

The obvious reclaim, an age cut over passes, is wrong twice over. It
deletes precisely the failed passes retention keeps for ever, and it
deletes them at the worst possible moment: a disk filling is correlated
with collection being broken, because a blocked install is what writes
both the failure rows and the archived bodies. It also frees almost
nothing, since passes.body_sha references bodies and not the reverse, so
dropping a pass row orphans its body and leaves the bytes on disk.

When every ordinary step has run and the target is still unmet, Reclaim
says so and stops rather than deleting the health history quietly. That
deletion is a person's decision.

Every step checkpoints: under WAL a DELETE grows the file it was run to
shrink.

MSG
)"
```

---

## Task 9: `avitomon watch add` and `run --once` — the increment's demonstrable behaviour

**Files:**
- Create: `cmd/avitomon/watch.go`, `cmd/avitomon/run.go`
- Modify: `cmd/avitomon/main.go` — route `watch` and `run`

**Interfaces:**
- Consumes: everything above, plus `transport.NewFetcher`, `bt.Open`, `direct.Open` from increment 2.
- Produces: the binary, and the increment's demonstrable behaviour.

- [ ] **Step 1: Write `watch.go`**

The URL is stored verbatim. Parsing it into `query`, `category_id` and the rest
is increment 4's `avito.SearchURL`, which does not exist yet, and inventing half
of it here would be a second parser to keep in step with the real one.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

func watchCmd(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: avitomon watch add [flags] <url>")
	}
	fs := flag.NewFlagSet("watch add", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	name := fs.String("name", "", "what to call it")
	surface := fs.String("surface", "mobile", "mobile|desktop")
	interval := fs.Int("interval", 300, "seconds between passes (floor 120)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("exactly one URL is required")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now().Unix()
	id, err := s.AddWatch(ctx, store.Watch{
		Name: *name, Kind: "search", URL: fs.Arg(0),
		Surface: avito.Surface(*surface), MaxPages: 1,
		IntervalSec: *interval, AbsenceConfirmations: 2,
		AbsenceMinSpanSec: 1200,
		// Due immediately. A watch somebody just added and that does nothing
		// for five minutes reads as broken.
		NextDueAt: now,
		Enabled:   true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	fmt.Printf("watch %d added
", id)
	return nil
}
```

- [ ] **Step 2: Write `run.go`**

`avitomon run --once [-db FILE] [-key K] [-control URL] [-proxies FILE]` does exactly one thing: read the transport mode from `settings`, open that Leaser, take `DueWatches(now, 1)`, fetch, and `SavePass`. It must:

- **Refuse to start with no transport chosen.** `ErrNoTransportChosen` prints the question and exits 2, rather than picking one.
- **Call `CheckSpace` before the fetch, not after.** A pass that cannot be written is a request spent for nothing, and the request is the scarce thing.
- **On a `*transport.HaltError`, call `MarkHalted` and print the boundary message.** The watch stays halted; this program never clears it.
- **Set `next_due_at = started_at + interval_sec`** on a pass that completed, whatever its class. A watch that stops being scheduled because it failed once is a watch that never recovers.

```go
func runOnce(ctx context.Context, s *store.Store, f *transport.Fetcher, w store.Watch, now int64) error {
	if err := s.CheckSpace(ctx); err != nil {
		return err
	}
	started := now
	res, ferr := f.Fetch(ctx, w.URL)

	p := store.Pass{
		WatchID: w.ID, StartedAt: started, FinishedAt: time.Now().Unix(),
		Page: 1, Surface: w.Surface,
		Class: string(res.Verdict.Class), HTTPStatus: res.Status,
		Path: string(res.Page.Report.Path), Mode: string(f.Mode()),
		Session: res.Session, Egress: res.Egress,
		TotalCount: res.Page.TotalCount,
		// evidential is computed properly in increment 4, where the
		// admissibility tests live. Here it is the honest floor: a class that
		// answered, and an extraction that produced a container.
		Evidential: res.Verdict.Class == transport.ClassOK ||
			res.Verdict.Class == transport.ClassEmpty,
	}
	if ferr != nil {
		p.Error = ferr.Error()
	}
	if _, err := s.SavePass(ctx, p, res.Page.Listings); err != nil {
		return err
	}
	var halt *transport.HaltError
	if errors.As(ferr, &halt) {
		return s.MarkHalted(ctx, w.ID, halt.Verdict.Wall.Marker, p.FinishedAt)
	}
	return s.SetNextDue(ctx, w.ID, started+int64(w.IntervalSec))
}
```

Three small additions this needs, each one line:

- `func (s *Store) SetNextDue(ctx context.Context, id, at int64) error` in
  `watches.go`.
- `func (f *Fetcher) Mode() transport.Mode` in `internal/transport/ladder.go`,
  returning `f.leaser.Mode()`. The pass row needs it and the ladder is the only
  thing holding the leaser.
- **`Egress string` on `transport.Result`**, set beside `Session` and `Label`
  from `lease.Egress()`. The `passes` table has both an `egress` column and a
  `session` column, and `Result` currently carries only `Label` — which names
  the *endpoint*, not the exit. Writing the label into the egress column would
  repeat in the database exactly the bug increment 2 removed from the pacer, and
  it would be worse here: the pacer's copy was a live rate that could be
  corrected, and this one is history that cannot.

```go
// in Fetch, beside the two lines that already record the session and label
res.Session, res.Label, res.Egress = lease.Session(), lease.Label(), lease.Egress()
```

- [ ] **Step 3: Build and run the increment's demonstrable behaviour**

```bash
cd S:/Projects/Avito_Go && go build ./... && \
  rm -f /tmp/demo.db && \
  go run ./cmd/avitomon watch add -db /tmp/demo.db -name iphone \
    "https://m.avito.ru/all?q=iphone+15&s=104"
```

Expected: `watch 1 added`.

```bash
cd S:/Projects/Avito_Go && go run ./cmd/avitomon run --once -db /tmp/demo.db
```

Expected on a database with no transport chosen: exit 2 and the question, **not** a fetch. That is decision 3 working.

```bash
cd S:/Projects/Avito_Go && go run ./cmd/avitomon transport set -db /tmp/demo.db direct && \
  go run ./cmd/avitomon run --once -db /tmp/demo.db && \
  go run ./cmd/avitomon volume -db /tmp/demo.db
```

Expected: one pass written, and `volume` printing the file size with `passes 1`, `pass_items ~28`, `listings ~28`, `listing_snapshots ~28`, `watch_members ~28`.

- [ ] **Step 4: Prove retention against a synthetic year**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'Thin|Reclaim|Sweep' -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS. This is the increment's third demonstrable: *running retention against a synthetic year leaves one row per day past thirty days and one per week past a year.*

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd/avitomon/ internal/store/ internal/transport/ && git commit -m "$(cat <<'MSG'
feat(cmd): watch add and run --once — one pass, end to end

The first time a fetch and the database meet. run --once reads the
transport mode from settings, opens that leaser, takes one due watch,
fetches it and writes the pass.

Four rules it follows, each of which is a way this could go wrong:

  With no transport chosen it prints the question and exits, rather than
  picking one. That is decision 3, and the command line is where it lives
  until the panel exists.

  CheckSpace runs BEFORE the fetch. A pass that cannot be written is a
  request spent for nothing, and the request is the scarce thing.

  A halt marks the watch halted and this program never clears it.

  next_due_at advances on any completed pass, whatever its class. A watch
  that stops being scheduled because it failed once is a watch that never
  recovers.

evidential here is the honest floor — a class that answered and an
extraction that produced a container. The real admissibility tests are
increment 4's, where the lifecycle that needs them lives.

MSG
)"
```

---

## Done when

- `go build ./...` and `go vet ./...` are clean; `go test ./...` passes.
- `TestPackageReachesNoNetwork` still passes: nothing in this increment reached into `avito/`.
- `blanktrail`'s own suite passes, `TestPackageNamesNoTarget` included.
- Every table is `STRICT`, no `ON DELETE RESTRICT` exists, and exactly two millisecond columns do — all three proved against the applied schema.
- `avitomon run --once` on a database with no transport chosen exits without fetching.
- Two identical consecutive passes write three `listing_snapshots` rows in total, not six.
- A failed `SavePass` leaves zero rows in all four tables it writes.
- Retention over a synthetic year leaves one snapshot per day past thirty days and one per week past a year, keeps every failed pass, and thins `empty` alongside `ok`.
- `Reclaim` with a target the body sweep alone can meet does not touch passes.

## What increment 4 inherits

`passes` rows with `arrivals`, `items_seen` and `evidential`; `pass_items` as the raw membership evidence; `watch_members` carrying `last_position`, `window_size` and `missing_streak` — the displacement account, written but not yet reasoned over. Increment 4's `internal/lifecycle` does the arithmetic: in a date-sorted window of k slots an item at rank r is pushed out by k − r arrivals, so an absence explained by arrivals must not advance anything. `evidential` becomes the real admissibility test there and replaces the floor Task 9 writes.

Nothing in this increment knows what a change is, and nothing in it should learn.
