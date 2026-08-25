// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

// Surface is which of Avito's two front ends a page came from. It is not a
// cosmetic distinction: the desktop page carries a hydration catalogue and an
// absolute publication timestamp, the mobile page carries neither, and every
// rule about admissibility of evidence is written per surface because of it.
type Surface string

const (
	SurfaceDesktop Surface = "desktop"
	SurfaceMobile  Surface = "mobile"
)

// Path is which extractor answered. It is recorded on every snapshot and it is
// required rather than diagnostic: on the desktop surface a silent switch from
// state to dom is the earliest available warning that this install has been
// classified as a bot, earlier than a 429 and much earlier than a captcha.
type Path string

const (
	PathState Path = "state" // a JSON blob embedded in the page
	PathDOM   Path = "dom"   // server-rendered elements
)

// EnvelopeKind is what the document turned out to be.
type EnvelopeKind string

const (
	EnvelopeUnknown  EnvelopeKind = ""
	EnvelopeCatalog  EnvelopeKind = "catalog"  // a search or seller page
	EnvelopeItem     EnvelopeKind = "item"     // one listing's own page
	EnvelopeRedirect EnvelopeKind = "redirect" // the SPA shell, carrying only a canonical URL
)

// PriceKind separates the seller's own statement from our failure to read one.
// Collapsing them into a null price means that the first time Avito withholds
// the hydration state, thirty listings simultaneously "become negotiable".
type PriceKind string

const (
	PriceExact      PriceKind = "exact"
	PriceNegotiable PriceKind = "negotiable" // «Цена договорная» — the seller said so
	PriceAbsent     PriceKind = "absent"     // we did not read one; our silence, not theirs
)

// Listing is one advertisement as one extractor saw it in one document.
// Pointer fields are absent-or-present on purpose: a zero would be a claim.
type Listing struct {
	ItemID          int64
	URLPath         string
	Title           string
	CategoryID      int64
	CategoryRootID  int64
	MicroCategoryID int64
	LocationID      int64
	LocationName    string
	GeoAddress      string
	Lat, Lng        *float64
	CoordsPrecise   bool

	// Absolute epoch milliseconds as the site sent them, kept in their own unit
	// so nothing has to rescale a site-supplied fact. Always nil on the mobile
	// surface: it carries no usable date, and parsing "1 час назад" would give a
	// timestamp precise enough to look trustworthy and wrong enough to order
	// arrivals incorrectly.
	SortTimeMS  *int64
	AllowTimeMS *int64

	PriceKind                 PriceKind
	Price                     Money
	PriceLabel                string // the site's own rendering, verbatim
	PriceWithoutDiscountMinor *int64
	DiscountPct               *int64
	WasLowered                bool

	SellerKey  string // "brand:<slug>" or "" when the row does not identify one
	SellerName string
	IsCompany  *bool

	Description string

	ImagesCount *int64
	// ImageKey is the identity of the listing's first photo, shared by its six
	// size variants. Empty on the mobile surface, which carries no images at
	// all — see
	// docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md.
	ImageKey      string
	IsXL          *bool // an XL promotion placement
	IsReMap       *bool
	IsVerified    *bool
	IsReserved    *bool
	IsMarketplace *bool
	HasVideo      *bool
	HasDelivery   *bool

	// Item pages only. Views are public but cost one request each.
	ViewsTotal *int64
	ViewsToday *int64

	// Item pages only. The site's own words when a posting has ended, kept
	// verbatim and uninterpreted. Its presence is the only thing permitted to
	// call a listing closed.
	ClosedReason string
}

// AuditResult is the two extraction paths compared on one document.
type AuditResult struct {
	Agreed    int // ids both paths found
	OnlyState int // ids only the state path found
	OnlyDOM   int // ids only the DOM path found
}

// Page is one document, extracted.
type Page struct {
	Surface  Surface
	Kind     EnvelopeKind
	Listings []Listing

	// TotalCount is the site's own count for the whole result set:
	// catalog.totalCount on desktop, __initialData__.search.count on mobile.
	// Nil means the surface reported none, and a nil must never become
	// arithmetic on zero.
	TotalCount *int64

	// EmptyDeclared is the site saying, in its own words, that nothing matched.
	// It is what separates a real empty answer from a page we failed to read.
	EmptyDeclared bool

	// CanonicalURL is set only on EnvelopeRedirect: the SPA shell's whole
	// useful content.
	CanonicalURL string

	// Audit is set when both paths were run on purpose rather than because one
	// failed. It is how the agreement a golden fixture asserts gets calibrated
	// on live pages.
	Audit *AuditResult

	Report ExtractionReport
}

// Ok reports whether this page is a usable catalogue reading. No caller may
// substitute a status check for it.
func (p Page) Ok() bool {
	return p.Kind == EnvelopeCatalog && len(p.Listings) > 0
}
