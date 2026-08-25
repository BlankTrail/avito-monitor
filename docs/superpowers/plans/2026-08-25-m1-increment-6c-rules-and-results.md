# M1 increment 6C — Правила, Результаты, and the export

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A person can edit the rules that decide what they are told about, see
every decision those rules made and why, and take the collected data out as CSV,
XLSX or JSON — from a browser, with no command line. This closes increment 6's
own demonstrable: **the whole product is usable from a browser.**

**Architecture:** `internal/export` lifted from the donor with its field
catalogue swapped for this target's, three formats instead of six, and a
streaming read from the store so a hundred thousand rows never sit in memory.
Two screens on 6A's shell.

**Tech Stack:** Go 1.26, `archive/zip` and `encoding/xml` (standard library) for
the XLSX, `encoding/csv`, `encoding/json`. No new dependency.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §The
panel: «**Правила** and **Результаты** are WB_GO's, with the field catalogue
swapped: CSV, XLSX and JSON from one column selection, streaming, through the
lifted `internal/export`.»

**Predecessors:** 6A (`4975b74`) and 6B (`81ea6b9`), both merged.

## Global Constraints

- **Go 1.26, no CGO.** `modernc.org/sqlite` only. **This plan adds no dependency**
  — no spreadsheet library, for the reason the chart has no plotting library.
- **All user-facing copy is Russian**, correct orthography, «ё» where it belongs.
- **One selection gives identical columns in every format.** That is the claim
  the export exists to keep, and it is what makes a format's options checkable:
  an option is refused when the output would silently be missing something asked
  for, and ignored when it only names formatting some other format applies.
- **Money is written from integer minor units and never from a float.** The
  single highest-consequence line in this codebase.
- **Streaming.** A row is written and forgotten. An export that built a slice
  first would hold a hundred thousand listings in memory on a machine already
  running a scraper.
- **Secrets never reach a page, a log, an error or an export.**

---

## What already exists

**From 6A/6B:** the `web.Server` shell, `esc`/`attr`/`field`/`checkbox`/`note`/
`writeFragment`, `page`, `render`, `s.tabs` (now matching by prefix), `num`,
`ago`, `internal/ru.Plural`.

**In `internal/rules`, and edited by this plan's screen:** `Rule{ID, Name, Kind,
Condition Node, Scope, Urgent, ThresholdPct, ThresholdMinor, MinInterval,
Aggregate, Targets, Enabled, FromDefaults}`; `Node{Op, Nodes, Field, Cmp,
Value}`; `Fields()`, `FieldLabel(f)`, `Save`, `All`, `Rule.Validate`;
`ScopeListing|ScopeThread|ScopeSeller|ScopeWatch|ScopeFilter`; `Reason`
constants `quiet-hours|rate-limit|duplicate|threshold|seed|repost`.

**In `internal/store`:** `Rules`, `SaveRule`, `RuleEvents(ctx, limit)`,
`Targets`, `SaveTarget`, `EnabledTargets`, `Changes(ctx, watchID, since, limit)`,
`ListingNames`, `Watches`, `ThreadSeries`.

**In `internal/track`:** `Kinds()` — the closed catalogue of 22 change kinds —
and `Kind`, `Unit`.

**Does not exist and this plan adds it:** `internal/export` (the seam, the field
catalogue, three writers), `store.ExportRows` (the streaming read),
`internal/web/results.go`, `internal/web/rules.go`, and a `DeleteRule` on the
store.

**Local facts:** Bash heredocs eat backslashes; use Write/Edit for anything with
`\n` or `\t`. `GOPROXY` is `direct`, so tidying needs
`GOPROXY=https://proxy.golang.org,direct`.

## File structure

| file | responsible for |
|---|---|
| `internal/export/fields.go` | the column catalogue — what can be exported at all |
| `internal/export/export.go` | `Value`, `Writer`, `Options`, `Columns`, `Export` |
| `internal/export/format.go` | the name-to-writer map, in one place |
| `internal/export/csv.go` | the CSV writer, with its encoding and separator |
| `internal/export/json.go` | JSON and JSONL |
| `internal/export/xlsx.go` | the spreadsheet, written as a zip of XML |
| `internal/store/export.go` | `ExportRows`, streaming |
| `internal/web/results.go` | Результаты, its filter and its download |
| `internal/web/rules.go` | Правила, its editor, its targets and its log |

---

## Task 1: The column catalogue and the export seam

**Files:**
- Create: `internal/export/fields.go`, `internal/export/export.go`, `internal/export/format.go`, `internal/export/fields_test.go`, `internal/export/export_test.go`

The donor's `wb/fields.go` catalogue is about Wildberries and none of it
survives; the *shape* does, and so does every rule attached to it. This is the
one place in the plan where the lift is a rewrite rather than a copy.

**Interfaces:**
- Produces:

```go
// In internal/export:
type FieldType string
const (
    TypeText  FieldType = "text"
    TypeInt   FieldType = "int"
    TypeMoney FieldType = "money"
    TypeTime  FieldType = "time"
    TypeBool  FieldType = "bool"
)
type Field struct {
    Key   string     // travels in URLs and file headers; never changes once released
    Name  string     // what a person reads
    Group string     // the heading it sits under
    Type  FieldType
}
func Fields() []Field
func FieldByKey(key string) (Field, bool)
func Columns(selection []string) (cols []Field, unknown []string)

type Value struct {
    Absent   bool
    Text     string
    Int      int64
    Bool     bool
    Minor    int64  // money, in minor units, never divided
    Currency string
    Unix     int64  // seconds UTC, for TypeTime
}
type Options struct {
    Separator rune   // CSV only; default ','
    Decimal   rune   // CSV only; default '.'
    Encoding  string // "utf-8" | "utf-8-bom" | "windows-1251"
}
type Writer interface {
    Begin(columns []Field) error
    Write(values []Value) error
    Close() error
}
func NewWriter(format string, w io.Writer, o Options) (Writer, error)
func Extension(format string) (string, error)
func Export(ctx context.Context, rows iter.Seq2[Row, error], sel []string, w Writer) (int, error)

// Row is what the store streams. Declared here rather than in store so that
// internal/store does not learn what an export is.
type Row struct {
    ItemID     int64
    ThreadID   int64
    WatchID    int64
    WatchName  string
    Title      string
    URLPath    string
    PriceKind  string
    PriceMinor *int64
    Currency   string
    DiscountPct *int64
    SellerID   int64
    Position   int
    FirstSeenAt int64
    LastSeenAt  int64
    SnapshotAt  int64
    Present     bool
    IsXL        *bool
    IsReserved  *bool
}
```

