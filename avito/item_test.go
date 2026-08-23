// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

const itemPage = `<html><body>` +
	`<h1 data-marker="item-view/title-info">iPhone 15, 128 ГБ</h1>` +
	`<span data-marker="item-view/item-id">№ 7775028313</span>` +
	`<span data-marker="item-view/item-price">26 490 ₽</span>` +
	`<span data-marker="item-view/item-date">сегодня в 12:20</span>` +
	`<div data-marker="item-view/item-description">Новый, с гарантией</div>` +
	`<span data-marker="item-view/total-views">1 284 просмотра</span>` +
	`<span data-marker="item-view/today-views">42 сегодня</span>` +
	`</body></html>`

func TestExtractItem(t *testing.T) {
	page, ok := extractItem(Document{Surface: SurfaceDesktop, Body: []byte(itemPage)})
	if !ok {
		t.Fatal("extractItem returned false on a well-formed item page")
	}
	if page.Kind != EnvelopeItem {
		t.Errorf("Kind = %q, want %q", page.Kind, EnvelopeItem)
	}
	if page.Report.Rule != RuleItemMarkers {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleItemMarkers)
	}
	if page.Ok() {
		t.Error("Ok() = true on an item envelope; it asserts a catalogue")
	}
	if len(page.Listings) != 1 {
		t.Fatalf("Listings = %d, want exactly 1", len(page.Listings))
	}
	l := page.Listings[0]
	if l.ItemID != 7775028313 {
		t.Errorf("ItemID = %d, want 7775028313", l.ItemID)
	}
	if l.PriceKind != PriceExact || l.Price.Minor != 2649000 {
		t.Errorf("price = %v %d, want exact 2649000 — the rendered label is the "+
			"only price source this surface has", l.PriceKind, l.Price.Minor)
	}
	if l.Description != "Новый, с гарантией" {
		t.Errorf("Description = %q", l.Description)
	}
	if l.ViewsTotal == nil || *l.ViewsTotal != 1284 {
		t.Errorf("ViewsTotal = %v, want 1284", l.ViewsTotal)
	}
	if l.ViewsToday == nil || *l.ViewsToday != 42 {
		t.Errorf("ViewsToday = %v, want 42", l.ViewsToday)
	}
	if l.ClosedReason != "" {
		t.Errorf("ClosedReason = %q on a live posting", l.ClosedReason)
	}
}

// TestExtractItemClosed pins the only thing permitted to call a listing closed:
// the site's own words, stored verbatim and uninterpreted.
func TestExtractItemClosed(t *testing.T) {
	closed := `<div data-marker="item-view/item-id">№ 99</div>` +
		`<div data-marker="item-view/closed-warning">Объявление снято с публикации</div>`
	page, ok := extractItem(Document{Surface: SurfaceDesktop, Body: []byte(closed)})
	if !ok {
		t.Fatal("false on a closed posting; a closed posting is a reading")
	}
	if got := page.Listings[0].ClosedReason; got != "Объявление снято с публикации" {
		t.Errorf("ClosedReason = %q, want the site's words verbatim", got)
	}
}
