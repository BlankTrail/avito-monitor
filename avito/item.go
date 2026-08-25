// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "strings"

const RuleItemMarkers = "item.markers.v1"

// Item-page handles. There is no ld+json and no itemprop="price" on this
// surface — measured — so data-marker is all there is.
const (
	markerItemID     = "item-view/item-id"
	markerItemTitle  = "item-view/title-info"
	markerItemPrice  = "item-view/item-price"
	markerItemDesc   = "item-view/item-description"
	markerItemParams = "item-view/item-params"
	markerViewsTotal = "item-view/total-views"
	markerViewsToday = "item-view/today-views"
	markerItemClosed = "item-view/closed-warning"
)

var itemMarkers = map[string]bool{
	markerItemID:     true,
	markerItemTitle:  true,
	markerItemPrice:  true,
	markerItemDesc:   true,
	markerItemParams: true,
	markerViewsTotal: true,
	markerViewsToday: true,
	markerItemClosed: true,
}

// markerSellerLink is the anchor pointing at whoever posted this.
const markerSellerLink = "seller-link/link"

var itemLinkMarkers = map[string]bool{markerSellerLink: true}

// extractItem reads one listing's own page.
//
// It exists in this increment rather than a later one because it is what turns
// a windowed disappearance guess into a stated fact: the confirmation sweeper
// has no other source, and without it every removal alert stays at confidence
// 0.3 for ever.
func extractItem(d Document) (Page, bool) {
	page := Page{Surface: d.Surface, Kind: EnvelopeItem}
	page.Report.Rule = RuleItemMarkers
	page.Report.Path = PathDOM

	f := scanMarkers(d.Body, itemMarkers)
	// The seller, from the link and not from the text. This is the one field
	// enrichment exists to recover: a listing collected from the mobile
	// surface has no seller at all, and the seller is the repost ladder's one
	// hard gate. The block's text runs the name, the rating and the join date
	// together with no separator, so the href is the only thing here worth
	// reading — and it is read whole rather than pattern-matched.
	links := scanMarkerAttrs(d.Body, itemLinkMarkers, "href")
	id, ok := DigitsToInt(f[markerItemID])
	if !ok {
		return Page{}, false
	}
	page.Report.Found = 1
	l := Listing{
		ItemID:      id,
		Title:       f[markerItemTitle],
		Description: f[markerItemDesc],
		PriceLabel:  f[markerItemPrice],
		PriceKind:   PriceAbsent,
		Price:       Money{Currency: "RUB"},
		// The site's own words when a posting has ended, kept verbatim and
		// uninterpreted. Its presence is the only thing permitted to write
		// state='closed' with confidence 1.0.
		ClosedReason: f[markerItemClosed],
	}
	if key, quality := sellerKeyFromLink(links[markerSellerLink]); key != "" {
		l.SellerKey, l.SellerKeyQuality = key, quality
		l.SellerProfilePath = trimQuery(links[markerSellerLink])
	}
	switch {
	case l.PriceLabel == "":
		l.PriceKind = PriceAbsent
	case strings.Contains(l.PriceLabel, negotiableLabel):
		l.PriceKind = PriceNegotiable
	default:
		if minor, ok := DigitsToMinor(l.PriceLabel); ok {
			l.PriceKind = PriceExact
			l.Price.Minor = minor
		} else {
			l.PriceKind = PriceNegotiable
		}
	}
	// No second number to disagree with, so the advisory cross-check does not
	// run here either.
	if v, ok := DigitsToInt(f[markerViewsTotal]); ok {
		l.ViewsTotal = &v
	}
	if v, ok := DigitsToInt(f[markerViewsToday]); ok {
		l.ViewsToday = &v
	}
	page.Listings = []Listing{l}
	return page, true
}