- [ ] **Step 1: Write the failing test for the catalogue**

Create `internal/export/fields_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"strings"
	"testing"
)

// TestAKeyIsSafeInAUrlAndInAHeader, because it travels in both and a saved
// selection that names a key nobody recognises silently loses a column.
func TestAKeyIsSafeInAUrlAndInAHeader(t *testing.T) {
	for _, f := range Fields() {
		if f.Key == "" {
			t.Errorf("%q has no key", f.Name)
		}
		if f.Key != strings.ToLower(f.Key) {
			t.Errorf("key %q is not lowercase", f.Key)
		}
		if strings.ContainsAny(f.Key, " ,;\"'/?&=#") {
			t.Errorf("key %q carries a character that a URL or a CSV header would eat", f.Key)
		}
		if f.Name == "" {
			t.Errorf("key %q has no name for a person to read", f.Key)
		}
		if f.Group == "" {
			t.Errorf("key %q sits under no heading", f.Key)
		}
	}
}

// TestNoKeyAppearsTwice. Two fields with one key is a selection that cannot say
// which it meant.
func TestNoKeyAppearsTwice(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Fields() {
		if seen[f.Key] {
			t.Errorf("key %q is declared twice", f.Key)
		}
		seen[f.Key] = true
	}
}

// TestColumnsAreInCatalogueOrderAndNotInClickOrder.
//
// The walk is over the catalogue rather than over the selection, which is what
// makes two people who ticked the same boxes in different orders get the same
// file — and what makes a key ticked twice one column rather than two.
func TestColumnsAreInCatalogueOrderAndNotInClickOrder(t *testing.T) {
	all := Fields()
	if len(all) < 4 {
		t.Fatal("the catalogue is too small to test ordering")
	}
	first, second := all[0].Key, all[1].Key

	forward, unknown := Columns([]string{first, second})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %v", unknown)
	}
	backward, _ := Columns([]string{second, first, second})
	if len(forward) != 2 || len(backward) != 2 {
		t.Fatalf("forward %d, backward %d", len(forward), len(backward))
	}
	if forward[0].Key != backward[0].Key || forward[1].Key != backward[1].Key {
		t.Error("the column order followed the selection rather than the catalogue")
	}
}

// TestAnUnknownKeyIsReportedAndNotSilentlyDropped.
//
// A saved selection from another release must not lose a column without saying
// so — the person would find out from a spreadsheet with one column missing,
// months later, if at all.
func TestAnUnknownKeyIsReportedAndNotSilentlyDropped(t *testing.T) {
	cols, unknown := Columns([]string{Fields()[0].Key, "колонка-из-будущего"})
	if len(cols) != 1 {
		t.Errorf("cols = %v", cols)
	}
	if len(unknown) != 1 || unknown[0] != "колонка-из-будущего" {
		t.Errorf("unknown = %v", unknown)
	}
}

// TestAnEmptySelectionIsEveryColumn.
//
// «Выгрузить» with nothing ticked means «everything», not «nothing»: a file with
// no columns is the one output nobody can have meant, and it is what a link with
// no parameters produces.
func TestAnEmptySelectionIsEveryColumn(t *testing.T) {
	cols, unknown := Columns(nil)
	if len(cols) != len(Fields()) {
		t.Errorf("an empty selection gave %d columns of %d", len(cols), len(Fields()))
	}
	if len(unknown) != 0 {
		t.Errorf("unknown = %v", unknown)
	}
}

// TestEveryMoneyFieldIsTypedAsMoney.
//
// The type is what decides whether a value is divided by a hundred at the point
// of rendering. A price typed as an int is a hundredfold error that still sorts
// correctly and still looks plausible.
func TestEveryMoneyFieldIsTypedAsMoney(t *testing.T) {
	for _, f := range Fields() {
		looksLikeMoney := strings.Contains(f.Key, "price") ||
			strings.Contains(f.Key, "discount_minor")
		if looksLikeMoney && f.Type != TypeMoney {
			t.Errorf("%q is typed %q; a price rendered as a plain integer is a "+
				"hundredfold error that still looks plausible", f.Key, f.Type)
		}
	}
}
```

