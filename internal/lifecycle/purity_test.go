// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle_test

import (
	"go/build"
	"strings"
	"testing"
)

// TestPackageIsPure. lifecycle answers three questions about facts it was
// handed, and nothing else.
//
// An import of time is the one that matters here. Every gate in this package
// is a comparison of two timestamps the caller read, so a verdict is
// reproducible from the row that produced it — and "why was I told this was
// gone" has to stay answerable a month later. A clock inside would make the
// answer depend on when the question was asked.
func TestPackageIsPure(t *testing.T) {
	banned := []string{"net", "net/http", "database/sql", "os", "time"}
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if imp == b || strings.HasPrefix(imp, b+"/") {
				t.Errorf("lifecycle imports %q; a verdict must be a function of the facts "+
					"it was given and of nothing else", imp)
			}
		}
	}
}
