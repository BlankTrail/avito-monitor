// SPDX-License-Identifier: AGPL-3.0-or-later

package blanktrail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageNamesNoTarget keeps this package usable against any site.
//
// The rule is a design decision, not a style preference: recognising a specific
// target's defences here would duplicate work the proxy already does, age
// separately from it, and drift. Until now the rule was enforced by review
// alone, which is how a rule quietly stops holding.
func TestPackageNamesNoTarget(t *testing.T) {
	// Names of specific sites and specific defence vendors, plus the one
	// classifier concept the package must never grow. The generic term for the
	// category is deliberately absent: retry.go states the boundary with it
	// ("... handling belongs to the proxy, not here"), and a guard that forbids
	// stating a rule is a guard that gets deleted.
	//
	// The Wildberries entries stay although this project does not target it.
	// A guard that forbids only the current target stops being a guard the
	// moment the package is copied a third time, and this is the second copy.
	//
	// "captcha" comes OFF the list, and that is a deliberate loosening rather
	// than an oversight. PortSpec gains CaptchaAction and CaptchaMaxAttempts
	// in the commit after this one, because a live port reports those controls
	// and the SDK could not set them. They are the proxy's own knobs and apply
	// to any target; this list exists to stop the package learning about
	// *sites*, and a rule that also forbade the vendor's own vocabulary would
	// have to be suspended for every such field. "avito" and "avisc" go ON in
	// exchange, so the guard ends up stricter about the thing it was written
	// to prevent.
	banned := []string{
		"wildberries", "wbaas", "avito", "avisc",
		"qrator", "cloudflare", "akamai", "datadome", "perimeterx", "geetest",
	}

	// This file's own name, so the loop below can skip it: the banned list
	// and this comment necessarily spell out every word they forbid, so
	// scanning this source would fail on its own declaration of the rule,
	// not on a violation of it.
	const self = "neutrality_test.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || name == self {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// The module path contains the target's name and is not a violation.
		text := strings.ToLower(string(src))
		text = strings.ReplaceAll(text, "github.com/blanktrail/avito-monitor", "")
		for _, word := range banned {
			if strings.Contains(text, word) {
				t.Errorf("%s mentions %q; this package must stay neutral about what it is pointed at", name, word)
			}
		}
	}
}