- [ ] **Step 2: Write `internal/export/fields.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package export writes what was collected, in the formats a person can open.
//
// Lifted in shape from WB_GO's internal/export: the Writer seam, the Value
// type, the catalogue-order rule and the option discipline are that package's
// and are unchanged. The catalogue itself is not — that one is about
// Wildberries — and three formats are carried rather than six, because the spec
// asks for CSV, XLSX and JSON and a SQL dump nobody asked for is a format to
// keep working for ever.
//
// One selection gives identical columns in every format. That is the claim this
// package exists to keep, and everything else here follows from it.
package export

// FieldType is what a value is, for whoever renders it.
//
// It is the type and not the column name that decides whether a number is
// divided by a hundred, which is why money has its own: a price rendered as a
// plain integer is a hundredfold error that still sorts correctly and still
// looks like a price.
type FieldType string

const (
	TypeText  FieldType = "text"
	TypeInt   FieldType = "int"
	TypeMoney FieldType = "money"
	TypeTime  FieldType = "time"
	TypeBool  FieldType = "bool"
)

// Field is one exportable column.
type Field struct {
	// Key identifies the field in a saved selection and names its export
	// column. Lowercase and free of spaces because it travels in URLs and in
	// file headers, and it must not change once released: a saved selection
	// naming a key nobody recognises silently loses a column.
	Key   string
	Name  string
	Group string
	Type  FieldType
}

// catalogue is the declaration itself.
//
// Order matters and nothing sorts it: it is the order of the checkboxes on the
// screen and of the columns in every file, so two people who ticked the same
// boxes in different orders get the same spreadsheet.
var catalogue = []Field{
	// What this row is about. First, because a file whose identity columns are
	// at the far right is a file somebody has to scroll to read.
	{Key: "item_id", Name: "Номер объявления", Group: "Объявление", Type: TypeInt},
	{Key: "thread_id", Name: "Номер сквозной", Group: "Объявление", Type: TypeInt},
	{Key: "title", Name: "Заголовок", Group: "Объявление", Type: TypeText},
	{Key: "url", Name: "Ссылка", Group: "Объявление", Type: TypeText},

	{Key: "price", Name: "Цена", Group: "Цена", Type: TypeMoney},
	{Key: "price_kind", Name: "Что с ценой", Group: "Цена", Type: TypeText},
	{Key: "discount_pct", Name: "Скидка, %", Group: "Цена", Type: TypeInt},

	{Key: "seller_id", Name: "Продавец", Group: "Продавец", Type: TypeInt},

	{Key: "watch", Name: "Наблюдение", Group: "Наблюдение", Type: TypeText},
	{Key: "position", Name: "Позиция в выдаче", Group: "Наблюдение", Type: TypeInt},
	{Key: "present", Name: "Сейчас в выдаче", Group: "Наблюдение", Type: TypeBool},

	{Key: "first_seen", Name: "Впервые увидели", Group: "Время", Type: TypeTime},
	{Key: "last_seen", Name: "Последний раз видели", Group: "Время", Type: TypeTime},
	{Key: "snapshot_at", Name: "Снимок сделан", Group: "Время", Type: TypeTime},

	{Key: "is_xl", Name: "Продвигается", Group: "Признаки", Type: TypeBool},
	{Key: "is_reserved", Name: "Забронировано", Group: "Признаки", Type: TypeBool},
}

// Fields is every exportable column, in catalogue order.
func Fields() []Field {
	out := make([]Field, len(catalogue))
	copy(out, catalogue)
	return out
}

// FieldByKey finds one field. The bool is false for a key the catalogue does
// not have, which is a saved selection from a newer release.
func FieldByKey(key string) (Field, bool) {
	for _, f := range catalogue {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// Groups is every heading, in the order the catalogue introduces them.
func Groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range catalogue {
		if !seen[f.Group] {
			seen[f.Group] = true
			out = append(out, f.Group)
		}
	}
	return out
}

// Columns turns a selection into the column set, in catalogue order.
//
// The walk is over the catalogue rather than over the selection, which is what
// makes the order the catalogue's and not the order somebody clicked in. It also
// makes a key named twice one column rather than two.
//
// An empty selection is every column. «Выгрузить» with nothing ticked means
// everything, not nothing: a file with no columns is the one output nobody can
// have meant, and it is exactly what a link with no parameters produces.
//
// Unknown keys are reported and never silently dropped: a saved selection from
// another release must not lose a column without saying so, or the person finds
// out from a spreadsheet missing a column, months later, if at all.
func Columns(selection []string) (cols []Field, unknown []string) {
	if len(selection) == 0 {
		return Fields(), nil
	}
	want := make(map[string]bool, len(selection))
	for _, key := range selection {
		want[key] = true
	}
	for _, f := range catalogue {
		if want[f.Key] {
			cols = append(cols, f)
			delete(want, f.Key)
		}
	}
	for _, key := range selection {
		if want[key] {
			unknown = append(unknown, key)
			delete(want, key)
		}
	}
	return cols, unknown
}
```

- [ ] **Step 3: Write the failing test for the seam**

