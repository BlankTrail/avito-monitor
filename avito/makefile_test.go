// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestTheMakefileAndTheTestsBanTheSameThings.
//
// `make check-no-net` keeps its own copy of the ban list, in a grep, and the
// two drifted: net/url was allowed here — avito.SearchURL needs it and the spec
// calls that function pure — and stayed forbidden there. `make check` was red
// against a package whose own two tests were green, and nobody noticed, because
// running that target needs a `make` this project's own machine does not have.
//
// So the drift is checked by a test instead, which everybody runs. The Makefile
// stays because the spec names that spelling; this is what keeps it honest.
func TestTheMakefileAndTheTestsBanTheSameThings(t *testing.T) {
	raw, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}

	// The alternation inside the check-no-net grep, found by locating the line
	// rather than by matching the whole command: a pattern precise enough to
	// describe that grep is a pattern that breaks when somebody adds a flag to
	// it, and this guard has to survive edits to the thing it guards.
	var list string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "grep -REn") {
			continue
		}
		open := strings.LastIndex(line, "(")
		shut := strings.LastIndex(line, ")")
		if open >= 0 && shut > open {
			list = line[open+1 : shut]
		}
	}
	if list == "" {
		t.Fatal("не нашёл список запретов в check-no-net: " +
			"цель переписали, а этот сторож — нет")
	}

	want := map[string]bool{}
	for _, b := range bannedTransitively {
		want[b] = true
	}
	for b := range forbiddenDirect {
		want[b] = true
	}

	got := map[string]bool{}
	for _, b := range strings.Split(list, "|") {
		got[b] = true
	}

	for b := range want {
		if !got[b] {
			t.Errorf("тесты запрещают %q, а Makefile — нет", b)
		}
	}
	for b := range got {
		if !want[b] {
			t.Errorf("Makefile запрещает %q, а тесты — нет: "+
				"`make check` будет красным там, где `go test` зелёный", b)
		}
	}
	if t.Failed() {
		t.Logf("Makefile: %v", sortedSet(got))
		t.Logf("тесты:    %v", sortedSet(want))
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
