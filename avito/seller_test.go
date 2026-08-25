// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// TestAProfileLinkBecomesAKeyThatSaysWhereItCameFrom.
//
// Two spellings and no third. Inventing an identity that silently merges two
// people is worse than declining to identify either, so anything unrecognised
// yields nothing at all rather than a guess — and the schema's key_quality has
// no value meaning «we made this up».
func TestAProfileLinkBecomesAKeyThatSaysWhereItCameFrom(t *testing.T) {
	for _, c := range []struct{ link, wantKey, wantQuality string }{
		{"/brands/bioniqo?src=search_seller_info&iid=1", "brand:bioniqo", "slug"},
		{"/brands/bioniqo", "brand:bioniqo", "slug"},
		{"/user/8f3c1d2e9a/profile/?src=search", "user:8f3c1d2e9a", "profile"},
		{"/user/8f3c1d2e9a/", "user:8f3c1d2e9a", "profile"},
		{"/company/whatever", "", ""},
		{"/user/", "", ""},
		{"", "", ""},
	} {
		key, quality := sellerKeyFromLink(c.link)
		if key != c.wantKey || quality != c.wantQuality {
			t.Errorf("%q → (%q, %q), want (%q, %q)",
				c.link, key, quality, c.wantKey, c.wantQuality)
		}
	}
}

// TestOneSellerKeepsOneKeyAcrossTheirListings.
//
// The link carries a per-listing iid in its query. Left in, the same seller
// would get a different key on every listing and the repost key would never
// bucket two of their listings together — which is the one thing it exists to
// do.
func TestOneSellerKeepsOneKeyAcrossTheirListings(t *testing.T) {
	first, _ := sellerKeyFromLink("/brands/bioniqo?src=search_seller_info&iid=7775028313")
	second, _ := sellerKeyFromLink("/brands/bioniqo?page_from=from_items_list_icon&iid=9000000001")
	if first != second {
		t.Errorf("%q != %q; the per-listing id leaked into the key", first, second)
	}
}

// TestTheSellerIsReadFromBothSpellingsOnARealPage.
//
// Only userLogo was ever read. The spec names a second spelling —
// iva.UserInfoStep[].payload.profile — which is in the payload on four rows in
// ten and carries the display name and the rating besides.
func TestTheSellerIsReadFromBothSpellingsOnARealPage(t *testing.T) {
	page := parseFixture(t, "desktop-canonical-p1")

	keyed, named, rated, slug, profile := 0, 0, 0, 0, 0
	for _, l := range page.Listings {
		if l.SellerKey != "" {
			keyed++
		}
		switch l.SellerKeyQuality {
		case "slug":
			slug++
		case "profile":
			profile++
		}
		if l.SellerName != "" {
			named++
		}
		if l.SellerRatingHundredths != nil {
			rated++
		}
	}
	t.Logf("of %d: keyed=%d (slug=%d profile=%d) named=%d rated=%d",
		len(page.Listings), keyed, slug, profile, named, rated)

	// Forty-seven, and the measurement of 2026-08-25 found forty-six.
	//
	// The extra one is checked and not adopted: listing 8188051319 carries no
	// userLogo at all and does carry a UserInfoStep profile pointing at
	// /brands/yablomarket. Reading the second spelling recovered it, which is
	// the point of reading the second spelling — the measurement counted what
	// the build could see at the time, and this build sees one more.
	//
	// Its quality is "slug" and not "profile" because the quality names the
	// shape of the link, not the field it was found in: a brand link is a
	// brand link wherever it was written.
	//
	// A build that reads fewer has lost something; one that reads more has
	// started inventing, and the next number to move should be explained the
	// same way this one was.
	if keyed != 47 {
		t.Errorf("%d listings carry a seller key, want 47", keyed)
	}
	if named == 0 {
		t.Error("no display name was read; UserInfoStep carries one")
	}
	if rated == 0 {
		t.Error("no rating was read; UserInfoStep carries one")
	}
	// A key is never recorded without saying how it was obtained: a heuristic
	// key is never accepted as repost evidence, and that rule can only be
	// enforced if the quality travelled with the key.
	for _, l := range page.Listings {
		if l.SellerKey != "" && l.SellerKeyQuality == "" {
			t.Errorf("listing %d has a key with no quality", l.ItemID)
			break
		}
	}
}

