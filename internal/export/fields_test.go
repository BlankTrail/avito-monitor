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
			t.Errorf("key %q carries a character a URL or a CSV header would eat", f.Key)
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
		if strings.Contains(f.Key, "price") && f.Key != "price_kind" &&
			f.Type != TypeMoney {
			t.Errorf("%q is typed %q; a price rendered as a plain integer is a "+
				"hundredfold error that still looks plausible", f.Key, f.Type)
		}
	}
}

// TestEveryGroupHasAtLeastOneField, so the screen draws no empty heading.
func TestEveryGroupHasAtLeastOneField(t *testing.T) {
	for _, g := range Groups() {
		found := false
		for _, f := range Fields() {
			if f.Group == g {
				found = true
			}
		}
		if !found {
			t.Errorf("group %q has no fields", g)
		}
	}
}

// TestFieldByKeyRefusesWhatItDoesNotHave, rather than returning a zero Field
// that would render as a nameless column.
func TestFieldByKeyRefusesWhatItDoesNotHave(t *testing.T) {
	if _, ok := FieldByKey("нет такого"); ok {
		t.Error("an unknown key was found")
	}
	if f, ok := FieldByKey("price"); !ok || f.Type != TypeMoney {
		t.Errorf("FieldByKey(price) = %+v, %v", f, ok)
	}
}
