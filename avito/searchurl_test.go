// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func query(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// TestTheSortIsForcedToDateAndNotDefaulted.
//
// The one sentence everything downstream rests on: page one of a DATE-SORTED
// query is the only window in which «new listing» is a set difference rather
// than a guess. A relevance-sorted page reshuffles on its own, so a listing
// appearing on it means nothing — and the whole lifecycle would be computing
// differences between two orderings of an unchanged market.
//
// Forced and not defaulted, so a pasted URL carrying somebody's own sort does
// not quietly become the watch's.
func TestTheSortIsForcedToDateAndNotDefaulted(t *testing.T) {
	for _, pasted := range []string{
		"https://www.avito.ru/moskva/telefony?q=iphone",
		"https://www.avito.ru/moskva/telefony?q=iphone&s=1",
		"https://m.avito.ru/moskva/telefony?q=iphone&s=default",
	} {
		got, err := SearchURL(pasted, SurfaceMobile, 1)
		if err != nil {
			t.Fatalf("%s: %v", pasted, err)
		}
		if s := query(t, got).Get("s"); s != sortByDate {
			t.Errorf("%s → s=%q, want %q", pasted, s, sortByDate)
		}
	}
}

// TestTheHostComesFromTheSurfaceAndNotFromWhatWasPasted.
//
// A watch set to the mobile surface must fetch m.avito.ru whatever its owner
// copied, or the surface setting is a label rather than a choice — and the
// budget arithmetic, which counts a desktop page as two requests, would be
// counting the wrong thing.
func TestTheHostComesFromTheSurfaceAndNotFromWhatWasPasted(t *testing.T) {
	for _, c := range []struct {
		pasted  string
		surface Surface
		want    string
	}{
		{"https://www.avito.ru/moskva/telefony?q=x", SurfaceMobile, "m.avito.ru"},
		{"https://m.avito.ru/moskva/telefony?q=x", SurfaceDesktop, "www.avito.ru"},
		{"https://avito.ru/moskva/telefony?q=x", SurfaceMobile, "m.avito.ru"},
	} {
		got, err := SearchURL(c.pasted, c.surface, 1)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != c.want {
			t.Errorf("%s as %s → %s, want %s", c.pasted, c.surface, u.Host, c.want)
		}
	}
}

// TestTheRegionAndCategoryAreCarried.
//
// Dropping the path silently widens a Moscow watch to the whole country, which
// looks exactly like the watch working and is a completely different market.
func TestTheRegionAndCategoryAreCarried(t *testing.T) {
	got, err := SearchURL(
		"https://www.avito.ru/moskva/telefony/mobilnye_telefony?q=iphone",
		SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/moskva/telefony/mobilnye_telefony") {
		t.Errorf("the path was lost: %s", got)
	}
}

// TestAPageWithNoPathBecomesAll, which is what avito.ru itself means by one.
func TestAPageWithNoPathBecomesAll(t *testing.T) {
	got, err := SearchURL("https://www.avito.ru?q=iphone", SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "/all") {
		t.Errorf("= %s", got)
	}
}

// TestDeepPaginationIsRefused.
//
// The third of three independent refusals — the schema's CHECK and the planner
// are the others — because one comment is not a control and the failure mode is
// somebody raising it «just for this one query». Walking past the first page or
// two is what turns a 429 into a 403, and a 403 costs the whole clearance.
func TestDeepPaginationIsRefused(t *testing.T) {
	if _, err := SearchURL("https://www.avito.ru/all?q=x", SurfaceMobile, 2); err != nil {
		t.Errorf("page two was refused: %v", err)
	}
	_, err := SearchURL("https://www.avito.ru/all?q=x", SurfaceMobile, 3)
	if !errors.Is(err, ErrDeepPagination) {
		t.Errorf("page three gave %v, want ErrDeepPagination", err)
	}
}