Create `internal/export/export_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"context"
	"errors"
	"iter"
	"testing"
)

// recorder is a Writer that remembers what it was given.
type recorder struct {
	columns []Field
	rows    [][]Value
	closed  bool
	failOn  int // fail Write on this row index; -1 never
}

func (r *recorder) Begin(cols []Field) error { r.columns = cols; return nil }
func (r *recorder) Write(v []Value) error {
	if r.failOn == len(r.rows) {
		return errors.New("writer refused")
	}
	r.rows = append(r.rows, append([]Value(nil), v...))
	return nil
}
func (r *recorder) Close() error { r.closed = true; return nil }

func rows(list ...Row) iter.Seq2[Row, error] {
	return func(yield func(Row, error) bool) {
		for _, r := range list {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func minor(v int64) *int64 { return &v }

func aRow() Row {
	return Row{
		ItemID: 7800000001, ThreadID: 7800000001, WatchID: 1,
		WatchName: "iphone 15", Title: "iPhone 15, 128 ГБ",
		URLPath: "/m/x_7800000001",
		PriceKind: "exact", PriceMinor: minor(4_000_000), Currency: "RUB",
		SellerID: 42, Position: 3,
		FirstSeenAt: 1_700_000_000, LastSeenAt: 1_700_003_600,
		SnapshotAt:  1_700_003_600, Present: true,
	}
}

// TestOneValuePerColumnAlways.
//
// The Writer contract says len(values) equals len(columns), and every format
// leans on it: a CSV row short by one is a file whose columns shift from that
// row down, and nothing about it looks wrong until somebody sorts it.
func TestOneValuePerColumnAlways(t *testing.T) {
	rec := &recorder{failOn: -1}
	n, err := Export(context.Background(), rows(aRow(), aRow()),
		[]string{"item_id", "price", "title"}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Export wrote %d rows", n)
	}
	if len(rec.columns) != 3 {
		t.Fatalf("columns = %v", rec.columns)
	}
	for i, r := range rec.rows {
		if len(r) != len(rec.columns) {
			t.Errorf("row %d has %d values for %d columns", i, len(r), len(rec.columns))
		}
	}
}

// TestMoneyTravelsAsMinorUnitsAndIsNeverDividedHere.
//
// The division belongs to the format, at the point of rendering, once. A Value
// carrying roubles would make every writer's correctness depend on whether some
// earlier one had already divided.
func TestMoneyTravelsAsMinorUnitsAndIsNeverDividedHere(t *testing.T) {
	rec := &recorder{failOn: -1}
	if _, err := Export(context.Background(), rows(aRow()), []string{"price"}, rec); err != nil {
		t.Fatal(err)
	}
	got := rec.rows[0][0]
	if got.Minor != 4_000_000 {
		t.Errorf("Minor = %d, want the kopecks untouched", got.Minor)
	}
	if got.Currency != "RUB" {
		t.Errorf("Currency = %q", got.Currency)
	}
}

// TestAnAbsentValueIsAbsentAndNotZero.
//
// A listing with no price and a listing priced at nothing are different facts,
// and a spreadsheet showing 0 for the first is a spreadsheet somebody sorts by
// price and draws a wrong conclusion from.
func TestAnAbsentValueIsAbsentAndNotZero(t *testing.T) {
	r := aRow()
	r.PriceKind, r.PriceMinor = "negotiable", nil
	r.DiscountPct = nil

	rec := &recorder{failOn: -1}
	if _, err := Export(context.Background(), rows(r),
		[]string{"price", "discount_pct"}, rec); err != nil {
		t.Fatal(err)
	}
	for i, v := range rec.rows[0] {
		if !v.Absent {
			t.Errorf("column %d came back present: %+v", i, v)
		}
	}
}

// TestTheWriterIsClosedEvenWhenARowFails.
//
// A half-written file with no end is worse than none: a ZIP without its central
// directory does not open at all, and a person who downloaded it has no way to
// tell it apart from a slow connection.
func TestTheWriterIsClosedEvenWhenARowFails(t *testing.T) {
	rec := &recorder{failOn: 1}
	_, err := Export(context.Background(), rows(aRow(), aRow(), aRow()),
		[]string{"item_id"}, rec)
	if err == nil {
		t.Fatal("Export hid the writer's refusal")
	}
	if !rec.closed {
		t.Error("the writer was not closed after a failed row")
	}
}

// TestAFailureFromTheSourceStopsTheExport, rather than writing a file that is
// silently short.
func TestAFailureFromTheSourceStopsTheExport(t *testing.T) {
	bad := func(yield func(Row, error) bool) {
		yield(aRow(), nil)
		yield(Row{}, errors.New("база не отвечает"))
	}
	rec := &recorder{failOn: -1}
	if _, err := Export(context.Background(), bad, []string{"item_id"}, rec); err == nil {
		t.Fatal("a source failure produced a complete-looking file")
	}
	if !rec.closed {
		t.Error("the writer was not closed")
	}
}

// TestAnUnknownColumnStopsTheExportRatherThanShrinkingIt.
//
// The alternative is a file that is missing a column the person asked for, with
// nothing anywhere saying so.
func TestAnUnknownColumnStopsTheExportRatherThanShrinkingIt(t *testing.T) {
	rec := &recorder{failOn: -1}
	_, err := Export(context.Background(), rows(aRow()),
		[]string{"item_id", "колонка-из-будущего"}, rec)
	if err == nil {
		t.Fatal("an unknown column was quietly dropped")
	}
	if got := err.Error(); !contains(got, "колонка-из-будущего") {
		t.Errorf("err = %q; it does not name the column", got)
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
```

Replace that hand-rolled `contains` with `strings.Contains` and the import — it
is written out here only so the block compiles as given; a hand-rolled substring
search in a test is noise.

- [ ] **Step 4: Write `internal/export/export.go` and `format.go`**

`export.go` holds `Value`, `Options`, `Writer`, `Row`, `valueOf` and `Export`.
The shape is the donor's; the body of `valueOf` is this catalogue's:

```go
// valueOf reads one field out of one row.
//
// The switch is over the key and not over the type, because two fields of one
// type come from different places. A key with no case here is a field declared
// in the catalogue and never wired, which Export refuses rather than writing a
// column of blanks — a blank column reads as «this listing has no seller», and
// the truth is «this build forgot to fill it in».
func valueOf(r Row, f Field) (Value, bool) {
	switch f.Key {
	case "item_id":
		return Value{Int: r.ItemID}, true
	case "thread_id":
		return Value{Int: r.ThreadID}, true
	case "title":
		return Value{Text: r.Title}, true
	case "url":
		if r.URLPath == "" {
			return Value{Absent: true}, true
		}
		return Value{Text: "https://www.avito.ru" + r.URLPath}, true
	case "price":
		if r.PriceMinor == nil {
			return Value{Absent: true}, true
		}
		return Value{Minor: *r.PriceMinor, Currency: r.Currency}, true
	case "price_kind":
		return Value{Text: priceKindLabel(r.PriceKind)}, true
	case "discount_pct":
		return optInt(r.DiscountPct), true
	case "seller_id":
		if r.SellerID == 0 {
			return Value{Absent: true}, true
		}
		return Value{Int: r.SellerID}, true
	case "watch":
		return Value{Text: r.WatchName}, true
	case "position":
		return Value{Int: int64(r.Position)}, true
	case "present":
		return Value{Bool: r.Present}, true
	case "first_seen":
		return optTime(r.FirstSeenAt), true
	case "last_seen":
		return optTime(r.LastSeenAt), true
	case "snapshot_at":
		return optTime(r.SnapshotAt), true
	case "is_xl":
		return optBool(r.IsXL), true
	case "is_reserved":
		return optBool(r.IsReserved), true
	}
	return Value{}, false
}

// priceKindLabel renders the three-valued price as a word.
//
// «exact» is not shown at all: it is the ordinary case and a column repeating
// «точная» on every row is a column nobody reads. The other two are what a
// person filters on.
func priceKindLabel(kind string) string {
	switch kind {
	case "negotiable":
		return "договорная"
	case "absent":
		return "не прочитана"
	}
	return ""
}

func optInt(p *int64) Value {
	if p == nil {
		return Value{Absent: true}
	}
	return Value{Int: *p}
}

func optBool(p *bool) Value {
	if p == nil {
		return Value{Absent: true}
	}
	return Value{Bool: *p}
}

// optTime treats zero as absent. Nothing in this store was collected in 1970,
// and a spreadsheet column of 01.01.1970 is worse than an empty one.
func optTime(ts int64) Value {
	if ts <= 0 {
		return Value{Absent: true}
	}
	return Value{Unix: ts}
}
```

and `Export` itself:

