// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPackageReachesNoNetwork keeps every test in this package offline by
// construction rather than by discipline.
//
// The rule is a design decision, not a style preference: an extractor that can
// reach the network grows a test that does, and that test then fails for
// reasons that have nothing to do with the code — a captcha, a ban, somebody
// else's Wi-Fi. Worse, it stops being a test of the parser at all. Enforcing it
// here means the whole suite can run on a train.
func TestPackageReachesNoNetwork(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	banned := []string{"net", "net/http", "net/url", "database/sql"}
	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		for _, b := range banned {
			if dep == b {
				t.Errorf("package avito reaches %q; it must stay offline", dep)
			}
		}
	}
}