// TestTheFilterBlobIsCarriedAndNeverInterpreted.
//
// «f» is Avito's own encoding of whatever somebody ticked in the sidebar. A
// pasted URL carrying one this build does not understand is still fetched
// exactly as pasted, because the alternative is silently watching a different
// market from the one somebody chose.
func TestTheFilterBlobIsCarriedAndNeverInterpreted(t *testing.T) {
	blob := "ASgBAgICA0SywA2SoO0RtMANzqs5"
	got, err := SearchURL(
		"https://www.avito.ru/moskva/telefony?q=x&f="+blob, SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	if query(t, got).Get("f") != blob {
		t.Errorf("the filter blob did not survive: %s", got)
	}
}

// TestAPriceBandSurvives, because it is the thing the panel tells somebody to
// add when their watch is too broad.
func TestAPriceBandSurvives(t *testing.T) {
	got, err := SearchURL(
		"https://www.avito.ru/moskva/telefony?q=x&pmin=20000&pmax=60000",
		SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	q := query(t, got)
	if q.Get("pmin") != "20000" || q.Get("pmax") != "60000" {
		t.Errorf("the price band was lost: %s", got)
	}
}

// TestAnythingNotOnTheListIsDropped.
//
// Carrying everything would carry a session id or a click token somebody
// copied out of their address bar into every request this watch ever makes —
// for months, from one address, which is how an install becomes recognisable.
func TestAnythingNotOnTheListIsDropped(t *testing.T) {
	got, err := SearchURL(
		"https://www.avito.ru/moskva/telefony?q=x&sid=12345&utm_source=mail&iid=99",
		SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"sid", "utm_source", "iid"} {
		if query(t, got).Get(unwanted) != "" {
			t.Errorf("%q survived: %s", unwanted, got)
		}
	}
}

// TestSomethingThatIsNotAvitoIsRefused, rather than fetched.
func TestSomethingThatIsNotAvitoIsRefused(t *testing.T) {
	for _, bad := range []string{
		"", "not a url", "ftp://avito.ru/all",
		"https://avito.ru.example.com/all", "https://example.com/all?q=x",
	} {
		if _, err := SearchURL(bad, SurfaceMobile, 1); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestTheSameInputGivesTheSameURL, because a URL that varied between calls
// would give one watch two identities on the site.
func TestTheSameInputGivesTheSameURL(t *testing.T) {
	const pasted = "https://www.avito.ru/moskva/telefony?q=iphone&pmax=60000&f=ASgB"
	first, err := SearchURL(pasted, SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := SearchURL(pasted, SurfaceMobile, 1)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("call %d gave %s, first gave %s", i, again, first)
		}
	}
}

// TestAShopUrlIsASellerWatch.
//
// One request rather than two: /brands/<slug> is already canonical and needs no
// shell hop. Measured — a single fetch carrying its own state block with
// forty-five listings, which desktop.catalog.v1 reads unchanged.
func TestAShopUrlIsASellerWatch(t *testing.T) {
	s, err := ParseSearch("https://www.avito.ru/brands/bioniqo?src=search_seller_info")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != KindSeller {
		t.Errorf("Kind = %q", s.Kind)
	}
	got, err := s.URL(SurfaceDesktop, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://www.avito.ru/brands/bioniqo" {
		t.Errorf("= %s", got)
	}
	// No sort and no page: there is nothing to sort and one page to read.
	if strings.Contains(got, "s=") || strings.Contains(got, "p=") {
		t.Errorf("a seller page carried search parameters: %s", got)
	}
}

// TestASellerWatchIsAlwaysDesktop.
//
// The mobile spelling of a seller page was never measured, and an unmeasured
// claim in the one place this product promises to watch a competitor is not a
// saving.
func TestASellerWatchIsAlwaysDesktop(t *testing.T) {
	got, err := SearchURL("https://m.avito.ru/brands/bioniqo", SurfaceMobile, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://www.avito.ru/brands/") {
		t.Errorf("= %s", got)
	}
}

// TestAPrivateSellerIsRefusedByNameAndNotSilentlyBroken.
//
// A company has /brands/<slug>, which this product can read. A private seller
// is identified through a field that appears on about twenty rows in fifty, so
// a watch on one would be a watch that sometimes knew who it was watching —
// worse than one that says it cannot.
func TestAPrivateSellerIsRefusedByNameAndNotSilentlyBroken(t *testing.T) {
	_, err := ParseSearch("https://www.avito.ru/user/8f3c1d2e9a/profile/?src=search")
	if !errors.Is(err, ErrPrivateSeller) {
		t.Fatalf("= %v, want ErrPrivateSeller", err)
	}
	if !strings.Contains(err.Error(), "магазин") {
		t.Errorf("the refusal does not say what IS supported: %v", err)
	}
}

// TestASellerPageHasNoSecondPageHere, because max_pages is one for a seller
// watch and the schema records it as one.
func TestASellerPageHasNoSecondPage(t *testing.T) {
	if _, err := SearchURL("https://www.avito.ru/brands/x", SurfaceDesktop, 2); !errors.Is(
		err, ErrDeepPagination) {
		t.Errorf("= %v", err)
	}
}