```go
// Export streams rows through a writer and returns how many it wrote.
//
// Streaming: a row is written and forgotten. Building a slice first would hold
// a hundred thousand listings in memory on a machine that is already running a
// scraper.
//
// The writer is closed whatever happens, including on the source failing. A
// half-written file with no end is worse than none — a ZIP missing its central
// directory does not open at all, and a person who downloaded it cannot tell it
// apart from a slow connection.
func Export(ctx context.Context, rows iter.Seq2[Row, error], sel []string, w Writer) (n int, err error) {
	defer func() {
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}()

	cols, unknown := Columns(sel)
	if len(unknown) > 0 {
		// Refused rather than shrunk: a file missing a column somebody asked
		// for, with nothing saying so, is the failure this reporting exists to
		// prevent.
		return 0, fmt.Errorf("export: этой сборке неизвестны колонки: %s",
			strings.Join(unknown, ", "))
	}
	for _, f := range cols {
		if _, ok := valueOf(Row{}, f); !ok {
			return 0, fmt.Errorf("export: колонка %q объявлена и не заполняется", f.Key)
		}
	}
	if err := w.Begin(cols); err != nil {
		return 0, err
	}

	values := make([]Value, len(cols))
	for row, rerr := range rows {
		if rerr != nil {
			return n, rerr
		}
		if err := ctx.Err(); err != nil {
			return n, err
		}
		for i, f := range cols {
			values[i], _ = valueOf(row, f)
		}
		if err := w.Write(values); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
```

`format.go` is the donor's, with three names instead of six:

```go
// NewWriter is the writer for a format name.
//
// An empty name is CSV, deliberately: it is what a link with no format parameter
// means, and refusing it would make the first thing a person tries the one that
// fails.
//
// Three formats and not the donor's six. The spec asks for CSV, XLSX and JSON;
// a SQLite file and two SQL dialects are three more outputs to keep working for
// ever, for a use case nobody in this product has stated.
func NewWriter(format string, w io.Writer, o Options) (Writer, error) {
	switch format {
	case "csv", "":
		return NewCSV(w, o)
	case "json":
		return NewJSON(w, o)
	case "jsonl":
		return NewJSONL(w, o)
	case "xlsx":
		return NewXLSX(w, o)
	}
	return nil, fmt.Errorf("формат %q эта сборка не знает", format)
}

// Extension is the file suffix a format is opened by.
//
// Not cosmetic: it is what decides whether the thing arriving in a downloads
// folder opens in a spreadsheet or in a text editor, and a name without it opens
// in neither.
func Extension(format string) (string, error) {
	switch format {
	case "csv", "":
		return "csv", nil
	case "json":
		return "json", nil
	case "jsonl":
		return "jsonl", nil
	case "xlsx":
		return "xlsx", nil
	}
	return "", fmt.Errorf("формат %q эта сборка не знает", format)
}
```

- [ ] **Step 5: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/export/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS. The `NewCSV`/`NewJSON`/`NewXLSX` referenced by
`format.go` do not exist yet, so either write `format.go` in Task 2 instead, or
add three one-line stubs returning an error and delete them there. **Prefer the
first** — a stub that returns «not implemented» from a file called format.go is
a lie about which task built it.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/export && \
  git commit -m "feat(export): the column catalogue and the seam that keeps one selection honest"
```

---

## Task 2: CSV and JSON

**Files:**
- Create: `internal/export/csv.go`, `internal/export/json.go`, `internal/export/format.go`, `internal/export/csv_test.go`, `internal/export/json_test.go`

Both lifted from the donor with the `wb.Field` references swapped for this
package's `Field`. The donor's `csv.go` carries a windows-1251 encoder for
Russian Excel; **check whether this project already has one** before lifting a
second:

```bash
cd S:/Projects/Avito_Go && ls internal/cp1251 2>/dev/null; grep -rn "1251" --include=*.go . | head -3
```

If it does not, lift `S:/Projects/WB_GO/internal/cp1251` as part of this task and
say in the commit that it came with the CSV rather than being a package of its
own idea.

- [ ] **Step 1: Write the failing tests**

Create `internal/export/csv_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func csvOf(t *testing.T, o Options, sel []string, list ...Row) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewCSV(&buf, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(context.Background(), rows(list...), sel, w); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestTheHeaderIsWhatAPersonReadsAndNotTheKey.
//
// «Цена» in the header and «price» in the URL. A spreadsheet whose columns are
// named in code identifiers is a spreadsheet somebody has to be told how to read.
func TestTheHeaderIsWhatAPersonReadsAndNotTheKey(t *testing.T) {
	got := csvOf(t, Options{}, []string{"item_id", "price"}, aRow())
	head := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(head, "Цена") {
		t.Errorf("header = %q", head)
	}
	if strings.Contains(head, "price") {
		t.Errorf("header carries the key rather than the name: %q", head)
	}
}

// TestMoneyIsWrittenInRoublesAndFromTheIntegerAlone.
//
// Four million kopecks is forty thousand roubles. The division happens once,
// here, from an integer — a float would round a kopeck somewhere in the tenth
// thousand and nobody would ever see which row.
func TestMoneyIsWrittenInRoublesAndFromTheIntegerAlone(t *testing.T) {
	got := csvOf(t, Options{}, []string{"price"}, aRow())
	if !strings.Contains(got, "40000.00") {
		t.Errorf("csv = %q; want 40000.00", got)
	}
	if strings.Contains(got, "4000000") {
		t.Error("the kopecks were written as roubles")
	}
}

// TestTheDecimalSeparatorIsAKnobAndNotDerivedFromTheEncoding.
//
// Russian Excel reads a comma and would show every price as text without it —
// but somebody exporting for another program to parse is entitled to a point,
// and guessing on their behalf would take that away.
func TestTheDecimalSeparatorIsAKnobAndNotDerivedFromTheEncoding(t *testing.T) {
	got := csvOf(t, Options{Decimal: ',', Separator: ';'}, []string{"price"}, aRow())
	if !strings.Contains(got, "40000,00") {
		t.Errorf("csv = %q", got)
	}
}

// TestAnAbsentValueIsAnEmptyCellAndNotAZero.
func TestAnAbsentValueIsAnEmptyCellAndNotAZero(t *testing.T) {
	r := aRow()
	r.PriceMinor = nil
	got := csvOf(t, Options{}, []string{"item_id", "price"}, r)
	line := strings.Split(strings.TrimSpace(got), "\n")[1]
	if !strings.HasSuffix(line, ",") && !strings.HasSuffix(line, ";") {
		t.Errorf("row = %q; an absent price is an empty cell", line)
	}
	if strings.Contains(line, ",0") || strings.Contains(line, ";0") {
		t.Errorf("row = %q; an absent price was written as zero", line)
	}
}

// TestATitleWithASeparatorInItDoesNotBreakTheFile, which is every third listing
// on this site.
func TestATitleWithASeparatorInItDoesNotBreakTheFile(t *testing.T) {
	r := aRow()
	r.Title = `iPhone 15, 128 ГБ, "как новый"`
	got := csvOf(t, Options{}, []string{"title", "item_id"}, r)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("a title with a comma produced %d lines", len(lines))
	}
	if !strings.Contains(lines[1], `"`) {
		t.Errorf("the title was not quoted: %q", lines[1])
	}
}

