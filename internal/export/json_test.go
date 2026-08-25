// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func jsonOf(t *testing.T, format string, sel []string, list ...Row) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(format, &buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Export(context.Background(), rows(list...), sel, w); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestAJsonFileIsOneArrayAndAJsonlFileIsOneObjectPerLine.
//
// The two formats exist for two readers: a program that loads the lot, and a
// pipeline that reads a line at a time. A JSONL with an envelope is neither.
func TestAJsonFileIsOneArrayAndAJsonlFileIsOneObjectPerLine(t *testing.T) {
	arr := jsonOf(t, "json", []string{"item_id"}, aRow(), aRow())
	var list []map[string]any
	if err := json.Unmarshal([]byte(arr), &list); err != nil {
		t.Fatalf("json is not an array: %v — %q", err, arr)
	}
	if len(list) != 2 {
		t.Errorf("array has %d entries", len(list))
	}

	lines := jsonOf(t, "jsonl", []string{"item_id"}, aRow(), aRow())
	got := strings.Split(strings.TrimSpace(lines), "\n")
	if len(got) != 2 {
		t.Fatalf("jsonl has %d lines: %q", len(got), lines)
	}
	for i, line := range got {
		var one map[string]any
		if err := json.Unmarshal([]byte(line), &one); err != nil {
			t.Errorf("line %d is not an object: %v", i, err)
		}
	}
}

// TestTheKeysAreTheKeysAndNotTheNames.
//
// A program reads this file, and the key is the half fields.go promises not to
// change between releases while a name moves with the interface's wording.
func TestTheKeysAreTheKeysAndNotTheNames(t *testing.T) {
	got := jsonOf(t, "jsonl", []string{"item_id", "price"}, aRow())
	if !strings.Contains(got, `"item_id"`) {
		t.Errorf("json = %q; no key", got)
	}
	if strings.Contains(got, "Номер объявления") {
		t.Errorf("json = %q; it carries the human name", got)
	}
}

// TestAnAbsentValueIsNullAndNotZero.
//
// The same fact CSV writes as an empty cell. A consumer summing a price column
// must not add a nought for a listing that had no price.
func TestAnAbsentValueIsNullAndNotZero(t *testing.T) {
	r := aRow()
	r.PriceMinor = nil

	got := jsonOf(t, "jsonl", []string{"price"}, r)
	var one map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &one); err != nil {
		t.Fatal(err)
	}
	v, ok := one["price"]
	if !ok {
		t.Fatal("the column is missing entirely; one shape per column is the promise")
	}
	if v != nil {
		t.Errorf("price = %v, want null", v)
	}
}

// TestMoneyIsANumberInRoublesAndNotAString, so a consumer can sum the column
// without parsing it out of a string first.
func TestMoneyIsANumberInRoublesAndNotAString(t *testing.T) {
	got := jsonOf(t, "jsonl", []string{"price"}, aRow())
	var one map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &one); err != nil {
		t.Fatal(err)
	}
	f, ok := one["price"].(float64)
	if !ok {
		t.Fatalf("price = %#v, want a number", one["price"])
	}
	if f != 40000 {
		t.Errorf("price = %v, want 40000 roubles from 4000000 kopecks", f)
	}
}

// TestJsonIsUtf8ByDefinitionAndAnotherEncodingIsRefused.
//
// RFC 8259 says so. A file in windows-1251 under a .json name is not JSON, and
// producing one because an option said to would be the silent loss this package
// exists to avoid.
func TestJsonIsUtf8ByDefinitionAndAnotherEncodingIsRefused(t *testing.T) {
	var buf bytes.Buffer
	if _, err := NewWriter("json", &buf, Options{Encoding: "windows-1251"}); err == nil {
		t.Error("a non-UTF-8 encoding was accepted for JSON")
	}
	// utf-8 is not refused: it is what JSON already is.
	if _, err := NewWriter("json", &buf, Options{Encoding: "utf-8"}); err != nil {
		t.Errorf("utf-8 was refused: %v", err)
	}
}

// TestTheSameSelectionGivesTheSameColumnsInEveryFormat.
//
// This is the claim the whole package exists to keep, and it is the one that
// cannot be checked inside a single format.
func TestTheSameSelectionGivesTheSameColumnsInEveryFormat(t *testing.T) {
	sel := []string{"item_id", "title", "price", "present"}

	csvHead := strings.SplitN(csvOf(t, Options{}, sel, aRow()), "\n", 2)[0]
	csvCols := strings.Split(strings.TrimRight(csvHead, "\r"), ",")

	var one map[string]any
	if err := json.Unmarshal(
		[]byte(strings.TrimSpace(jsonOf(t, "jsonl", sel, aRow()))), &one); err != nil {
		t.Fatal(err)
	}

	if len(csvCols) != len(one) {
		t.Fatalf("csv has %d columns and json has %d", len(csvCols), len(one))
	}
	for _, key := range sel {
		f, _ := FieldByKey(key)
		if _, ok := one[key]; !ok {
			t.Errorf("json is missing %q", key)
		}
		found := false
		for _, c := range csvCols {
			if c == f.Name {
				found = true
			}
		}
		if !found {
			t.Errorf("csv is missing %q (%q)", key, f.Name)
		}
	}
}

// TestNoColumnsAtAllIsRefusedRatherThanWritingAnEmptyObject.
//
// Columns never produces an empty set — an empty selection is everything — so a
// writer reaching Begin with none has been driven by something that bypassed
// it, and an empty object per row is not a file anybody can use.
func TestNoColumnsAtAllIsRefusedRatherThanWritingAnEmptyObject(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter("json", &buf, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Begin(nil); err == nil {
		t.Error("a writer accepted an empty column set")
	}
}
