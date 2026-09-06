// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"os/exec"
	"strings"
	"testing"
)

// bannedTransitively is what this package may not reach, however indirectly.
//
// Package level rather than a local, so TestTheMakefileAndTheTestsBanTheSameThings
// can read it: the Makefile keeps its own copy of this list in a grep, and the
// two drifted the moment net/url was allowed here and not there.
var bannedTransitively = []string{"net", "net/http", "database/sql"}

// TestPackageReachesNoNetwork keeps every test in this package offline by
// construction rather than by discipline.
//
// The rule is a design decision, not a style preference: an extractor that can
// reach the network grows a test that does, and that test then fails for
// reasons that have nothing to do with the code — a captcha, a ban, somebody
// else's Wi-Fi. Worse, it stops being a test of the parser at all. Enforcing it
// here means the whole suite can run on a train.
//
// It walks `go list -deps`, which is transitive — a package that imported
// something that imported net is caught too, which an import-list check would
// miss. The price of that reach is that it can only ban things stdlib basics do
// not pull in on their own: «time» and «os» arrive transitively through
// encoding/json and golang.org/x/net/html, so the rule that this package must
// not ASK the clock is enforced next door, on direct imports, by
// TestTheDomainImportsNothingItMustNot.
func TestPackageReachesNoNetwork(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	// net/url is deliberately not here, and it was until increment 8's sweep.
	//
	// This test's own reason is that «an extractor that can reach the network
	// grows a test that does» — and net/url cannot reach anything: it parses
	// and prints strings, and pulls in no networking package of its own, which
	// is why banning `net` transitively still catches anything that could.
	//
	// avito.SearchURL needs it, and the spec puts that function in this
	// package and calls it «the whole of the entry point, and it is pure». A
	// rule that forbade the spec's own pure function would be a rule enforcing
	// a mistake rather than a design.
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		for _, b := range bannedTransitively {
			if dep == b {
				t.Errorf("package avito reaches %q; it must stay offline", dep)
			}
		}
	}
}
