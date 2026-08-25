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
// named in code identifiers is one somebody has to be told how to read.
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

// TestMoneyAlwaysCarriesItsDecimals.
//
// A column where some cells have decimals and some do not is a column a
// spreadsheet reads as text, and a text column cannot be summed — which is what
// somebody opens this file to do.
func TestMoneyAlwaysCarriesItsDecimals(t *testing.T) {
	r := aRow()
	r.PriceMinor = minor(4_000_000) // exactly forty thousand roubles
	if got := csvOf(t, Options{}, []string{"price"}, r); !strings.Contains(got, "40000.00") {
		t.Errorf("csv = %q", got)
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
	if !strings.Contains(got, ";") && !strings.Contains(got, "Цена") {
		t.Errorf("the separator was ignored: %q", got)
	}
}

// TestAnAbsentValueIsAnEmptyCellAndNotAZero.
func TestAnAbsentValueIsAnEmptyCellAndNotAZero(t *testing.T) {
	r := aRow()
	r.PriceMinor = nil
	got := csvOf(t, Options{}, []string{"item_id", "price"}, r)
	line := strings.Split(strings.TrimSpace(got), "\n")[1]
	if !strings.HasSuffix(strings.TrimRight(line, "\r"), ",") {
		t.Errorf("row = %q; an absent price is an empty cell", line)
	}
	if strings.Contains(line, ",0") {
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
		t.Fatalf("a title with a comma produced %d lines: %q", len(lines), got)
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

	var plain bytes.Buffer
	w, _ = NewCSV(&plain, Options{})
	if _, err := Export(context.Background(), rows(aRow()), []string{"title"}, w); err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(plain.Bytes(), []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("a mark was written when none was asked for")
	}
}

// TestWindows1251IsActuallyWindows1251, because a Russian title written as
// UTF-8 under that label is mojibake in the one program the option exists for.
func TestWindows1251IsActuallyWindows1251(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewCSV(&buf, Options{Encoding: "windows-1251"})
	if err != nil {
		t.Fatal(err)
	}
	r := aRow()
	r.Title = "Привет"
	if _, err := Export(context.Background(), rows(r), []string{"title"}, w); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("Привет")) {
		t.Error("the file carries UTF-8 under a windows-1251 label")
	}
	// П is 0xCF in windows-1251.
	if !bytes.Contains(buf.Bytes(), []byte{0xCF}) {
		t.Errorf("no windows-1251 bytes in %q", buf.Bytes())
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

// TestATimeSortsAsTextInTheOrderItSortsAsTime, which is the property a
// spreadsheet column needs most.
func TestATimeSortsAsTextInTheOrderItSortsAsTime(t *testing.T) {
	early, late := aRow(), aRow()
	early.SnapshotAt, late.SnapshotAt = 1_700_000_000, 1_800_000_000

	got := csvOf(t, Options{}, []string{"snapshot_at"}, early, late)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if lines[1] >= lines[2] {
		t.Errorf("%q does not sort before %q", lines[1], lines[2])
	}
}
