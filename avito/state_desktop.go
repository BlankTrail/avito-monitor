// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	RuleDesktopCatalog  = "desktop.catalog.v1"
	RuleDesktopProfile  = "desktop.profile.v1"
	RuleDesktopProbe    = "desktop.catalog.probe"
	RuleDesktopRedirect = "desktop.redirect.v1"
)

// mfeStateAttr is what the state block is selected by. Never the script type.
const mfeStateAttr = "data-mfe-state"

// dig walks a path of object keys and returns the raw value at the end of it,
// or nil if any step is missing or is not an object.
func dig(root json.RawMessage, path ...string) json.RawMessage {
	cur := root
	for _, key := range path {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(cur, &obj); err != nil {
			return nil
		}
		next, ok := obj[key]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// itemsAt reads an items array and the count that belongs beside it.
func itemsAt(root json.RawMessage, countKey string, path ...string) ([]json.RawMessage, *int64, bool) {
	node := dig(root, path...)
	if node == nil {
		return nil, nil, false
	}
	var box struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(node, &box); err != nil || len(box.Items) == 0 {
		return nil, nil, false
	}
	var counts map[string]json.RawMessage
	var total *int64
	if json.Unmarshal(node, &counts) == nil {
		if raw, ok := counts[countKey]; ok {
			var n int64
			if json.Unmarshal(raw, &n) == nil {
				total = &n
			}
		}
	}
	return box.Items, total, true
}

// stateRule is one named way of finding the listings inside the state block.
//
// They are tried in order and the one that answered is recorded, because the
// answer is a fact about the page rather than an implementation detail: a run
// where a different rule starts answering is a page whose structure moved.
type stateRule struct {
	name string
	find func(root json.RawMessage) ([]json.RawMessage, *int64, bool)
}

// stateRules is the ordered table. Two envelopes are known and they share
// nothing at the top: a search page is loaderData.data.catalog, a seller page
// is state.searchData.profileCatalog with no loaderData at all. Both carry the
// same item objects underneath, which is why one decoder serves both.
var stateRules = []stateRule{
	{RuleDesktopCatalog, func(r json.RawMessage) ([]json.RawMessage, *int64, bool) {
		items, total, ok := itemsAt(r, "totalCount", "loaderData", "data", "catalog")
		if ok && total == nil {
			// A search page states the count one level up from its catalogue.
			if raw := dig(r, "loaderData", "data", "totalCount"); raw != nil {
				var n int64
				if json.Unmarshal(raw, &n) == nil {
					total = &n
				}
			}
		}
		return items, total, ok
	}},
	{RuleDesktopProfile, func(r json.RawMessage) ([]json.RawMessage, *int64, bool) {
		return itemsAt(r, "totalCount", "state", "searchData", "profileCatalog")
	}},
}

// extractDesktopState reads the one data-mfe-state block a desktop page
// carries. It serves the search page, the seller page and the SPA shell alike:
// all three use the same block, which is why one extractor covers them.
func extractDesktopState(d Document) (Page, bool) {
	text, scriptType, found := scriptTextByAttr(d.Body, mfeStateAttr, "true")
	if !found {
		return Page{}, false
	}
	root := json.RawMessage(text)
	var probeObj map[string]json.RawMessage
	if err := json.Unmarshal(root, &probeObj); err != nil {
		return Page{}, false
	}
	page := Page{Surface: SurfaceDesktop}
	page.Report.Path = PathState
	page.Report.ScriptType = scriptType

	// The shell first: it has no catalogue and its whole content is one URL.
	if data := dig(root, "loaderData", "data"); data != nil {
		var redirect struct {
			Redirected bool   `json:"redirected"`
			URL        string `json:"url"`
		}
		if err := json.Unmarshal(data, &redirect); err == nil &&
			redirect.Redirected && redirect.URL != "" {
			page.Kind = EnvelopeRedirect
			page.CanonicalURL = redirect.URL
			page.Report.Rule = RuleDesktopRedirect
			return page, true
		}
	}

	var raw []json.RawMessage
	rule := ""
	for _, r := range stateRules {
		if items, total, ok := r.find(root); ok {
			raw, page.TotalCount, rule = items, total, r.name
			break
		}
	}
	if rule == "" {
		// No named rule answered. Fall to the shape probe, which matches on
		// structure rather than on any key's name, and always warns.
		probe := probeForItems(root, 0)
		if len(probe) == 0 {
			return Page{}, false
		}
		raw = probe
		rule = RuleDesktopProbe
		page.Report.Warn(WarnShapeProbe,
			"items reached by structure; no named rule matched this envelope")
	}

	page.Kind = EnvelopeCatalog
	page.Report.Rule = rule
	page.Report.Found = len(raw)
	for _, item := range raw {
		l, ok := decodeStateListing(item, &page.Report)
		if !ok {
			page.Report.Dropped++
			continue
		}
		page.Listings = append(page.Listings, l)
	}
	return page, true
}

// maxProbeDepth bounds the structural search. Six is deeper than any envelope
// observed and shallow enough that a pathological payload cannot make the
// probe the expensive part of a poll.
const maxProbeDepth = 6

// probeForItems finds an array of objects a majority of which carry a numeric
// id and a string urlPath. It matches on shape and never on the key's name,
// because the name is the thing that moved.
func probeForItems(raw json.RawMessage, depth int) []json.RawMessage {
	if depth > maxProbeDepth {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		hits := 0
		for _, e := range arr {
			var probe struct {
				ID      *int64  `json:"id"`
				URLPath *string `json:"urlPath"`
			}
			if json.Unmarshal(e, &probe) == nil && probe.ID != nil && probe.URLPath != nil {
				hits++
			}
		}
		if hits*2 > len(arr) {
			return arr
		}
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	// Map iteration order is random, so a payload with two candidate arrays
	// would answer differently between runs. Walk the keys in order.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		if got := probeForItems(obj[k], depth+1); got != nil {
			return got
		}
	}
	return nil
}

// sortStrings is an insertion sort: these slices are the key sets of one JSON
// object, so they are small, and it keeps sort out of the import list for one
// use in one place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// stateItem mirrors the fields measured on every listing of a desktop page.
type stateItem struct {
	ID              int64  `json:"id"`
	URLPath         string `json:"urlPath"`
	Title           string `json:"title"`
	CategoryID      int64  `json:"categoryId"`
	MicroCategoryID int64  `json:"microCategoryId"`
	LocationID      int64  `json:"locationId"`
	SortTimeStamp   *int64 `json:"sortTimeStamp"`
	AllowTimeStamp  *int64 `json:"allowTimeStamp"`
	Category        struct {
		RootID int64 `json:"rootId"`
	} `json:"category"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Geo struct {
		FormattedAddress string `json:"formattedAddress"`
	} `json:"geo"`
	Coords struct {
		Lat       string `json:"lat"`
		Lng       string `json:"lng"`
		Precision int    `json:"precision"`
	} `json:"coords"`
	PriceDetailed *struct {
		Value      int64  `json:"value"`
		String     string `json:"string"`
		FullString string `json:"fullString"`
		HasValue   *bool  `json:"hasValue"`
		WasLowered bool   `json:"wasLowered"`
	} `json:"priceDetailed"`
	PriceWithoutDiscount *int64 `json:"priceWithoutDiscount"`
	DiscountPercent      *int64 `json:"discountPercent"`
	ImagesCount          *int64 `json:"imagesCount"`
	IsXL                 *bool  `json:"isXl"`
	IsReMapPromotion     *bool  `json:"isReMapPromotion"`
	IsVerifiedItem       *bool  `json:"isVerifiedItem"`
	IsReserved           *bool  `json:"isReserved"`
	IsMarketplace        *bool  `json:"isMarketplace"`
	HasVideo             *bool  `json:"hasVideo"`
	Contacts             *struct {
		Delivery bool `json:"delivery"`
	} `json:"contacts"`
	Images   []map[string]string `json:"images"`
	UserLogo *struct {
		Link string `json:"link"`
	} `json:"userLogo"`
	IVA *struct {
		DescriptionStep []struct {
			Payload struct {
				Description string `json:"description"`
			} `json:"payload"`
		} `json:"DescriptionStep"`
		SecondLineStep []struct {
			Payload struct {
				Value string `json:"value"`
			} `json:"payload"`
		} `json:"SecondLineStep"`
	} `json:"iva"`
}

func decodeStateListing(raw json.RawMessage, rep *ExtractionReport) (Listing, bool) {
	var it stateItem
	if err := json.Unmarshal(raw, &it); err != nil {
		rep.Warn(WarnItemDropped, "json: "+err.Error())
		return Listing{}, false
	}
	if it.ID == 0 || it.URLPath == "" {
		rep.Warn(WarnItemDropped, "no id or no urlPath")
		return Listing{}, false
	}
	l := Listing{
		ItemID: it.ID,
		// The ?context= suffix is a per-request token. Keeping it would make
		// every poll look like a changed listing.
		URLPath:         trimQuery(it.URLPath),
		Title:           it.Title,
		CategoryID:      it.CategoryID,
		CategoryRootID:  it.Category.RootID,
		MicroCategoryID: it.MicroCategoryID,
		LocationID:      it.LocationID,
		LocationName:    it.Location.Name,
		GeoAddress:      it.Geo.FormattedAddress,
		CoordsPrecise:   it.Coords.Precision == 0,
		SortTimeMS:      it.SortTimeStamp,
		AllowTimeMS:     it.AllowTimeStamp,
		PriceKind:       PriceNegotiable,
		Price:           Money{Currency: "RUB"},
		ImagesCount:     it.ImagesCount,
		IsXL:            it.IsXL,
		IsReMap:         it.IsReMapPromotion,
		IsVerified:      it.IsVerifiedItem,
		IsReserved:      it.IsReserved,
		IsMarketplace:   it.IsMarketplace,
		HasVideo:        it.HasVideo,
	}
	if f, err := strconv.ParseFloat(it.Coords.Lat, 64); err == nil {
		l.Lat = &f
	}
	if f, err := strconv.ParseFloat(it.Coords.Lng, 64); err == nil {
		l.Lng = &f
	}
	if it.Contacts != nil {
		d := it.Contacts.Delivery
		l.HasDelivery = &d
	}
	// The first photo only. A listing's identity is not the set of its photos —
	// a seller who re-uploads may reorder or drop some — and the first is the
	// one both incarnations are most likely to share. Taking all of them would
	// mean deciding what a partial overlap means, and this signal is far too
	// weak to carry that judgement.
	if len(it.Images) > 0 {
		variants := make([]string, 0, len(it.Images[0]))
		for _, url := range it.Images[0] {
			variants = append(variants, url)
		}
		l.ImageKey = ImageKey(variants)
	}
	if it.UserLogo != nil && it.UserLogo.Link != "" {
		if slug := brandSlug(it.UserLogo.Link); slug != "" {
			l.SellerKey = "brand:" + slug
		}
	}
	if it.IVA != nil {
		if len(it.IVA.DescriptionStep) > 0 {
			l.Description = it.IVA.DescriptionStep[0].Payload.Description
		}
		if len(it.IVA.SecondLineStep) > 0 && it.IVA.SecondLineStep[0].Payload.Value == "Компания" {
			yes := true
			l.IsCompany = &yes
		}
	}
	if pd := it.PriceDetailed; pd != nil {
		l.PriceLabel = pd.FullString
		l.WasLowered = pd.WasLowered
		hasValue := pd.HasValue == nil || *pd.HasValue
		if hasValue && pd.Value > 0 {
			if minor, err := RoublesToMinor(pd.Value); err == nil {
				l.PriceKind = PriceExact
				l.Price.Minor = minor
				// Advisory only: it counts and never rejects. A collapse in the
				// agreement rate is the unit having changed.
				if dm, ok := DigitsToMinor(pd.String); ok && dm != minor {
					rep.Warn(WarnPriceMismatch, pd.String)
				}
			}
		}
		l.PriceWithoutDiscountMinor = minorOrNil(it.PriceWithoutDiscount)
		l.DiscountPct = it.DiscountPercent
	}
	return l, true
}

func minorOrNil(roubles *int64) *int64 {
	if roubles == nil {
		return nil
	}
	m, err := RoublesToMinor(*roubles)
	if err != nil {
		return nil
	}
	return &m
}

// trimQuery drops the query and the fragment from a listing path.
func trimQuery(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		return p[:i]
	}
	return p
}

// brandSlug reads the slug out of "/brands/<slug>?...".
func brandSlug(link string) string {
	const prefix = "/brands/"
	if !strings.HasPrefix(link, prefix) {
		return ""
	}
	return trimQuery(strings.TrimPrefix(link, prefix))
}
