// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strconv"
	"strings"
)

const RuleDesktopMicrodata = "desktop.microdata.v1"

// desktopSpec is the one-pass description of a desktop search card.
var desktopSpec = cardSpec{
	isCard: func(marker string) (string, bool) {
		if marker == "item" {
			return "", true
		}
		return "", false
	},
	wantText: map[string]bool{
		"item-title":   true,
		"item-price":   true,
		"item-address": true,
	},
	wantAttr: map[string][]string{
		"url":   {"href"},
		"price": {"content"},
	},
}

// extractDesktopDOM reads the fifty server-rendered cards a desktop search page
// carries beside its hydration state.
//
// It is an independent second opinion over the same bytes, and it exists
// because loaderData.toggles carries runtime_catalog_drop_state_for_bots: Avito
// can serve a page with its state removed to a client it has classified. A
// parser with one path would go quiet and look healthy.
func extractDesktopDOM(d Document) (Page, bool) {
	page := Page{Surface: SurfaceDesktop, Kind: EnvelopeCatalog}
	page.Report.Rule = RuleDesktopMicrodata
	page.Report.Path = PathDOM

	for _, c := range scanCards(d.Body, desktopSpec) {
		page.Report.Found++
		l, ok := decodeDesktopCard(c, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, len(page.Listings) > 0
}

func decodeDesktopCard(c cardFields, rep *ExtractionReport) (Listing, bool) {
	l := Listing{
		Title:     c.text("item-title"),
		PriceKind: PriceNegotiable,
		Price:     Money{Currency: "RUB"},
	}
	if href, ok := c.attr("url", "href"); ok {
		l.URLPath = trimQuery(href)
	}

	// The id comes from data-item-id, and when that is absent it comes from the
	// listing path, which ends in the same number. Dropping a card for want of
	// one attribute would cost fidelity on exactly the page where the fallback
	// matters most — the one Avito served without its hydration state.
	id, err := strconv.ParseInt(c.ID, 10, 64)
	if err != nil || id == 0 {
		id = idFromPath(l.URLPath)
	}
	if id == 0 {
		rep.Warn(WarnItemDropped, "no usable id: data-item-id="+c.ID+" path="+l.URLPath)
		return Listing{}, false
	}
	l.ItemID = id

	l.LocationName = c.text("item-address")
	l.PriceLabel = c.text("item-price")
	// itemprop="price" carries the number without the site's formatting, which
	// is the better source when it is there; the label is the fallback.
	if content, ok := c.attr("price", "content"); ok {
		if roubles, err := strconv.ParseInt(content, 10, 64); err == nil {
			if minor, err := RoublesToMinor(roubles); err == nil {
				l.PriceKind = PriceExact
				l.Price.Minor = minor
			}
		}
	}
	if l.PriceKind != PriceExact {
		if minor, ok := DigitsToMinor(l.PriceLabel); ok {
			l.PriceKind = PriceExact
			l.Price.Minor = minor
		}
	}
	if l.URLPath == "" && l.Title == "" {
		rep.Warn(WarnItemDropped, "card carried neither a url nor a title")
		return Listing{}, false
	}
	return l, true
}

// idFromPath reads the trailing number of a listing path: every Avito listing
// URL ends in its own id.
func idFromPath(p string) int64 {
	i := strings.LastIndexByte(p, '_')
	if i < 0 || i == len(p)-1 {
		return 0
	}
	id, err := strconv.ParseInt(p[i+1:], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
