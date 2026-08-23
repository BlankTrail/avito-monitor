// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// stateDoc wraps a JSON payload in the block shape the site actually serves.
func stateDoc(payload string) Document {
	return Document{
		Surface: SurfaceDesktop,
		Body: []byte(`<html><body><script type="mime/invalid" data-mfe-state="true">` +
			payload + `</script></body></html>`),
	}
}

func TestExtractDesktopStateNamedRule(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{
		"totalCount": 34692,
		"catalog":{"items":[
			{"id":7775028313,"urlPath":"/ekaterinburg/telefony/iphone_15_128_gb_7775028313?context=X",
			 "title":"iPhone 15, 128 ГБ","categoryId":84,"microCategoryId":1104365,
			 "locationId":654070,"sortTimeStamp":1786587948000,
			 "priceDetailed":{"value":26490,"string":"26 490","fullString":"26 490 ₽","wasLowered":false},
			 "coords":{"lat":"56.841773","lng":"60.609485"},
			 "isXl":false,"imagesCount":4}
		]}}}}`))
	if !ok {
		t.Fatal("extractDesktopState returned false on a well-formed catalogue")
	}
	if page.Report.Rule != RuleDesktopCatalog {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleDesktopCatalog)
	}
	if page.Report.Path != PathState {
		t.Errorf("Path = %q, want %q", page.Report.Path, PathState)
	}
	if page.Report.HasWarning(WarnShapeProbe) {
		t.Error("the named rule answered but a shape-probe warning was recorded")
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.URLPath != "/ekaterinburg/telefony/iphone_15_128_gb_7775028313" {
		t.Errorf("URLPath = %q; the ?context= suffix must be dropped, it is a "+
			"per-request token and keeping it would make every poll look like a "+
			"changed listing", l.URLPath)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000 — Avito states whole roubles",
			l.PriceKind, l.Price.Minor)
	}
	if l.PriceLabel != "26 490 ₽" {
		t.Errorf("PriceLabel = %q, want the site's own rendering verbatim", l.PriceLabel)
	}
	if l.SortTimeMS == nil || *l.SortTimeMS != 1786587948000 {
		t.Errorf("SortTimeMS = %v, want 1786587948000 kept in its own unit", l.SortTimeMS)
	}
	if l.Lat == nil || l.Lng == nil {
		t.Fatal("coords were not read; they are on every listing and they are free")
	}
	if page.TotalCount == nil || *page.TotalCount != 34692 {
		t.Errorf("TotalCount = %v, want 34692", page.TotalCount)
	}
}

// TestExtractDesktopStateNegotiable pins the three-valued price. An absent
// priceDetailed is the seller saying so; it is not our failure to read one.
func TestExtractDesktopStateNegotiable(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{"catalog":{"items":[
		{"id":1,"urlPath":"/x_1","title":"Диван",
		 "priceDetailed":{"value":0,"string":"","fullString":"Цена договорная","hasValue":false}}
	]}}}}`))
	if !ok {
		t.Fatal("false on a listing with no price")
	}
	if page.Listings[0].PriceKind != PriceNegotiable {
		t.Errorf("PriceKind = %q, want %q", page.Listings[0].PriceKind, PriceNegotiable)
	}
}

// TestExtractDesktopStateShapeProbe pins the last resort. A payload whose
// catalogue has moved must still be read, and the reading must be flagged,
// because a run where the probe starts answering is a page whose structure
// moved — reported before anyone notices the counts are wrong.
func TestExtractDesktopStateShapeProbe(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(`{"loaderData":{"data":{"somethingNew":{"rows":[
		{"id":11,"urlPath":"/a_11","title":"A"},
		{"id":12,"urlPath":"/a_12","title":"B"}
	]}}}}`))
	if !ok {
		t.Fatal("the probe did not find an array that is obviously listings")
	}
	if page.Report.Rule != RuleDesktopProbe {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleDesktopProbe)
	}
	if !page.Report.HasWarning(WarnShapeProbe) {
		t.Error("the probe answered without warning; that is the whole point of it")
	}
	if len(page.Listings) != 2 {
		t.Errorf("Listings = %d, want 2", len(page.Listings))
	}
}

// TestExtractDesktopStateRedirect pins the SPA shell, whose only useful content
// is the canonical URL — the reason a desktop search costs two requests.
func TestExtractDesktopStateRedirect(t *testing.T) {
	page, ok := extractDesktopState(stateDoc(
		`{"loaderData":{"data":{"status":{"code":301},"redirected":true,` +
			`"url":"/all/telefony/apple/iphone_15-ASgB?context=Y"}}}`))
	if !ok {
		t.Fatal("the shell was not recognised")
	}
	if page.Kind != EnvelopeRedirect {
		t.Fatalf("Kind = %q, want %q", page.Kind, EnvelopeRedirect)
	}
	if page.CanonicalURL != "/all/telefony/apple/iphone_15-ASgB?context=Y" {
		t.Errorf("CanonicalURL = %q; the query must be kept here, unlike on a "+
			"listing path — it is what the next fetch needs", page.CanonicalURL)
	}
	if page.Ok() {
		t.Error("a redirect shell reported Ok(); it carries no listings")
	}
}
