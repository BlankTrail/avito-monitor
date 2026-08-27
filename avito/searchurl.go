// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The entry point, and it is pure: a pasted URL in, a URL to fetch out, no
// clock and no network.
//
// It exists because of one sentence in the design that everything downstream
// rests on: page one of a DATE-SORTED query is the only window in which «new
// listing» is a set difference rather than a guess. A relevance-sorted page —
// which is what avito.ru gives anybody who has not asked otherwise — reshuffles
// on its own, so a listing appearing on it means nothing at all, and the whole
// lifecycle would be computing differences between two orderings of the same
// unchanged market.
//
// Before this, the pasted URL was fetched exactly as pasted.

// ErrDeepPagination is returned for a page past the second.
//
// Refused here as well as by the schema's CHECK and by the planner, and three
// independent refusals is deliberate: one comment is not a control, and the
// failure mode is somebody raising it «just for this one query». Walking past
// the first page or two is what turns a 429 into a 403, and a 403 costs the
// cookie jar and therefore the clearance the jar was carrying.
var ErrDeepPagination = errors.New("avito: дальше второй страницы ходить нельзя")

// ErrNotASearch is returned for something that is not an Avito search page.
var ErrNotASearch = errors.New("avito: это не похоже на страницу поиска Авито")

// ErrPrivateSeller is returned for a private seller's profile.
//
// Named rather than silently broken, which is the spec's own wording. A company
// has /brands/<slug>, which is a page this product can read; a private seller is
// identified through iva.UserInfoStep[].payload.profile, which appears on about
// twenty rows in fifty — so a watch on one would be a watch that sometimes knew
// who it was watching. M1 supports company seller watches and says so.
var ErrPrivateSeller = errors.New(
	"avito: следить можно за магазином (ссылка вида /brands/…), " +
		"а за частным продавцом эта версия не умеет")

// Kind is what a watch is watching.
type Kind string

const (
	// KindSearch is a query's first page, sorted by date.
	KindSearch Kind = "search"
	// KindSeller is a company's own page.
	//
	// A desktop watch and one request rather than two: /brands/<slug> is
	// already canonical and needs no shell hop — the measurement found a single
	// fetch carrying its own data-mfe-state block with forty-five listings in
	// it, which desktop.catalog.v1 reads unchanged.
	//
	// It is a desktop watch because the mobile spelling of a seller page was
	// never measured, and an unmeasured claim in the one place this product
	// promises to watch a competitor is not a saving.
	KindSeller Kind = "seller"
)

// sortByDate is Avito's own value for «сначала новые».
//
// Fixed and not user-configurable in M1, which is a decision rather than an
// omission: every other ordering makes the window a ranking rather than a
// timeline, and a monitor built on a ranking reports reshuffles as news.
const sortByDate = "104"

// keptParams are the query parameters carried through from a pasted URL.
//
// A closed list, and everything outside it is dropped. The opposite — carrying
// everything — would carry a session id or a click token somebody copied out of
// their address bar into every request this watch ever makes.
//
// «f» is Avito's own filter blob and is passed through without being
// interpreted: a pasted URL carrying one this build does not understand is
// still fetched exactly as pasted, which is the whole reason it is on the list.
var keptParams = []string{
	"q", "pmin", "pmax", "f", "user", "cd", "radius", "localPriority",
	"district", "metro", "geoCoords",
}

// Search is a pasted URL, parsed.
type Search struct {
	// Kind is what this URL turned out to be.
	Kind Kind
	// Path is the region and category, e.g. "/moskva/telefony". Carried and
	// never dropped: dropping it silently widens a Moscow watch to the whole
	// country, which looks like the watch working and is a different market.
	Path   string
	Params url.Values
}

// ParseSearch reads a pasted Avito URL.
func ParseSearch(raw string) (Search, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Search{}, fmt.Errorf("avito: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Search{}, ErrNotASearch
	}
	host := strings.ToLower(u.Hostname())
	if host != "avito.ru" && host != "www.avito.ru" && host != "m.avito.ru" {
		return Search{}, ErrNotASearch
	}

	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/all"
	}

	// A private seller's profile, refused by name. The alternative — accepting
	// it and watching whatever /user/<hash> happens to render — is a watch that
	// sometimes knows who it is watching, which is worse than one that says it
	// cannot.
	if strings.HasPrefix(path, "/user/") {
		return Search{}, ErrPrivateSeller
	}
	if slug := brandSlug(path); slug != "" {
		// Everything after the slug is Avito's own navigation inside the shop
		// and is dropped: the watch is on the shop.
		return Search{Kind: KindSeller, Path: "/brands/" + slug}, nil
	}

	in := u.Query()
	out := url.Values{}
	for _, name := range keptParams {
		if v := in.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return Search{Kind: KindSearch, Path: path, Params: out}, nil
}

// URL is what to fetch for one surface and one page.
//
// The host comes from the surface and never from what was pasted: a watch set
// to the mobile surface must fetch m.avito.ru whatever its owner copied, or the
// surface setting is a label rather than a choice.
func (s Search) URL(surface Surface, page int) (string, error) {
	if page < 1 {
		page = 1
	}
	if page > 2 {
		return "", ErrDeepPagination
	}
	host := "m.avito.ru"
	if surface == SurfaceDesktop {
		host = "www.avito.ru"
	}
	path := s.Path
	if path == "" {
		path = "/all"
	}

	// A seller page is already canonical: no sort, no page, nothing to add. It
	// is one request rather than two for exactly that reason.
	if s.Kind == KindSeller {
		if page > 1 {
			return "", ErrDeepPagination
		}
		return (&url.URL{Scheme: "https", Host: "www.avito.ru", Path: path}).String(), nil
	}

	q := url.Values{}
	for name, values := range s.Params {
		for _, v := range values {
			q.Add(name, v)
		}
	}
	// Forced, not defaulted. A pasted URL carrying s=1 is somebody's
	// relevance-sorted browsing, and honouring it would make every later
	// conclusion about arrivals meaningless while looking like it worked.
	q.Set("s", sortByDate)
	q.Set("p", strconv.Itoa(page))

	return (&url.URL{
		Scheme: "https", Host: host, Path: path, RawQuery: q.Encode(),
	}).String(), nil
}

// SearchURL is the whole entry point in one call: pasted URL to URL to fetch.
func SearchURL(raw string, surface Surface, page int) (string, error) {
	s, err := ParseSearch(raw)
	if err != nil {
		return "", err
	}
	return s.URL(surface, page)
}
