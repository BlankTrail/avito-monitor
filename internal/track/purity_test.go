// SPDX-License-Identifier: AGPL-3.0-or-later

package track_test

import (
	"go/build"
	"strings"
	"testing"
)

// TestPackageIsPure. track diffs two readings and nothing else.
//
// An import of database/sql would make a change depend on what happened to be
// stored. An import of time would let two runs over the same two readings
// disagree — a reading carries its own TS, and a producer reaching for the
// clock would date a change by when it was computed rather than by when it was
// observed.
func TestPackageIsPure(t *testing.T) {
	banned := []string{"net", "net/http", "database/sql", "os", "time"}
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if imp == b || strings.HasPrefix(imp, b+"/") {
				t.Errorf("track imports %q; a change must be a function of its "+
					"two readings and of nothing else", imp)
			}
		}
	}
}
