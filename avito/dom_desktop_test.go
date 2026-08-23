// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strings"
	"testing"
)

const desktopCard = `<div data-marker="item" data-item-id="7775028313" ` +
	`class="iva-item-root-Gz1qE" itemtype="http://schema.org/Product">` +
	`<a itemprop="url" href="/ekaterinburg/telefony/iphone_15_128_gb_7775028313?context=X">` +
	`<span data-marker="item-title">iPhone 15, 128 ГБ</span></a>` +
	`<meta itemprop="price" content="26490"/>` +
	`<p data-marker="item-price"><strong>26 490 ₽</strong></p>` +
	`<div data-marker="item-address">Екатеринбург</div>` +
	`</div>`

func TestExtractDesktopDOM(t *testing.T) {
	page, ok := extractDesktopDOM(Document{Surface: SurfaceDesktop,
		Body: []byte("<html><body>" + desktopCard + "</body></html>")})
	if !ok {
		t.Fatal("extractDesktopDOM returned false on a well-formed card")
	}
	if page.Report.Rule != RuleDesktopMicrodata || page.Report.Path != PathDOM {
		t.Errorf("Rule/Path = %q/%q, want %q/%q",
			page.Report.Rule, page.Report.Path, RuleDesktopMicrodata, PathDOM)
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.Title != "iPhone 15, 128 ГБ" {
		t.Errorf("Title = %q", l.Title)
	}
	if l.URLPath != "/ekaterinburg/telefony/iphone_15_128_gb_7775028313" {
		t.Errorf("URLPath = %q, want the query dropped", l.URLPath)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000", l.PriceKind, l.Price.Minor)
	}
}

// TestDesktopDOMIgnoresClasses is the robustness claim, proved rather than
// asserted. Avito ships CSS modules: every class is a stem plus a build hash,
// so a selector on a class breaks on a deploy and returns zero cards — and
// zero cards is exactly what a new-listing monitor reports on a quiet market.
func TestDesktopDOMIgnoresClasses(t *testing.T) {
	rehashed := strings.ReplaceAll(desktopCard, "iva-item-root-Gz1qE", "totally-different-YLctS")
	page, ok := extractDesktopDOM(Document{Surface: SurfaceDesktop, Body: []byte(rehashed)})
	if !ok || len(page.Listings) != 1 {
		t.Fatal("rehashing the class changed the result; no extractor may depend on a class")
	}
}
