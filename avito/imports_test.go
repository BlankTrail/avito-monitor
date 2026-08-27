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

// forbiddenDirect is what this package's own files may not import.
//
// The companion to TestPackageReachesNoNetwork, and the two are not duplicates:
// that one walks `go list -deps` and is transitive, so it catches anything
// reaching the network however indirectly — but for the same reason it can only
// ban packages that stdlib basics do not pull in themselves. «time» and «os»
// arrive transitively through encoding/json and x/net/html, so the rule that
// this package must not ASK the clock has to be checked on direct imports, and
// that is this.
//
// The clock is the one people question. An extractor that could ask the time
// would have a golden fixture whose output changed at midnight; Options.Now
// exists so a caller passes one in, and that is what makes the fixtures
// reproducible.
var forbiddenDirect = map[string]string{
	"time":     "a clock makes a golden fixture change at midnight; use Options.Now",
	"os":       "the domain reads bytes it was handed, not files it found",
	"os/exec":  "the domain runs nothing",
	"net":      "the domain must not reach the network",
	"net/http": "the domain must not reach the network",
}

// TestTheDomainImportsNothingItMustNot is the direct half of `make check-no-net`.
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
			if why, bad := forbiddenDirect[path]; bad {
				t.Errorf("%s imports %q: %s", name, path, why)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were checked")
	}
}
