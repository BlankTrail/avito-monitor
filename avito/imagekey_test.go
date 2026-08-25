// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// parseFixture is loadFixture plus Extract, which is what every test that cares
// about the result rather than the plumbing wants.
func parseFixture(t *testing.T, name string) Page {
	t.Helper()
	doc, m := loadFixture(t, name)
	page, err := Extract(doc, Options{Now: m.FrozenNow})
	if err != nil {
		t.Fatalf("Extract(%s): %v", name, err)
	}
	return page
}

// TestSixVariantsOfOnePhotoCollapseToOneKey.
//
// The variants of one photo differ in the middle of the token, not at its end —
// measured, against a spec sentence that said the opposite. So the key is the
// longest common prefix and nothing cleverer: naming the parts would be a guess
// about a URL grammar nobody has documented.
func TestSixVariantsOfOnePhotoCollapseToOneKey(t *testing.T) {
	variants := []string{
		"https://90.img.avito.st/image/1/1.ureJN7aAFl6fl7Rf8XWXrJuXFFo7gBJeO-d3Wjs0F_w8lBJEn5e0Xz8.-kq1kcp2wRP5LujA7tnRrIB3SJhcgGD30i2CPDtLtSE",
		"https://90.img.avito.st/image/1/1.ureJN7aAFl7nl8xf8XWXrJuXFFo7gBJeO-d3WjtMF4Q8lBJE55fMXz8.h9Ny0zSB3JaYOG-beY_lT2ESt8YvhzUF3yl7-x2jhvU",
		"https://90.img.avito.st/image/1/1.ureJN7aAFl7DkOhY8XWXrJuXFFo7gBJeO-d3WjtoEKA7lBJEw5DoWD8.7Jh5M9mZ3pQePfVXB6-wU_JOs-DzFJq_qmR9CmabD7g",
	}
	got := ImageKey(variants)
	if got != "https://90.img.avito.st/image/1/1.ureJN7aAFl" {
		t.Errorf("ImageKey = %q", got)
	}
	// Forty-four characters, measured. If this moves, the URL shape moved with
	// it and the ten discriminating characters are worth re-counting.
	if len(got) != 44 {
		t.Errorf("len = %d, the measurement found 44", len(got))
	}
}

// TestTheKeyDoesNotDependOnTheOrderTheVariantsArriveIn.
//
// They come out of a JSON object, and Go's map range order is randomised per
// run. A key that differed between two runs of the same build would be a link
// that appeared and vanished for no reason anybody could reproduce.
func TestTheKeyDoesNotDependOnTheOrderTheVariantsArriveIn(t *testing.T) {
	a := "https://90.img.avito.st/image/1/1.ureJN7aAFl6fl7Rf8XWXrJuXFFo7gBJ"
	b := "https://90.img.avito.st/image/1/1.ureJN7aAFl7nl8xf8XWXrJuXFFo7gBJ"
	c := "https://90.img.avito.st/image/1/1.ureJN7aAFl7DkOhY8XWXrJuXFFo7gBJ"
	first := ImageKey([]string{a, b, c})
	for _, order := range [][]string{{c, b, a}, {b, a, c}, {c, a, b}} {
		if got := ImageKey(order); got != first {
			t.Errorf("%v gave %q, want %q", order, got, first)
		}
	}
}

// TestOneVariantIsNotAKey.
//
// A single URL is its own longest common prefix, which would make the key the
// whole token — and the whole token is the part that differs between variants.
// Two listings whose photo was read at one size would then never match, and one
// read at two sizes would match nothing read at one. An absent signal must
// contribute nothing rather than contribute a wrong thing.
func TestOneVariantIsNotAKey(t *testing.T) {
	if got := ImageKey([]string{"https://90.img.avito.st/image/1/1.abcdefghijklmnop.def"}); got != "" {
		t.Errorf("ImageKey of one variant = %q, want empty", got)
	}
	if got := ImageKey(nil); got != "" {
		t.Errorf("ImageKey of nothing = %q", got)
	}
}

// TestAPrefixTooShortToDiscriminateIsRefused.
//
// Two photos on different hosts share only «https://», and a key that short
// would match every listing on the site against every other — which the
// linker's «exactly one candidate» gate would then reject, having spent the
// work of looking.
func TestAPrefixTooShortToDiscriminateIsRefused(t *testing.T) {
	got := ImageKey([]string{
		"https://90.img.avito.st/image/1/1.aaa",
		"https://11.img.avito.st/image/2/9.zzz",
	})
	if got != "" {
		t.Errorf("ImageKey = %q, want empty for a prefix that discriminates nothing", got)
	}
}

// TestTheDesktopFixtureYieldsAnImageKeyAndTheMobileOneNever.
//
// The surface difference is the whole reason increment 7 is shaped the way it
// is, and it belongs in a test rather than only in a document.
func TestTheDesktopFixtureYieldsAnImageKeyAndTheMobileOneNever(t *testing.T) {
	desktop := parseFixture(t, "desktop-canonical-p1")
	seen := map[string]int{}
	for _, l := range desktop.Listings {
		if l.ImageKey != "" {
			seen[l.ImageKey]++
		}
	}
	withKey := 0
	for key, n := range seen {
		withKey += n
		if n > 1 {
			t.Errorf("%d listings share the image key %q", n, key)
		}
	}
	if withKey == 0 {
		t.Fatal("no listing on the desktop page produced an image key")
	}
	t.Logf("desktop: %d of %d listings carry an image key",
		withKey, len(desktop.Listings))

	mobile := parseFixture(t, "mobile-search-30")
	for _, l := range mobile.Listings {
		if l.ImageKey != "" {
			t.Errorf("the mobile surface produced an image key: %q", l.ImageKey)
		}
	}
}
