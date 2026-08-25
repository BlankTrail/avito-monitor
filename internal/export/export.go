// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"strings"
)

// Value is one cell, before any format has decided how to render it.
//
// Money is here as minor units and is never divided in this file. The division
// belongs to the format, at the point of rendering, once — a Value carrying
// roubles would make every writer's correctness depend on whether some earlier
// one had already divided.
type Value struct {
	// Absent is «there is no value», which is not «the value is zero». A
	// listing with no price and one priced at nothing are different facts, and
	// a spreadsheet showing zero for the first is one somebody sorts by price
	// and draws a wrong conclusion from.
	Absent   bool
	Text     string
	Int      int64
	Bool     bool
	Minor    int64
	Currency string
	// Unix is seconds UTC, for TypeTime.
	Unix int64
}

// Options is every knob the formats have between them.
//
// A format refuses an option naming content it cannot produce and ignores an
// option naming only formatting some other format applies. The line between the
// two is whether the output would silently be missing something asked for.
type Options struct {
	// Separator is the CSV column separator; zero means ','.
	Separator rune
	// Decimal is the CSV decimal separator; zero means '.'.
	//
	// Not derived from Encoding, deliberately. Russian Excel reads a comma and
	// would show every price as text without it, which is why the knob exists —
	// but somebody exporting for another program to parse is entitled to a
	// point, and guessing on their behalf would take that away.
	Decimal rune
	// Encoding is "utf-8", "utf-8-bom" or "windows-1251". CSV only.
	Encoding string
}

// Writer is the one shape every format implements.
//
// Three methods and no more. Begin gives it the column list to build its header
// from, Write gives it one row, Close finishes the output and must be safe to
// call after an error. Any method that assumed a file, a byte count or a
// seekable destination would have to be taken back out the day something
// batches rows into a remote sink.
type Writer interface {
	// Begin declares the columns. Called once, before any row.
	Begin(columns []Field) error
	// Write emits one row. len(values) always equals len(columns).
	Write(values []Value) error
	// Close finishes the output. Must be safe to call after an error.
	Close() error
}

// Row is one listing as it stands, with the context an export column needs.
//
// Declared here rather than in internal/store, and the store imports this
// package for it. Both directions look wrong at first — the store learning what
// a spreadsheet is, or this package learning what a database is — and neither
// happens: Row is a plain data struct with no behaviour, and the store already
// imports internal/lifecycle and internal/transport for the same kind of thing.
// A third package existing only to hold one struct and avoid an import would be
// a package whose whole purpose is bookkeeping.
type Row struct {
	ItemID    int64
	ThreadID  int64
	WatchID   int64
	WatchName string
	Title     string
	URLPath   string

	// PriceKind is «exact», «negotiable» or «absent». PriceMinor is nil unless
	// it is exact: the three are three different statements and an export that
	// collapsed them would lose the one a person filters on.
	PriceKind   string
	PriceMinor  *int64
	Currency    string
	DiscountPct *int64

	SellerID int64
	Position int

	FirstSeenAt int64
	LastSeenAt  int64
	SnapshotAt  int64
	Present     bool

	IsXL       *bool
	IsReserved *bool
}

// Export streams rows through a writer and returns how many it wrote.
//
// Streaming: a row is written and forgotten. Building a slice first would hold a
// hundred thousand listings in memory on a machine already running a scraper.
//
// The writer is closed whatever happens, the source failing included. A
// half-written file with no end is worse than none — a ZIP missing its central
// directory does not open at all, and whoever downloaded it cannot tell it apart
// from a slow connection.
func Export(ctx context.Context, rows iter.Seq2[Row, error], sel []string, w Writer) (n int, err error) {
	defer func() {
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}()

	cols, unknown := Columns(sel)
	if len(unknown) > 0 {
		// Refused rather than shrunk: a file missing a column somebody asked
		// for, with nothing anywhere saying so, is the failure this reporting
		// exists to prevent.
		return 0, fmt.Errorf("export: этой сборке неизвестны колонки: %s",
			strings.Join(unknown, ", "))
	}
	for _, f := range cols {
		if _, ok := valueOf(Row{}, f); !ok {
			// A key declared in the catalogue and never wired would write a
			// column of blanks, which reads as «this listing has no seller»
			// when the truth is «this build forgot to fill it in».
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

// valueOf reads one field out of one row.
//
// The switch is over the key and not over the type, because two fields of one
// type come from different places. The bool is false for a key with no case,
// which Export refuses before writing anything.
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
		if r.Position <= 0 {
			return Value{Absent: true}, true
		}
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
// «exact» is not shown at all: it is the ordinary case, and a column repeating
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

// columnHeaders is what a person reads: the field's name.
func columnHeaders(cols []Field) []string {
	out := make([]string, len(cols))
	for i, f := range cols {
		out[i] = f.Name
	}
	return out
}

// columnKeys is what a program reads: the field's key.
//
// JSON and JSONL use it. It is the half fields.go promises not to change
// between releases, while a name moves with the interface's wording.
func columnKeys(cols []Field) []string {
	out := make([]string, len(cols))
	for i, f := range cols {
		out[i] = f.Key
	}
	return out
}

// formatMoney renders minor units with the chosen decimal separator.
//
// Lifted from the donor unchanged, negation included. Negated through an
// unsigned magnitude: Go defines signed overflow as two's-complement
// wraparound, so -minor would in fact give the right bits even for
// math.MinInt64 — this is not a correctness fix. It is written this way because
// the magnitude is what the digits are taken from, and naming it as unsigned
// says so; the reader does not have to know the wraparound rule to see that the
// loop cannot produce a negative digit.
//
// Always two decimals. A price of forty thousand roubles exactly is «40000.00»
// and not «40000», because a column where some cells have decimals and some do
// not is a column a spreadsheet reads as text.
func formatMoney(minor int64, decimal rune) string {
	neg := minor < 0
	u := uint64(minor)
	if neg {
		u = uint64(-(minor + 1)) + 1
	}

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString(strconv.FormatUint(u/100, 10))
	b.WriteRune(decimal)
	frac := u % 100
	b.WriteByte(byte('0' + frac/10))
	b.WriteByte(byte('0' + frac%10))
	return b.String()
}