// TestAByteOrderMarkIsWrittenWhenAskedFor, because Excel opens a UTF-8 file
// without one as windows-1251 and shows Russian as mojibake.
func TestAByteOrderMarkIsWrittenWhenAskedFor(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewCSV(&buf, Options{Encoding: "utf-8-bom"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(context.Background(), rows(aRow()), []string{"title"}, w); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("no byte-order mark")
	}
}

// TestAnEncodingThisBuildDoesNotHaveIsRefusedRatherThanIgnored.
//
// The line the option discipline draws: an option is refused when the output
// would silently be missing something asked for. A file written in UTF-8 while
// the person asked for KOI8-R is a file that opens as nonsense.
func TestAnEncodingThisBuildDoesNotHaveIsRefusedRatherThanIgnored(t *testing.T) {
	var buf bytes.Buffer
	if _, err := NewCSV(&buf, Options{Encoding: "koi8-r"}); err == nil {
		t.Error("an unknown encoding was accepted")
	}
}
```

`json_test.go` covers: an absent value is `null` and not `0`; money is a number
in roubles with two decimals and not a string; a JSON file is one array and a
JSONL file is one object per line; and the same selection gives the same keys in
both.

- [ ] **Step 2: Lift and adapt**

```bash
cd S:/Projects/Avito_Go && \
  cp S:/Projects/WB_GO/internal/export/csv.go S:/Projects/WB_GO/internal/export/json.go internal/export/ && \
  sed -i 's|wildberries-monitor|avito-monitor|g' internal/export/csv.go internal/export/json.go && \
  grep -n "wb\." internal/export/csv.go internal/export/json.go
```

Every `wb.Field` becomes `Field`; every `wb.FieldType`/`wb.Type*` becomes this
package's. Delete the `RawWriter` half of `json.go`: `IncludeRaw` is about
Wildberries' own response body, this product has no per-row payload, and an
interface with no implementor is an interface somebody will implement.

- [ ] **Step 3: Write `format.go`** (the block given in Task 1 Step 4).

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/export/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/export internal/cp1251 && \
  git commit -m "feat(export): CSV and JSON, with the decimal separator left as a choice"
```

---

## Task 3: XLSX

**Files:**
- Create: `internal/export/xlsx.go`, `internal/export/sheets.go`, `internal/export/xlsx_test.go`

The donor's, lifted. 444 + 190 lines, written against `archive/zip` and
`encoding/xml` for the reason the chart is written against `image/png`: an XLSX
is a zip of four small XML documents, and a spreadsheet library to produce them
is a spreadsheet library in a public AGPL repository.

- [ ] **Step 1: Write the failing test**

Create `internal/export/xlsx_test.go`. The assertions that matter:

```go
// TestTheFileIsAZipWithTheFourPartsAnXlsxNeeds.
//
// A spreadsheet is a container, and one missing part is a file Excel refuses to
// open at all rather than opening wrongly — so this checks the manifest rather
// than trusting the writer.
func TestTheFileIsAZipWithTheFourPartsAnXlsxNeeds(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewXLSX(&buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(context.Background(), rows(aRow()), nil, w); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range z.File {
		have[f.Name] = true
	}
	for _, want := range []string{
		"[Content_Types].xml", "_rels/.rels",
		"xl/workbook.xml", "xl/worksheets/sheet1.xml",
	} {
		if !have[want] {
			t.Errorf("the archive has no %s", want)
		}
	}
}

// TestATitleWithAnAmpersandDoesNotCorruptTheSheet.
//
// «Samsung & Co» is an ordinary title and a raw & is malformed XML, which Excel
// reports as «unreadable content» and offers to repair — losing the file.
func TestATitleWithAnAmpersandDoesNotCorruptTheSheet(t *testing.T) { /* … */ }

// TestMoneyIsANumberAndNotAString, so a spreadsheet can sum a column of prices.
func TestMoneyIsANumberAndNotAString(t *testing.T) { /* … */ }

// TestAnAbsentValueIsAnEmptyCell, so «no price» does not sort as zero.
func TestAnAbsentValueIsAnEmptyCellInASpreadsheet(t *testing.T) { /* … */ }
```

Fill the three elided bodies by reading the sheet XML out of the zip and
asserting on it — the donor's `xlsx_test.go` has helpers for exactly this; lift
them with the writer.

- [ ] **Step 2: Lift**

```bash
cd S:/Projects/Avito_Go && \
  cp S:/Projects/WB_GO/internal/export/xlsx.go S:/Projects/WB_GO/internal/export/sheets.go internal/export/ && \
  cp S:/Projects/WB_GO/internal/export/xlsx_test.go internal/export/ && \
  sed -i 's|wildberries-monitor|avito-monitor|g' internal/export/xlsx*.go internal/export/sheets.go && \
  gofmt -w ./internal/export && go build ./internal/export/
```

Then fix every `wb.` reference and every fixture that names a Wildberries field.
The donor's test file is large; keep the tests that are about XLSX and delete the
ones that are about its catalogue.

- [ ] **Step 3: Run and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/export/ -v 2>&1 | \
  grep -E "^(---|ok|FAIL)" && git add internal/export && \
  git commit -m "feat(export): XLSX, written as a zip of XML rather than by a library"
```

---

## Task 4: The streaming read

**Files:**
- Create: `internal/store/export.go`, `internal/store/export_test.go`

**Interfaces:**
- Produces:

```go
// In internal/store:
type ExportFilter struct {
    WatchID     int64 // 0 is every watch
    PresentOnly bool
    Since       int64 // snapshot_at floor; 0 is everything
    PriceMin    int64 // minor units; 0 is no floor
    PriceMax    int64
    Query       string // substring of the title, case-insensitive
}
func (s *Store) ExportRows(ctx context.Context, f ExportFilter) iter.Seq2[export.Row, error]
func (s *Store) CountExportRows(ctx context.Context, f ExportFilter) (int, error)
```

**The dependency points one way and this is the one place it could go wrong.**
`internal/store` must not import `internal/export`, or the store learns what a
spreadsheet is. So `export.Row` is declared in `internal/export` and the store
returns it — which means the store *does* import export. Both directions are
wrong; the fix is that `Row` is a plain data struct and belongs to neither.

**Declare `Row` in `internal/store`** as `store.ExportRow`, and have
`internal/export` take a type parameter or its own interface? No — simpler and
truer: **`internal/export` declares `Row` and `internal/store` imports
`internal/export`.** The store already imports `internal/lifecycle` and
`internal/transport`; importing a sibling that holds one struct and no behaviour
is the same shape. The alternative — a third package for one struct — is a
package whose whole purpose is to avoid an import.

Write that reasoning into `internal/store/export.go`'s header comment, because a
reader will ask.

- [ ] **Step 1: Write the failing test**

`internal/store/export_test.go` asserts:

- **One row per listing, carrying its newest snapshot.** A listing with four
  snapshots is one row, not four: «Результаты» is what is there now, and the
  history is История's job.
- **The watch filter narrows it**, and a listing in two watches appears once per
  watch, because the position and the watch name differ.
- **`PresentOnly` excludes what has left the window**, and off, it includes it —
  a person exporting to find what disappeared needs the rows that are gone.
- **The price band is inclusive of both ends** and **excludes a listing whose
  price was not read**, matching `rules.Filter`.
- **The query matches a title case-insensitively**, including Cyrillic — «айфон»
  must match «Айфон».
- **The sequence stops on the first error** and does not yield a zero Row after
  it.
- **Nothing is loaded that is not yielded**: assert by exporting a hundred rows
  through a writer that counts, with `CountExportRows` agreeing.

- [ ] **Step 2: Write `internal/store/export.go`**

One query, ordered by `l.item_id`, joining `watch_members` to `listings` to the
newest `listing_snapshots` row per listing. The newest snapshot is a correlated
subquery on `max(ts)` rather than a window function, because SQLite's window
functions are available but a correlated subquery over `idx_listing_snapshots_item_ts`
is the index this schema already has.

`iter.Seq2` so the rows stream: the function opens the `*sql.Rows`, yields, and
closes in a `defer` inside the sequence.

- [ ] **Step 3: Run and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/store/ -v 2>&1 | \
  grep -E "^(---|ok|FAIL)" && git add internal/store && \
  git commit -m "feat(store): the export read, one row per listing and streamed"
```

---

## Task 5: Результаты

**Files:**
- Create: `internal/web/results.go`, `internal/web/results_test.go`
- Modify: `internal/web/server.go` (three routes, one tab)

Three routes: `GET /results` renders the page, `GET /results/table` answers the
table as a fragment for `data-load`, and `GET /results/export` streams the file.

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **The download carries a filename with the right extension**, in
  `Content-Disposition`, because a file called «export» opens in nothing.
- **The response streams**: assert that `Content-Length` is absent, which is what
  a streamed body looks like, and that a large export does not fail.
- **Every format is offered and each produces its own bytes** — a CSV that
  answers to `?format=xlsx` is worse than a refusal.
- **A format this build does not know is refused** with the name in the message.
- **The column selection survives into the file**, and an empty selection is
  every column.
- **The filter is on the page and in the export link**, so the file matches what
  is on screen. A download that ignored the filter is the failure this screen
  would be blamed for.
- **A title is escaped** in the table.
- **An export of nothing is a file with a header and no rows**, not a 404: a
  person who filtered to nothing asked a question and got an answer.

- [ ] **Step 2: Write `internal/web/results.go`**

The export handler:

```go
func (s *Server) exportResults(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	ext, err := export.Extension(format)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The header goes out before the first row, because the body is streamed:
	// there is no length to declare and no way to change the status once bytes
	// have moved. A failure mid-file is therefore a truncated download, which
	// is why Export closes its writer — a container without its end does not
	// open at all.
	w.Header().Set("Content-Type", contentType(format))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="avito-%s.%s"`,
			s.now().UTC().Format("2006-01-02"), ext))

	writer, err := export.NewWriter(format, w, optionsFrom(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := export.Export(r.Context(),
		s.Store.ExportRows(r.Context(), filterFrom(r)),
		r.URL.Query()["column"], writer); err != nil {
		// Nothing useful can be sent now — the status is gone. Logged where the
		// operator will see it, and the truncated file is its own signal.
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
	}
}
```

**The `NewWriter` call must come before the headers**, not after: a format whose
options are refused would otherwise answer 400 with a `Content-Disposition`
already set. Reorder it and say so in a comment.

- [ ] **Step 3: Run and commit**

---

## Task 6: Правила

**Files:**
- Create: `internal/web/rules.go`, `internal/web/rules_test.go`
- Modify: `internal/web/server.go` (routes and tab), `internal/store/rules.go` (add `DeleteRule`)

The screen edits `rules.Rule`: kind, scope, condition, thresholds, interval,
targets, urgent, aggregate, enabled. And it shows the log — `rule_events` — which
is the only answer the product has to «почему мне не сказали».

- [ ] **Step 1: Write the failing test**

The assertions that matter:

- **A rule that cannot match is refused with every reason at once**, which
  `Rule.Validate` already produces — the screen must show all of them, not the
  first.
- **The nine defaults are marked as such on screen**, so deleting one is an
  informed act. That is what `FromDefaults` is for and nothing has read it yet.
- **The log shows the reason a decision was suppressed**, in Russian, for each of
  the six reasons — `seed`, `repost`, `threshold`, `duplicate`, `rate-limit`,
  `quiet-hours` — and a fired decision shows as fired.
- **A rule with no target is refused** and says so.
- **The condition editor round-trips a two-leaf And**, because that is the shape
  the price defaults use and a tree that came back different would fire on
  something other than what its owner wrote.
- **Deleting a rule asks**, and its `rule_events` go with it — the schema
  cascades, and the screen must say so on the confirmation rather than let
  somebody discover it.
- **A target can be added, disabled and deleted**, and disabling one silences it
  everywhere at once (6A's engine already intersects; this is the screen for it).

- [ ] **Step 2: Write the screen**

The condition editor is the hard part and it is deliberately small: the tree
supports and/or groups and compare leaves, and the screen offers **one group of
leaves** — an And of comparisons. Deeper trees are storable and editable by hand
and not by this screen, which is stated on the page rather than hidden:

```go
	b.WriteString(note("neutral",
		"Здесь можно собрать условие из простых сравнений, соединённых «и». " +
			"Более сложные условия хранятся и работают, но редактируются пока " +
			"только через файл базы."))
```

A rule whose stored condition is deeper than this editor can draw is shown
read-only with that sentence, and is not silently flattened — flattening would
change what it fires on without telling anybody.

- [ ] **Step 3: Run and commit**

---

## Task 7: The demonstrable — the whole product from a browser

**Files:**
- Create: `docs/measurements/2026-08-XX-panel-6c.md`

This is increment 6's own demonstrable, and it is the one that has been waiting
since 6A: **the whole product is usable from a browser with no command line.**

- [ ] **Step 1: Full verification**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./cmd ./internal && go vet ./... && \
  go build ./... && go test ./... 2>&1 | grep -v "no test files"
```

- [ ] **Step 2: Do it from the browser, start to finish**

Fresh database. `avitomon panel` and nothing else on the command line:

1. Answer the transport question.
2. Add a proxy list, or skip it.
3. Add a watch.
4. Add a Telegram target.
5. Watch a pass land — this needs `avitomon run --once` from a terminal, **which
   is the gap**: M1 has no scheduler, so collection is still started by hand.
   Record that plainly; it is increment 6's demonstrable meeting a limit that
   belongs to the increment list rather than to this plan.
6. Edit a rule. Delete a default. See the log fill with decisions and reasons.
7. Filter Результаты and download each of CSV, XLSX and JSON. **Open the XLSX in
   a spreadsheet** — a zip that unzips is not a spreadsheet that opens.
8. Look at Здоровье сбора and История with real rows in them.

Record what happened, what did not, and — if the browser pane still does not
composite frames — say so in §1 as 6A and 6B did, and say which steps were
therefore driven over HTTP rather than clicked.

- [ ] **Step 3: Commit**

---

## Done when

- `gofmt -l` prints nothing; `go vet ./...` clean; `go test ./...` passes.
- One selection gives identical columns in CSV, XLSX and JSON, and a test says so
  by comparing the three headers.
- Money is written from integer minor units in every format.
- An absent value is an empty cell, a `null` and an empty CSV field — never a
  zero.
- An unknown column stops the export and names itself.
- The export streams: nothing builds a slice of rows.
- Правила edits every field of a rule, refuses one that cannot match with every
  reason at once, marks the defaults as defaults, and shows the log with each
  suppression reason in Russian.
- Результаты filters, and the download matches the filter.
- The XLSX **opens in a spreadsheet**, checked by a person.
- Increment 6's demonstrable is performed and written up, including the
  command-line step that remains.

## What this plan deliberately does not build

**A scheduler.** `avitomon run --once` is still how a pass happens. M1's
increment list has no scheduler in it; the panel can configure collection and
cannot start it on a timer. This is the one place increment 6's «no command
line» is not literally true, and it is named here rather than discovered in step
5 of the demonstrable.

**SQLite and SQL-dump exports.** The donor has both. The spec asks for three
formats; three more outputs to keep working for ever, for a use case nobody
stated, is not a trade this project makes.

**`IncludeRaw`.** The donor keeps Wildberries' own response beside each row. This
product has no per-row payload — `bodies` is per pass, not per listing — so the
option and its `RawWriter` interface are dropped rather than carried with no
implementor.

**A deep condition editor.** And/or nesting is storable, evaluable and tested;
the screen edits one And of comparisons and says so. A screen that silently
flattened a deeper tree would change what a rule fires on without telling its
owner.

**The tray binary and the service units.** Still built by none of 6A, 6B or 6C.
This is increment 6's remaining gap and it has now been named in three plans.

## Self-review

**Spec coverage.** §The panel's last paragraph is one sentence and this plan is
all of it: Правила (Task 6), Результаты (Task 5), the field catalogue swapped
(Task 1), CSV/XLSX/JSON from one column selection (Tasks 2–3), streaming (Tasks
1 and 4), through the lifted `internal/export` (Tasks 1–3).

**Placeholder scan.** Task 3 Step 1 elides three test bodies with `/* … */` and
says in prose to fill them from the donor's own helpers; Tasks 4, 5 and 6 give
their assertions as prose lists rather than as code. That is a deliberate
step down in precision for the three tasks that are lifts or that repeat shapes
already written twice in 6A and 6B — and it is a real weakness of this plan
compared with 6A's, stated here rather than hidden. An executor who wants the
6A level of detail for Task 6 should reread `internal/web/settings.go`, which is
the same shape.

**Type consistency.** `export.Field`, `export.Value`, `export.Options`,
`export.Writer` and `export.Row` are declared in Task 1 and used in Tasks 2–5.
`store.ExportFilter` is declared in Task 4 and used in Task 5. The
store-imports-export direction is settled in Task 4 with its reasoning, and it
is the one architectural decision in this plan that a reviewer should check
first.
