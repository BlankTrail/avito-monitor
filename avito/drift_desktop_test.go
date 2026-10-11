// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// TestPagesThatMovedReadWithoutAWarning.
//
// Found on 11 October 2026 in a real installation: 107 of 253 passes refused
// as evidence for «the extraction reported a degradation». Two causes, one
// fixture each. The seller page's catalogue moved from state.searchData to
// loaderData.searchData, so every shop pass fell to the shape probe and warned.
// Search pages began carrying the site's banners among the listings, and each
// counted as a dropped listing. A warning makes a pass non-evidential, so those
// watches saw their seeding pass and nothing after it.
func TestPagesThatMovedReadWithoutAWarning(t *testing.T) {
	for _, c := range []struct {
		name, rule string
		listings   int
	}{
		{"desktop-seller-v2", RuleDesktopProfile, 12},
		{"desktop-banner", RuleDesktopCatalog, 50},
	} {
		doc, m := loadFixture(t, c.name)
		page, err := Extract(doc, Options{Now: m.FrozenNow})
		if err != nil {
			t.Fatal(err)
		}
		if page.Report.Rule != c.rule {
			t.Errorf("%s: rule %q, want %q", c.name, page.Report.Rule, c.rule)
		}
		if len(page.Report.Warnings) > 0 {
			t.Errorf("%s: warned %v", c.name, page.Report.Warnings)
		}
		if len(page.Listings) != c.listings || page.Report.Dropped != 0 {
			t.Errorf("%s: %d listings, %d dropped", c.name, len(page.Listings), page.Report.Dropped)
		}
	}
}
