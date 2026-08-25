// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func xlsxOf(t *testing.T, sel []string, list ...Row) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewXLSX(&buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(context.Background(), rows(list...), sel, w); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the file is not a zip: %v", err)
	}
	return z
}

func partOf(t *testing.T, z *zip.Reader, name string) string {
	t.Helper()
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		raw, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	t.Fatalf("the archive has no %s", name)
	return ""
}

// TestTheFileIsAZipWithTheFourPartsAnXlsxNeeds.
//
// A spreadsheet is a container, and one missing part is a file Excel refuses to
// open at all rather than opening wrongly — so this checks the manifest rather
// than trusting the writer.
func TestTheFileIsAZipWithTheFourPartsAnXlsxNeeds(t *testing.T) {
	z := xlsxOf(t, nil, aRow())
	have := map[string]bool{}
	for _, f := range z.File {
		have[f.Name] = true
	}
	for _, want := range []string{
		"[Content_Types].xml", "_rels/.rels",
		"xl/workbook.xml", "xl/worksheets/sheet1.xml",
	} {
		if !have[want] {
			t.Errorf("the archive has no %s (has %v)", want, have)
		}
	}
}

// TestEveryPartIsWellFormedXml.
//
// Excel answers malformed XML with «unreadable content» and an offer to repair,
// which loses the file. Parsing every part is the cheapest way to be sure the
// writer never produces one.
func TestEveryPartIsWellFormedXml(t *testing.T) {
	z := xlsxOf(t, nil, aRow())
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		body := partOf(t, z, f.Name)
		dec := xml.NewDecoder(strings.NewReader(body))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s is not well-formed: %v", f.Name, err)
				break
			}
		}
	}
}

// TestATitleWithAnAmpersandDoesNotCorruptTheSheet.
//
// «Samsung & Co» is an ordinary title and a raw & is malformed XML — which
// Excel reports as unreadable content and offers to repair, losing the file.
func TestATitleWithAnAmpersandDoesNotCorruptTheSheet(t *testing.T) {
	r := aRow()
	r.Title = `Samsung & Co <"лучший"> цена`

	z := xlsxOf(t, []string{"title"}, r)
	sheet := partOf(t, z, "xl/worksheets/sheet1.xml")

	dec := xml.NewDecoder(strings.NewReader(sheet))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the sheet is malformed: %v", err)
		}
	}
	if strings.Contains(sheet, "& Co") {
		t.Error("a raw ampersand reached the sheet")
	}
}

// TestMoneyIsANumberAndNotAString, so a spreadsheet can sum a column of prices.
func TestMoneyIsANumberAndNotAString(t *testing.T) {
	z := xlsxOf(t, []string{"price"}, aRow())
	sheet := partOf(t, z, "xl/worksheets/sheet1.xml")

	if !strings.Contains(sheet, "<v>40000.00</v>") {
		t.Errorf("no numeric price cell in %q", sheet)
	}
	// And not also as text somewhere. <t> is where an inline string lives, so
	// the digits appearing inside one would mean a cell a spreadsheet cannot
	// sum — which is what somebody opens this file to do.
	for _, chunk := range strings.Split(sheet, "<t>")[1:] {
		text := chunk[:strings.Index(chunk, "</t>")]
		if strings.Contains(text, "40000") {
			t.Errorf("the price appears inside a text element: %q", text)
		}
	}
}

// TestAnAbsentValueIsAnEmptyCellInASpreadsheet, so «no price» does not sort as
// zero and does not drag an average down.
func TestAnAbsentValueIsAnEmptyCellInASpreadsheet(t *testing.T) {
	r := aRow()
	r.PriceMinor = nil

	z := xlsxOf(t, []string{"item_id", "price"}, r)
	sheet := partOf(t, z, "xl/worksheets/sheet1.xml")
	if strings.Contains(sheet, "<v>0</v>") {
		t.Errorf("an absent price was written as zero: %q", sheet)
	}
}

// TestTheHeaderRowCarriesTheNames, which is what a person reads.
func TestTheHeaderRowCarriesTheNames(t *testing.T) {
	z := xlsxOf(t, []string{"price"}, aRow())
	sheet := partOf(t, z, "xl/worksheets/sheet1.xml")
	if !strings.Contains(sheet, "Цена") {
		t.Errorf("the header does not carry the column name: %q", sheet)
	}
}

// TestATimeIsANumberPlusAFormatAndNotText.
//
// As text a date sorts alphabetically, filters as a string and cannot be
// subtracted, which is every question anyone asks of a date.
func TestATimeIsANumberPlusAFormatAndNotText(t *testing.T) {
	z := xlsxOf(t, []string{"snapshot_at"}, aRow())
	sheet := partOf(t, z, "xl/worksheets/sheet1.xml")
	if strings.Contains(sheet, "2023-11-14") || strings.Contains(sheet, "T00:") {
		t.Errorf("the date was written as text: %q", sheet)
	}
}

// TestAnUnknownEncodingIsIgnoredRatherThanRefused.
//
// The other side of the option discipline: an XLSX is UTF-8 inside a zip and
// Encoding names formatting some other format applies, so there is nothing the
// person would be missing to be told about.
func TestAnUnknownEncodingIsIgnoredRatherThanRefused(t *testing.T) {
	var buf bytes.Buffer
	if _, err := NewXLSX(&buf, Options{Encoding: "windows-1251"}); err != nil {
		t.Errorf("an encoding that cannot change an XLSX was refused: %v", err)
	}
}
