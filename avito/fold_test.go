// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// TestFoldingIsCaseAndPunctuationBlindInRussian.
//
// Case folding and regexp do not mix well in Cyrillic, which is why the spec
// says this is a strings.Builder walk and not a pattern.
func TestFoldingIsCaseAndPunctuationBlindInRussian(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"iPhone 15, 128 ГБ", "iphone 15 128 гб"},
		{"IPHONE 15 — 128 ГБ!!!", "iphone 15 128 гб"},
		{"  iPhone   15,128  ГБ  ", "iphone 15 128 гб"},
		{"Ёлка", "ёлка"},
		{"ЁЛКА", "ёлка"},
		{"", ""},
		{"!!!", ""},
		{"   ", ""},
	} {
		if got := FoldTitle(c.in); got != c.want {
			t.Errorf("FoldTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFoldingIsStableUnderAnEditThatChangesNothingAPersonReads.
//
// The key is computed once at insert precisely so a later edit cannot move it.
// This is the other half: an edit that is only punctuation must not have
// produced a different key in the first place.
func TestFoldingIsStableUnderAnEditThatChangesNothingAPersonReads(t *testing.T) {
	a := FoldTitle("iPhone 15 Pro, 256 ГБ (как новый)")
	b := FoldTitle("iPhone 15 Pro 256 ГБ — как новый")
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}

// TestFoldingKeepsWordsApart.
//
// Punctuation collapses to a separator and not to nothing. «Продам-куплю» and
// «продамкуплю» are different titles, and a folding that ran them together
// would put two listings in one bucket on the strength of a hyphen.
func TestFoldingKeepsWordsApart(t *testing.T) {
	if FoldTitle("Продам-куплю") == FoldTitle("Продамкуплю") {
		t.Error("a hyphen vanished instead of becoming a separator")
	}
	if got := FoldTitle("Продам-куплю"); got != "продам куплю" {
		t.Errorf("FoldTitle = %q", got)
	}
}

// TestTheKeyIsEmptyWhenThereIsNothingToKeyOn.
//
// A key over four empty fields is one value shared by every listing that ever
// lacked them, which would make the candidate seek return the whole table.
// Empty is the honest answer and the linker skips it.
func TestTheKeyIsEmptyWhenThereIsNothingToKeyOn(t *testing.T) {
	if got := RepostKey(Listing{}); got != "" {
		t.Errorf("RepostKey of nothing = %q", got)
	}
	// A title alone is not enough either: measured, seven distinct titles
	// covered fifty listings.
	if got := RepostKey(Listing{Title: "iPhone 15, 128 ГБ"}); got != "" {
		t.Errorf("RepostKey of a bare title = %q, want empty", got)
	}
	// And a listing with the context but no readable title is not keyable
	// either: the title is the term the ladder actually compares.
	if got := RepostKey(Listing{
		SellerKey: "brand:bioniqo", MicroCategoryID: 10, LocationID: 1,
	}); got != "" {
		t.Errorf("RepostKey with no title = %q, want empty", got)
	}
	full := Listing{
		Title: "iPhone 15, 128 ГБ", SellerKey: "brand:bioniqo",
		MicroCategoryID: 10, LocationID: 637640,
	}
	if RepostKey(full) == "" {
		t.Error("a complete listing produced no key")
	}
}

// TestTheKeyMovesWithEachOfItsFourTerms, so that two sellers offering the same
// phone in the same place never share a bucket.
func TestTheKeyMovesWithEachOfItsFourTerms(t *testing.T) {
	base := Listing{
		Title: "iPhone 15, 128 ГБ", SellerKey: "brand:bioniqo",
		MicroCategoryID: 10, LocationID: 637640,
	}
	seen := map[string]string{RepostKey(base): "base"}
	for name, mutate := range map[string]func(Listing) Listing{
		"title":    func(l Listing) Listing { l.Title = "iPhone 15, 256 ГБ"; return l },
		"seller":   func(l Listing) Listing { l.SellerKey = "brand:other"; return l },
		"category": func(l Listing) Listing { l.MicroCategoryID = 11; return l },
		"location": func(l Listing) Listing { l.LocationID = 621540; return l },
	} {
		key := RepostKey(mutate(base))
		if prev, ok := seen[key]; ok {
			t.Errorf("changing the %s gave the same key as %s", name, prev)
		}
		seen[key] = name
	}
}

// TestTheTermsCannotBeShiftedIntoEachOther.
//
// Concatenated without a separator, ("ab","c") and ("a","bc") hash alike — and
// a seller key ending in a digit beside a category id is exactly that shape.
func TestTheTermsCannotBeShiftedIntoEachOther(t *testing.T) {
	a := RepostKey(Listing{Title: "т", SellerKey: "brand:x1", MicroCategoryID: 0, LocationID: 23})
	b := RepostKey(Listing{Title: "т", SellerKey: "brand:x", MicroCategoryID: 10, LocationID: 23})
	if a == b {
		t.Error("two different listings hashed to one key")
	}
}

// TestTheDescriptionDigestFoldsTheSameWayTheTitleDoes.
//
// Two incarnations of one advertisement are pasted from the same text, and a
// re-typed line break must not break the match. Empty for an empty
// description, because an absent signal must contribute nothing — a rule that
// matters more here than anywhere, since only twenty rows in fifty carry a
// description at all.
func TestTheDescriptionDigestFoldsTheSameWayTheTitleDoes(t *testing.T) {
	if DescriptionSHA("") != "" {
		t.Error("an empty description produced a digest")
	}
	if DescriptionSHA("   \n\t  ") != "" {
		t.Error("whitespace produced a digest")
	}
	a := DescriptionSHA("Продаю телефон.\nСостояние отличное!")
	b := DescriptionSHA("продаю телефон состояние отличное")
	if a != b {
		t.Errorf("%q != %q; the digest is not folded", a, b)
	}
	if DescriptionSHA("Продаю телефон") == DescriptionSHA("Продаю планшет") {
		t.Error("two different descriptions share a digest")
	}
}

// TestTheMeasuredCollisionCountsHold.
//
// These numbers are why increment 7 is split the way it is, and a change to the
// folding that moved them would change what the linker can do. From
// docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md.
//
// Forty-six distinct keys from fifty listings, and the four missing are four
// PAIRS: same seller, same micro-category, same location, same folded title,
// and both listings alive at the same moment. That is the dealer with eight
// identical phones the spec calls the common case, sitting on page one of an
// ordinary query — and it is what the co-existence veto exists for. Two things
// alive at once are two things, not one thing twice.
//
// Every one of the fifty gets a key, not only the forty-seven with a seller:
// micro-category and location are on all fifty, so the bucket is built from
// what there is. A bucket with no seller is simply a wider one, and the
// seller gate — hard, and undetermined when either side is unknown — is what
// caps such a pair at a proposal that can never suppress an alert.
//
// The mobile row expects zero keys rather than the measurement's nine. That is
// the same fact stated more usefully: the measurement keyed on a folded title
// alone and got nine buckets for thirty listings, and RepostKey refuses to
// build a key at all when the seller, category and location are all empty —
// which is the whole reason it refuses.
func TestTheMeasuredCollisionCountsHold(t *testing.T) {
	for _, c := range []struct {
		fixture        string
		listings       int
		distinctTitles int
		distinctKeys   int
	}{
		{"desktop-canonical-p1", 50, 7, 46},
		{"mobile-search-30", 30, 9, 0},
	} {
		page := parseFixture(t, c.fixture)
		if len(page.Listings) != c.listings {
			t.Fatalf("%s: %d listings", c.fixture, len(page.Listings))
		}
		titles, keys := map[string]bool{}, map[string]bool{}
		for _, l := range page.Listings {
			titles[FoldTitle(l.Title)] = true
			if k := RepostKey(l); k != "" {
				keys[k] = true
			}
		}
		if len(titles) != c.distinctTitles {
			t.Errorf("%s: %d distinct folded titles, measured %d",
				c.fixture, len(titles), c.distinctTitles)
		}
		if len(keys) != c.distinctKeys {
			t.Errorf("%s: %d distinct repost keys, want %d",
				c.fixture, len(keys), c.distinctKeys)
		}
	}
}
