// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"context"
	"errors"
	"iter"
	"strings"
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
		URLPath:   "/m/x_7800000001",
		PriceKind: "exact", PriceMinor: minor(4_000_000), Currency: "RUB",
		SellerID: 42, Position: 3,
		FirstSeenAt: 1_700_000_000, LastSeenAt: 1_700_003_600,
		SnapshotAt: 1_700_003_600, Present: true,
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
// and a spreadsheet showing 0 for the first is one somebody sorts by price and
// draws a wrong conclusion from.
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

// TestTheThreePriceKindsSurviveAsThreeStatements.
//
// The price column is empty for the two absences and the kind column says which
// it was. Collapsing them would lose the one a person filters on: «договорная»
// is the seller saying something, «не прочитана» is our defect.
func TestTheThreePriceKindsSurviveAsThreeStatements(t *testing.T) {
	cases := []struct {
		kind  string
		minor *int64
		want  string
	}{
		{"exact", minor(4_000_000), ""},
		{"negotiable", nil, "договорная"},
		{"absent", nil, "не прочитана"},
	}
	for _, c := range cases {
		r := aRow()
		r.PriceKind, r.PriceMinor = c.kind, c.minor

		rec := &recorder{failOn: -1}
		if _, err := Export(context.Background(), rows(r),
			[]string{"price", "price_kind"}, rec); err != nil {
			t.Fatal(err)
		}
		if got := rec.rows[0][1].Text; got != c.want {
			t.Errorf("%q rendered as %q, want %q", c.kind, got, c.want)
		}
		if c.minor == nil && !rec.rows[0][0].Absent {
			t.Errorf("%q left a price behind", c.kind)
		}
	}
}

// TestTheWriterIsClosedEvenWhenARowFails.
//
// A half-written file with no end is worse than none: a ZIP without its central
// directory does not open at all, and whoever downloaded it has no way to tell
// it apart from a slow connection.
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
		if !yield(aRow(), nil) {
			return
		}
		yield(Row{}, errors.New("база не отвечает"))
	}
	rec := &recorder{failOn: -1}
	if _, err := Export(context.Background(), bad, []string{"item_id"}, rec); err == nil {
		t.Fatal("a source failure produced a complete-looking file")
	}
	if !rec.closed {
		t.Error("the writer was not closed")
	}
	if len(rec.rows) != 1 {
		t.Errorf("%d rows written; the failing one must not have been", len(rec.rows))
	}
}

// TestAnUnknownColumnStopsTheExportRatherThanShrinkingIt.
//
// The alternative is a file missing a column the person asked for, with nothing
// anywhere saying so.
func TestAnUnknownColumnStopsTheExportRatherThanShrinkingIt(t *testing.T) {
	rec := &recorder{failOn: -1}
	_, err := Export(context.Background(), rows(aRow()),
		[]string{"item_id", "колонка-из-будущего"}, rec)
	if err == nil {
		t.Fatal("an unknown column was quietly dropped")
	}
	if !strings.Contains(err.Error(), "колонка-из-будущего") {
		t.Errorf("err = %q; it does not name the column", err)
	}
	if len(rec.columns) != 0 {
		t.Error("the writer was started before the columns were checked")
	}
}

// TestACancelledContextStopsMidStream, so closing a browser tab does not leave
// a query running over a hundred thousand rows.
func TestACancelledContextStopsMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	many := func(yield func(Row, error) bool) {
		for i := 0; i < 1000; i++ {
			if i == 2 {
				cancel()
			}
			if !yield(aRow(), nil) {
				return
			}
		}
	}
	rec := &recorder{failOn: -1}
	n, err := Export(ctx, many, []string{"item_id"}, rec)
	if err == nil {
		t.Fatal("a cancelled export reported success")
	}
	if n > 10 {
		t.Errorf("wrote %d rows after cancellation", n)
	}
	if !rec.closed {
		t.Error("the writer was not closed")
	}
}

// TestEveryCatalogueColumnIsActuallyFilled.
//
// A key declared and never wired writes a column of blanks, which reads as «this
// listing has no seller» when the truth is «this build forgot». Export refuses
// it; this walks the whole catalogue so the refusal never has to fire in front
// of a person.
func TestEveryCatalogueColumnIsActuallyFilled(t *testing.T) {
	for _, f := range Fields() {
		if _, ok := valueOf(aRow(), f); !ok {
			t.Errorf("column %q is declared and not filled", f.Key)
		}
	}
}
