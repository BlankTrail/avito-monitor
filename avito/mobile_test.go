// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// The literal continues with further statements after the string, which is why
// the scan must find the end of the string rather than the end of the script.
const mobileState = `<script>window.__initialData__ = ` +
	`"{\"search\":{\"count\":59959,\"mainCount\":59959}}";window.other = 2;</script>`

const mobileCard = `<div data-marker="item-wrapper(7831893581)" itemtype="http://schema.org/Product">` +
	`<div data-marker="titleLabelGrid">iPhone 15 Pro, 256 ГБ</div>` +
	`<div data-marker="priceLabelGrid">49 490 ₽</div>` +
	`<div data-marker="itemParamsGrid">Новый</div>` +
	`<div data-marker="addressLabel">Республика Татарстан, Казань</div>` +
	`<div data-marker="sellerAndRatingSellerLabel">TorStore Kazan</div>` +
	`<a data-marker="item/link" itemProp="url" data-marker-id="7831893581" ` +
	`href="/kazan/telefony/iphone_15_pro_256_gb_7831893581?context=Z"></a>` +
	`<meta itemProp="description" content="..."/>` +
	`</div>`

func TestFirstJSString(t *testing.T) {
	got, ok := firstJSString(`window.x = "a\"b\\" ; window.y = "second";`)
	if !ok {
		t.Fatal("no string literal found")
	}
	if got != `"a\"b\\"` {
		t.Errorf("got %q, want the first literal with its escapes intact "+
			"— an escaped quote must not end the scan", got)
	}
}

func TestExtractMobile(t *testing.T) {
	page, ok := extractMobile(Document{Surface: SurfaceMobile,
		Body: []byte("<html><body>" + mobileState + mobileCard + "</body></html>")})
	if !ok {
		t.Fatal("extractMobile returned false on a well-formed page")
	}
	if page.Report.Rule != RuleMobileCards || page.Report.Path != PathDOM {
		t.Errorf("Rule/Path = %q/%q, want %q/%q",
			page.Report.Rule, page.Report.Path, RuleMobileCards, PathDOM)
	}
	// dom is the mobile surface's primary path. It is not a degradation and
	// must never be reported as one.
	if page.Report.HasWarning(WarnDegraded) {
		t.Error("a healthy mobile page was marked degraded")
	}
	if page.TotalCount == nil || *page.TotalCount != 59959 {
		t.Fatalf("TotalCount = %v, want 59959 from __initialData__.search.count "+
			"— it is what the too_broad guard reads on this surface", page.TotalCount)
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7831893581 {
		t.Errorf("ItemID = %d, want 7831893581", l.ItemID)
	}
	if l.Title != "iPhone 15 Pro, 256 ГБ" {
		t.Errorf("Title = %q", l.Title)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 4949000 {
		t.Errorf("price = %v %d, want exact 4949000", l.PriceKind, l.Price.Minor)
	}
	if l.SellerName != "TorStore Kazan" {
		t.Errorf("SellerName = %q", l.SellerName)
	}
	// Measured: all thirty cards carry content="..." — the literal three dots.
	// The mobile surface has no description, and reporting the placeholder as
	// one would put three dots into thirty listings' history.
	if l.Description != "" {
		t.Errorf("Description = %q, want empty: the mobile placeholder is not "+
			"a description", l.Description)
	}
	// The measurement is unambiguous: parsing "1 час назад" would give a
	// timestamp precise enough to look trustworthy and wrong enough to order
	// arrivals incorrectly, so it is not parsed at all.
	if l.SortTimeMS != nil {
		t.Errorf("SortTimeMS = %v, want nil on the mobile surface", *l.SortTimeMS)
	}
	// This surface has no priceDetailed, so there is no second number to
	// disagree with and the advisory counter must stay untouched.
	if page.Report.HasWarning(WarnPriceMismatch) {
		t.Error("a price cross-check was recorded on a surface that cannot do one")
	}
}

// TestExtractMobileEmpty pins the distinction the classifier needs: the site
// saying nothing matched, versus a page we failed to read.
func TestExtractMobileEmpty(t *testing.T) {
	body := `<script>window.__initialData__ = ` +
		`"{\"search\":{\"count\":0,\"emptySearch\":\"Ничего не найдено по запросу\"}}";</script>`
	page, ok := extractMobile(Document{Surface: SurfaceMobile, Body: []byte(body)})
	if !ok {
		t.Fatal("an empty result set must extract successfully; it is an answer")
	}
	if !page.EmptyDeclared {
		t.Error("EmptyDeclared = false; the site said so in its own words")
	}
	if page.TotalCount == nil || *page.TotalCount != 0 {
		t.Errorf("TotalCount = %v, want 0", page.TotalCount)
	}
	if page.Ok() {
		t.Error("Ok() = true on a page with no listings")
	}
}

// TestExtractMobileIDMismatch pins the cross-check. Two identifiers that
// disagree mean the card is not what it says it is.
func TestExtractMobileIDMismatch(t *testing.T) {
	bad := `<div data-marker="item-wrapper(111)">` +
		`<div data-marker="titleLabelGrid">X</div>` +
		`<a data-marker="item/link" data-marker-id="222" href="/x_222"></a></div>`
	page, _ := extractMobile(Document{Surface: SurfaceMobile,
		Body: []byte(mobileState + bad)})
	if len(page.Listings) != 0 {
		t.Errorf("Listings = %d, want 0 — the two ids disagreed", len(page.Listings))
	}
	if !page.Report.HasWarning(WarnIDMismatch) {
		t.Error("no id-mismatch warning was recorded")
	}
}
