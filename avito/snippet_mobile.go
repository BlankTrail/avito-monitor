// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// RuleMobileSnippet is the mobile search page in its «snippet» layout.
//
// Met on 11 October 2026 on a live query with a price band: the page said 831
// results and the extractor read none. The cards carried no item-wrapper and no
// link at all — snippetTitle, snippetPrice, snippetSellerInfo — because the
// listings now arrive in window.__initialData__ itself, under
// search.screenFeed.items[].itemSnippet, and the markup is drawn from them.
//
// The spec's measurement that «the state block carries no listings» was true of
// the grid layout and is still true of it; this is a second layout served
// alongside the first, not a replacement. Both are read, by whichever shape the
// page has.
//
// It carries more than the grid cards ever did: the category, the location
// id, a description, the seller's name and rating, delivery, reservation, and
// five size variants of the first photo — which gives the mobile surface an
// image key, one of the repost ladder's two strong signals that this surface
// had never been able to supply.
const RuleMobileSnippet = "mobile.snippet.v1"

// mobileSnippet is one itemSnippet as the page sends it.
type mobileSnippet struct {
	Analytics struct {
		CategoryID int64 `json:"categoryId"`
		ItemID     int64 `json:"itemId"`
		LocationID int64 `json:"locationId"`
		MicrocatID int64 `json:"microcatId"`
	} `json:"analytics"`
	Item struct {
		Description string `json:"description"`
		HasDelivery *bool  `json:"hasDelivery"`
		IsReserved  *bool  `json:"isReserved"`
		ItemID      string `json:"itemId"`
		Title       string `json:"title"`
		URI         string `json:"uri"`
	} `json:"item"`
	Price *struct {
		Value struct {
			Text string `json:"text"`
		} `json:"value"`
	} `json:"price"`
	Seller *struct {
		Name   string `json:"name"`
		Rating *struct {
			Reviews string `json:"reviewsCountFormatted"`
			Score   string `json:"scoreFormatted"`
		} `json:"rating"`
	} `json:"seller"`
	FirstGalleryImage map[string]string `json:"firstGalleryImage"`
}

// decodeSnippets reads the state's listings, in page order.
func decodeSnippets(items []mobileFeedItem, rep *ExtractionReport) []Listing {
	var out []Listing
	for _, it := range items {
		s := it.ItemSnippet
		if s == nil {
			// Banners and widgets share the feed with listings.
			continue
		}
		rep.Found++
		l, ok := decodeSnippet(*s, rep)
		if !ok {
			rep.Dropped++
			continue
		}
		out = append(out, l)
	}
	return out
}

func decodeSnippet(s mobileSnippet, rep *ExtractionReport) (Listing, bool) {
	id, err := strconv.ParseInt(s.Item.ItemID, 10, 64)
	if err != nil || id == 0 {
		id = s.Analytics.ItemID
	}
	if id == 0 {
		rep.Warn(WarnItemDropped, "itemSnippet carried no usable id")
		return Listing{}, false
	}
	// Two identifiers that disagree: same rule as the grid cards.
	if s.Analytics.ItemID != 0 && s.Analytics.ItemID != id {
		rep.Warn(WarnIDMismatch, strconv.FormatInt(id, 10)+" vs "+
			strconv.FormatInt(s.Analytics.ItemID, 10))
		return Listing{}, false
	}
	l := Listing{
		ItemID:          id,
		Title:           strings.TrimSpace(s.Item.Title),
		URLPath:         snippetPath(s.Item.URI),
		CategoryID:      s.Analytics.CategoryID,
		MicroCategoryID: s.Analytics.MicrocatID,
		LocationID:      s.Analytics.LocationID,
		Description:     s.Item.Description,
		IsReserved:      s.Item.IsReserved,
		HasDelivery:     s.Item.HasDelivery,
		PriceKind:       PriceAbsent,
		Price:           Money{Currency: "RUB"},
	}
	if s.Seller != nil {
		l.SellerName = strings.TrimSpace(s.Seller.Name)
		if s.Seller.Rating != nil {
			if h, ok := ratingHundredths(s.Seller.Rating.Score); ok {
				l.SellerRatingHundredths = &h
			}
			l.SellerReviewSummary = strings.Trim(s.Seller.Rating.Reviews, "() ")
		}
	}
	if s.Price != nil {
		l.PriceLabel = strings.TrimSpace(s.Price.Value.Text)
	}
	switch {
	case l.PriceLabel == "":
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
	if len(s.FirstGalleryImage) > 0 {
		variants := make([]string, 0, len(s.FirstGalleryImage))
		for _, u := range s.FirstGalleryImage {
			variants = append(variants, u)
		}
		// Map order is random; the key is a common prefix and must not be.
		sort.Strings(variants)
		l.ImageKey = ImageKey(variants)
	}
	if l.URLPath == "" && l.Title == "" {
		rep.Warn(WarnItemDropped, "itemSnippet carried neither a url nor a title")
		return Listing{}, false
	}
	return l, true
}

// snippetPath is the web path inside the app link the snippet carries:
// ru.avito://1/item/show?context=…&itemId=…&uri=%2Fmoskva%2F…_8405486366%3Fcontext%3D…
func snippetPath(uri string) string {
	i := strings.IndexByte(uri, '?')
	if i < 0 {
		return ""
	}
	q, err := url.ParseQuery(uri[i+1:])
	if err != nil {
		return ""
	}
	p := q.Get("uri")
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return trimQuery(p)
}

// ratingHundredths reads «5,0» or «4.8» as hundredths of a point.
func ratingHundredths(s string) (int64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 5 {
		return 0, false
	}
	return int64(f*100 + 0.5), true
}
