// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strconv"
	"strings"
)

const RuleMobileCards = "mobile.cards.v1"

// negotiableLabel is the seller's own words for a price they will discuss.
const negotiableLabel = "Цена договорная"

// mobileSpec is the one-pass description of a mobile search card. The id is in
// the card's own marker rather than in an attribute, so isCard returns it.
var mobileSpec = cardSpec{
	isCard: func(marker string) (string, bool) {
		const prefix = "item-wrapper("
		if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, ")") {
			return "", false
		}
		return marker[len(prefix) : len(marker)-1], true
	},
	wantText: map[string]bool{
		"titleLabelGrid":             true,
		"priceLabelGrid":             true,
		"itemParamsGrid":             true,
		"addressLabel":               true,
		"sellerAndRatingSellerLabel": true,
	},
	wantAttr: map[string][]string{
		"item/link":   {"href", "data-marker-id"},
		"description": {"content"},
	},
}

// extractMobile reads the default collection surface.
//
// Two readers run and both are required: window.__initialData__ for the count
// and the site's own empty-result wording, and the item-wrapper cards for the
// listings. The state block carries no listings — measured — and the cards
// carry no count, so neither alone is a reading.
func extractMobile(d Document) (Page, bool) {
	page := Page{Surface: SurfaceMobile, Kind: EnvelopeCatalog}
	page.Report.Rule = RuleMobileCards
	page.Report.Path = PathDOM

	st, haveState := readMobileState(d.Body)
	if !haveState {
		// No container at all. The caller's classifier reads this as a soft
		// block; it is not our business to decide that here.
		return Page{}, false
	}
	switch {
	case st.Search.Count != nil:
		page.TotalCount = st.Search.Count
	case st.Search.MainCount != nil:
		page.TotalCount = st.Search.MainCount
	}
	page.EmptyDeclared = st.Search.EmptySearch != ""

	for _, c := range scanCards(d.Body, mobileSpec) {
		page.Report.Found++
		l, ok := decodeMobileCard(c, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, true
}

func decodeMobileCard(c cardFields, rep *ExtractionReport) (Listing, bool) {
	id, err := strconv.ParseInt(c.ID, 10, 64)
	if err != nil || id == 0 {
		rep.Warn(WarnItemDropped, "item-wrapper carried no usable id: "+c.ID)
		return Listing{}, false
	}
	// The id appears twice on this surface. Two identifiers that disagree mean
	// the card is not what it says it is, and a wrong id is worse than a
	// missing one: it attaches this reading to somebody else's history.
	if second, ok := c.attr("item/link", "data-marker-id"); ok {
		if got, err := strconv.ParseInt(second, 10, 64); err == nil && got != id {
			rep.Warn(WarnIDMismatch,
				strconv.FormatInt(id, 10)+" vs "+strconv.FormatInt(got, 10))
			return Listing{}, false
		}
	}
	l := Listing{
		ItemID:       id,
		Title:        c.text("titleLabelGrid"),
		LocationName: c.text("addressLabel"),
		SellerName:   c.text("sellerAndRatingSellerLabel"),
		PriceLabel:   c.text("priceLabelGrid"),
		PriceKind:    PriceAbsent,
		Price:        Money{Currency: "RUB"},
		// SortTimeMS stays nil. Measured: without s=104 there is no date on the
		// page at all, and with it every card read "1 час назад" — a resolution
		// of about an hour on a window that turns over inside it. A parsed value
		// would be precise enough to look trustworthy and wrong enough to order
		// arrivals incorrectly.
	}
	if href, ok := c.attr("item/link", "href"); ok {
		l.URLPath = trimQuery(href)
	}
	// The description is NOT available on this surface. Every one of the thirty
	// cards measured carries <meta itemProp="description" content="..."> — the
	// literal three dots, not the text — and no content attribute anywhere on
	// the page holds a description. It is read and discarded rather than not
	// read at all, so that the day Avito starts filling it in, this is the one
	// place that has to change.
	if desc, ok := c.attr("description", "content"); ok && desc != "..." {
		l.Description = desc
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
	// No priceDetailed on this surface, so the advisory cross-check does not
	// run and its counter is not touched: a surface that cannot disagree with
	// itself must not be recorded as agreeing.
	if l.URLPath == "" && l.Title == "" {
		rep.Warn(WarnItemDropped, "card carried neither a url nor a title")
		return Listing{}, false
	}
	return l, true
}
