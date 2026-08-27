// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbidden is what this package may not import, and why each one is on the
// list.
//
// The spec puts it plainly: «avito — the domain. No net, no net/http, no clock,
// no database». The reason is that this package is what turns bytes into a
// Page, and the moment it can fetch anything, the offline tests stop proving
// that a fixture and a live page are read by the same code.
//
// The clock is on the list for the same reason and it is the one people
// question: an extractor that could ask the time would have a golden fixture
// whose output changed at midnight. Options.Now exists so a caller passes one
// in — that is what makes the fixtures reproducible.
var forbidden = map[string]string{
	"net":          "the domain must not reach the network",
	"net/http":     "the domain must not reach the network",
	"net/url":      "a URL this package built would be a URL nothing measured",
	"database/sql": "the domain must not know a schema",
	"time":         "a clock makes a golden fixture change at midnight; use Options.Now",
	"os":           "the domain reads bytes it was handed, not files it found",
	"os/exec":      "no",
}

// TestTheDomainImportsNothingItMustNot is `make check-no-net`, enforced where
// it will actually run.
//
// The Makefile target exists because the spec names it, and it greps. This
// parses, which is stricter, and it runs for anybody who types `go test ./...`
// — which is everybody, and which is the difference between a rule and a wish.
func TestTheDomainImportsNothingItMustNot(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("%d files found; this test is looking in the wrong place", len(files))
	}

	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			// Tests may open fixtures and may hold a clock. They are how the
			// rule is checked, not part of what it constrains.
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checked++
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if why, bad := forbidden[path]; bad {
				t.Errorf("%s imports %q: %s", name, path, why)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
	t.Logf("checked %d files", checked)
}