// TestARatingIsRoundedAndNotTruncated.
//
// 4.9 in float64 is 4.899999…, and truncating would file every seller a
// hundredth low for ever — a systematic error that looks like data.
func TestARatingIsRoundedAndNotTruncated(t *testing.T) {
	page := parseFixture(t, "desktop-canonical-p1")
	for _, l := range page.Listings {
		if l.SellerRatingHundredths == nil {
			continue
		}
		if got := *l.SellerRatingHundredths; got%10 != 0 && got%5 != 0 {
			// Avito publishes one decimal place, so a hundredths value must be
			// a multiple of ten. 489 means the float was truncated.
			t.Errorf("rating = %d, which is not a whole tenth of a point", got)
		}
	}
}

// TestTheMobileSurfaceIdentifiesNoSellerAtAll, which is the fact the whole
// enrichment path exists for.
func TestTheMobileSurfaceIdentifiesNoSellerAtAll(t *testing.T) {
	page := parseFixture(t, "mobile-search-30")
	for _, l := range page.Listings {
		if l.SellerKey != "" {
			t.Errorf("the mobile surface produced a seller key: %q", l.SellerKey)
		}
	}
}

// TestAnItemPageIdentifiesItsSeller.
//
// This is the one field enrichment exists to recover. A listing collected from
// the mobile surface has no seller at all, and the seller is the repost
// ladder's one hard gate — so an item page that could not name one would make
// the whole enrichment path spend a request per listing and learn nothing.
//
// From the link and not from the text: the seller block's text runs the name,
// the rating and the join date together with no separator
// («Re:Market4,91898 отзывовКомпания · На Авито c 2020 года»), which is not
// something to parse a name out of.
func TestAnItemPageIdentifiesItsSeller(t *testing.T) {
	page := parseFixture(t, "desktop-item")
	if len(page.Listings) != 1 {
		t.Fatalf("%d listings on an item page", len(page.Listings))
	}
	l := page.Listings[0]
	if l.SellerKey != "brand:bioniqo" {
		t.Errorf("SellerKey = %q", l.SellerKey)
	}
	if l.SellerKeyQuality != "slug" {
		t.Errorf("SellerKeyQuality = %q", l.SellerKeyQuality)
	}
	if l.SellerProfilePath != "/brands/bioniqo" {
		t.Errorf("SellerProfilePath = %q", l.SellerProfilePath)
	}
}

// TestTheAttributeScannerReadsTheRightAttributeWhateverTheOrder.
//
// The tokenizer's attribute cursor only moves forward, so reading data-marker
// and then href with two walks finds the second only when the document happened
// to write them in that order. Both orders are in the wild.
func TestTheAttributeScannerReadsTheRightAttributeWhateverTheOrder(t *testing.T) {
	body := []byte(`<a data-marker="seller-link/link" href="/brands/first">x</a>` +
		`<a href="/brands/second" data-marker="other/link">y</a>`)
	got := scanMarkerAttrs(body, map[string]bool{
		"seller-link/link": true, "other/link": true,
	}, "href")
	if got["seller-link/link"] != "/brands/first" {
		t.Errorf("marker-then-href = %q", got["seller-link/link"])
	}
	if got["other/link"] != "/brands/second" {
		t.Errorf("href-then-marker = %q", got["other/link"])
	}
}

// TestARepeatedMarkerTakesTheFirst, so the answer does not depend on how far
// the page got before it was truncated.
func TestARepeatedMarkerTakesTheFirst(t *testing.T) {
	body := []byte(`<a data-marker="m" href="/brands/first">x</a>` +
		`<a data-marker="m" href="/brands/second">y</a>`)
	if got := scanMarkerAttrs(body, map[string]bool{"m": true}, "href"); got["m"] != "/brands/first" {
		t.Errorf("= %q", got["m"])
	}
}
