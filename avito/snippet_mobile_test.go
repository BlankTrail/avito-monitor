// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strconv"
	"strings"
	"testing"
)

// TestTheSnippetLayoutIsReadFromTheState.
//
// Captured on 11 October 2026: a price-banded query whose page said 831 results
// and from which the extractor read none — the cards had no item-wrapper and no
// link, because the listings arrive in __initialData__ and the markup is drawn
// from them. Classified ClassShape, which is honest, and collected nothing.
func TestTheSnippetLayoutIsReadFromTheState(t *testing.T) {
	doc, m := loadFixture(t, "mobile-search-snippet")
	page, err := Extract(doc, Options{Now: m.FrozenNow})
	if err != nil {
		t.Fatal(err)
	}
	if page.Report.Rule != RuleMobileSnippet {
		t.Errorf("rule = %q", page.Report.Rule)
	}
	if len(page.Listings) != 30 {
		t.Fatalf("listings = %d, want 30", len(page.Listings))
	}
	withImage := 0
	for _, l := range page.Listings {
		// The id is in the path the snippet carries, as it is everywhere else.
		if !strings.HasSuffix(l.URLPath, "_"+strconv.FormatInt(l.ItemID, 10)) {
			t.Errorf("%d: path %q does not end in its id", l.ItemID, l.URLPath)
		}
		if l.PriceKind == PriceExact && l.Price.Minor < 6_000_000 {
			t.Errorf("%d: %d kopecks is under the band's floor of 60 000 ₽", l.ItemID, l.Price.Minor)
		}
		if l.ImageKey != "" {
			withImage++
		}
	}
	// The repost ladder's strong signal the mobile surface never had.
	if withImage < 25 {
		t.Errorf("image keys on %d of 30", withImage)
	}
}

// TestTheRichCardCarriesItsTitleAndPrice. Vehicles get the «rich» card, and
// thirty of them were read with no title and no price.
func TestTheRichCardCarriesItsTitleAndPrice(t *testing.T) {
	doc, m := loadFixture(t, "mobile-search-rich")
	page, err := Extract(doc, Options{Now: m.FrozenNow})
	if err != nil {
		t.Fatal(err)
	}
	priced := 0
	for _, l := range page.Listings {
		if l.Title == "" {
			t.Errorf("%d has no title", l.ItemID)
		}
		if l.PriceKind == PriceExact {
			priced++
		}
	}
	if len(page.Listings) != 30 || priced < 28 {
		t.Errorf("listings = %d, priced = %d", len(page.Listings), priced)
	}
}
