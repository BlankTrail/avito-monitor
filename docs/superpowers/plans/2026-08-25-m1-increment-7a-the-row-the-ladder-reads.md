# Increment 7A: the row the repost ladder reads — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a collected listing carry the fields the repost ladder reads — seller, category, location, coordinates, folded-title key, description digest, image key — on every surface, so that increment 7B has gates with inputs.

**Architecture:** Three layers, each independently testable. `avito/` gains the extraction it never had (the image key, the private-seller spelling) and the pure derived values (folding, `RepostKey`, `DescriptionSHA`), because all of them are functions of one `avito.Listing` and nothing about storage. `internal/store` writes the twenty-one columns `upsertListing` currently omits, plus `sellers` and `seller_snapshots`, with `repost_key` frozen the moment the seller is known. A new enrichment path fills the seller for listings collected from the mobile surface, using only budget headroom so it can never crowd out a scheduled pass.

**Tech Stack:** Go 1.26, no CGO. `modernc.org/sqlite`. `crypto/sha256`, `golang.org/x/text/unicode/norm` is **not** taken — folding uses `strings`, `unicode` and `golang.org/x/text` is avoided; see Task 3.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md`, §Identity and lifecycle («Repost linking is advisory, gated, and biased towards missing») and increment 7 in §M1, cut into increments.

**Measurement this plan argues from:** `docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md`. Every number quoted below comes from it; do not quote a number it does not contain.

## Global Constraints

- **AGPL-3.0-or-later.** Every new file begins `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- **All user-facing copy is Russian.** Identifiers, comments and commit messages are English.
- **No fragile regexes against Avito.** Folding is `unicode`-driven through a `strings.Builder`; the spec says so and Cyrillic case folding plus `regexp` is the specific trap.
- **`Accept-Language: ru` on every request.** Already in the transport; the enrichment fetch must go through `transport.Fetcher` and not build its own client.
- **The BlankTrail API key, the Telegram bot token and the MTProto session are never written to a file this program creates, never logged, never included in an error.**
- **No number may be quoted that a measurement did not produce**, and never without its sample size.
- **`gofmt -l ./cmd ./internal ./avito` prints nothing; `go vet ./...` is clean; `go test ./...` passes.** Every task ends there.

---

## File Structure

**Created:**

| file | responsibility |
|---|---|
| `avito/imagekey.go` | the six size variants of one photo → one stable key |
| `avito/imagekey_test.go` | the measured facts: six variants, 44-character prefix, all distinct |
| `avito/fold.go` | `FoldTitle`, `RepostKey`, `DescriptionSHA` — pure, no storage |
| `avito/fold_test.go` | Cyrillic folding, and the collision counts pinned against the fixtures |
| `internal/store/seller.go` | `upsertSeller`, `maybeSellerSnapshot`, `SellerFingerprint` |
| `internal/store/seller_test.go` | key quality, the fork, the snapshot rule |
| `internal/store/enrich.go` | `ListingsNeedingSeller`, `ApplyEnrichment` |
| `internal/store/enrich_test.go` | the once-only key rewrite |
| `cmd/avitomon/enrich.go` | `avitomon enrich` — the headroom-only loop |

**Modified:**

| file | change |
|---|---|
| `avito/model.go` | `Listing` gains `ImageKey`, `SellerKeyQuality`, `SellerProfilePath`, `SellerRatingHundredths`, `SellerReviewSummary` |
| `avito/state_desktop.go` | read `images`, read `iva.UserInfoStep[].payload.profile` |
| `internal/store/pass.go` | `upsertListing` writes the full row and the seller |
| `internal/web/watches.go` | desktop becomes the offered default |
| `internal/web/health.go` | the seller-coverage line and the enrichment queue |

---

## Task 1: The image key

**Files:**
- Create: `avito/imagekey.go`, `avito/imagekey_test.go`
- Modify: `avito/model.go` (add `ImageKey string`), `avito/state_desktop.go` (read `images`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func ImageKey(variants []string) string` and `Listing.ImageKey string`.

The measured facts, and the plan argues only from these: a photo arrives as **exactly six** size variants; their longest common prefix is **44 characters**, of which **ten** discriminate; **all 20 of 20** keys on the desktop fixture are distinct; the variants diverge **mid-token**, not in a trailing one.

- [ ] **Step 1: Write the failing test**

```go
// TestSixVariantsOfOnePhotoCollapseToOneKey.
//
// The variants of one photo differ in the middle of the token, not at its end
// — measured, against a spec sentence that said the opposite. So the key is the
// longest common prefix and nothing cleverer: any attempt to name the parts
// would be a guess about a URL grammar nobody has documented.
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
}

// TestOneVariantIsNotAKey.
//
// A single URL is its own longest common prefix, which would make the key the
// whole token — and the whole token is what differs between variants. Two
// listings whose photo happened to be read at one size would then never match,
// and one read at two sizes would match nothing read at one. An absent signal
// must contribute nothing rather than contribute a wrong thing.
func TestOneVariantIsNotAKey(t *testing.T) {
	if got := ImageKey([]string{"https://90.img.avito.st/image/1/1.abc.def"}); got != "" {
		t.Errorf("ImageKey of one variant = %q, want empty", got)
	}
	if got := ImageKey(nil); got != "" {
		t.Errorf("ImageKey of nothing = %q", got)
	}
}

// TestAPrefixTooShortToDiscriminateIsRefused.
//
// Two photos from different hosts share only "https://", and a key that short
// would match every listing on the site against every other. The floor is the
// measured length of a real key minus a wide margin.
func TestAPrefixTooShortToDiscriminateIsRefused(t *testing.T) {
	got := ImageKey([]string{
		"https://90.img.avito.st/image/1/1.aaa",
		"https://11.img.avito.st/image/2/9.zzz",
	})
	if got != "" {
		t.Errorf("ImageKey = %q, want empty for a prefix that discriminates nothing", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'ImageKey|Variants|PrefixTooShort' -v
```

Expected: `undefined: ImageKey`.

- [ ] **Step 3: Write `avito/imagekey.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "strings"

// minImageKeyLen is the floor under a usable key.
//
// A real key measured 44 characters, of which the leading
// "https://NN.img.avito.st/image/N/N." is fixed and about ten discriminate.
// Two URLs from different hosts share only "https://", which is 8 — so any
// prefix shorter than the fixed part cannot be a photo id and is refused. 34 is
// that fixed part at its shortest.
const minImageKeyLen = 34

// ImageKey is one photo's identity, taken from its size variants.
//
// Measured rather than parsed: a photo arrives as exactly six variants sharing
// a prefix and diverging in the middle of a token, so the longest common prefix
// is what they have in common and no rule about Avito's URL grammar is needed.
// The spec called that prefix «the object id»; it is the first ten
// discriminating characters of a longer token, which is enough to tell photos
// apart and is not the same claim. Whether it survives a re-upload is
// unverified, which is why the fraction of confirmed links carrying it is a
// shipped metric rather than an assumption.
//
// Fewer than two variants yields nothing. A single URL is its own prefix, and
// that prefix is the part that differs between variants — so it would match
// nothing and be believed anyway. An absent signal must contribute nothing.
func ImageKey(variants []string) string {
	if len(variants) < 2 {
		return ""
	}
	prefix := variants[0]
	for _, v := range variants[1:] {
		prefix = commonPrefix(prefix, v)
		if len(prefix) < minImageKeyLen {
			return ""
		}
	}
	if len(prefix) < minImageKeyLen {
		return ""
	}
	return prefix
}

// commonPrefix is bytes and not runes, deliberately: these are URLs, which are
// ASCII, and a rune-wise walk would buy nothing but a slower loop.
func commonPrefix(a, b string) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

Note: Go 1.26 has a builtin `min`; delete the local one and use it. It is written out here only so the file compiles if read alone.

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'ImageKey|Variants|PrefixTooShort' -v
```

Expected: three PASS.

- [ ] **Step 5: Wire it into the desktop extractor**

In `avito/state_desktop.go`, the `stateItem` struct gains:

```go
	Images []map[string]string `json:"images"`
```

and `decodeStateListing` gains, beside the `UserLogo` block:

```go
	// The first photo only. A listing's identity is not the set of its photos —
	// a seller who re-uploads may reorder or drop some — and the first is the
	// one both incarnations are most likely to share. Taking all of them would
	// mean deciding what a partial overlap means, which is a judgement this
	// signal is too weak to carry.
	if len(it.Images) > 0 {
		variants := make([]string, 0, len(it.Images[0]))
		for _, url := range it.Images[0] {
			variants = append(variants, url)
		}
		// Sorted, because a map's range order is random and commonPrefix is
		// order-independent only in its result, not in its intermediate
		// lengths — and a key that differed between two runs of the same
		// build would be a link that appeared and vanished.
		sort.Strings(variants)
		l.ImageKey = ImageKey(variants)
	}
```

Add `"sort"` to the imports and `ImageKey string` to `Listing` in `avito/model.go`, beside `ImagesCount`, with this comment:

```go
	// ImageKey is the identity of the listing's first photo, shared by its size
	// variants. Empty on the mobile surface, which carries no images at all —
	// see docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md.
	ImageKey string
```

- [ ] **Step 6: Pin it against the real fixtures**

```go
// TestTheDesktopFixtureYieldsAnImageKeyForMostListingsAndTheMobileOneForNone.
//
// The surface difference is the whole reason increment 7 is shaped the way it
// is, and it belongs in a test rather than only in a document.
func TestTheDesktopFixtureYieldsAnImageKeyForMostListingsAndTheMobileOneForNone(t *testing.T) {
	desktop := parseFixture(t, "desktop-canonical-p1")
	withKey := 0
	seen := map[string]int{}
	for _, l := range desktop.Listings {
		if l.ImageKey != "" {
			withKey++
			seen[l.ImageKey]++
		}
	}
	if withKey == 0 {
		t.Fatal("no listing on the desktop page produced an image key")
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("%d listings share the image key %q", n, key)
		}
	}
	t.Logf("desktop: %d of %d listings carry an image key", withKey, len(desktop.Listings))

	mobile := parseFixture(t, "mobile-search-30")
	for _, l := range mobile.Listings {
		if l.ImageKey != "" {
			t.Errorf("the mobile surface produced an image key: %q", l.ImageKey)
		}
	}
}
```

`parseFixture` already exists in `avito`'s test files — find it with `grep -n "func parseFixture" avito/*_test.go` and use it as written rather than adding a second one. If it is named differently, use the existing helper; do not add a new fixture loader.

- [ ] **Step 7: Regenerate the golden files**

The `want.json` fixtures pin the whole `Page`, so a new field breaks them. Find the regeneration switch:

```bash
cd S:/Projects/Avito_Go && grep -rn "update\|golden\|WriteFile" avito/*_test.go | head
```

Regenerate with it, then **read the diff** before committing: the only change may be `ImageKey` appearing. Any other field moving means something else broke.

```bash
cd S:/Projects/Avito_Go && git diff --stat avito/testdata/
```

- [ ] **Step 8: Full check and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./avito ./cmd ./internal && go vet ./... && go test ./... 2>&1 | grep -v "no test files"
```

```bash
git add avito/ && git commit -m "feat(avito): the image key, from the variants a photo actually has"
```

---

## Task 2: The seller, both spellings

**Files:**
- Modify: `avito/model.go`, `avito/state_desktop.go`
- Test: `avito/state_desktop_test.go` (append)

**Interfaces:**
- Consumes: nothing.
- Produces: `Listing.SellerKey`, `Listing.SellerKeyQuality string` (`"slug"`, `"profile"` or `""`), `Listing.SellerProfilePath string`, `Listing.SellerRatingHundredths *int64`, `Listing.SellerReviewSummary string`.

Today only `userLogo.link` is read, which yields `brand:<slug>` for 46 of 50 desktop rows. The spec names a second spelling — `iva.UserInfoStep[].payload.profile` — and it is in the payload (20 of 50 rows) carrying the profile link, the display name and the rating. Reading it costs one struct and gives `sellers` its `display_name`, `key_quality` and `seller_snapshots` its rating.

The measured shape of that payload:

```json
{"payload":{"profile":{"link":"/brands/bioniqo?src=search_seller_info&iid=7775028313",
                       "title":"Re:Market"},
            "rating":{"score":4.9,"summary":"1 898 отзывов"}}}
```

- [ ] **Step 1: Write the failing test**

```go
// TestTheSellerKeyPrefersTheSlugAndFallsBackToTheProfile.
//
// The spec names two spellings and only one was ever read. They are not one
// identity: a private seller who changes their profile path forks into two
// rows, which the surrogate id in `sellers` exists to repair later.
func TestTheSellerKeyPrefersTheSlugAndFallsBackToTheProfile(t *testing.T) {
	page := parseFixture(t, "desktop-canonical-p1")

	keyed, slugQuality, profileQuality, named, rated := 0, 0, 0, 0, 0
	for _, l := range page.Listings {
		if l.SellerKey != "" {
			keyed++
		}
		switch l.SellerKeyQuality {
		case "slug":
			slugQuality++
		case "profile":
			profileQuality++
		}
		if l.SellerName != "" {
			named++
		}
		if l.SellerRatingHundredths != nil {
			rated++
		}
	}
	t.Logf("of %d: keyed=%d slug=%d profile=%d named=%d rated=%d",
		len(page.Listings), keyed, slugQuality, profileQuality, named, rated)

	if keyed == 0 {
		t.Fatal("no seller key at all")
	}
	// The measurement says 46 of 50 carry a key; a build that reads fewer has
	// lost something, and one that reads more has started inventing.
	if keyed < 46 {
		t.Errorf("%d listings carry a seller key, the measurement found 46", keyed)
	}
	if named == 0 {
		t.Error("no display name was read; UserInfoStep carries one")
	}
	if rated == 0 {
		t.Error("no rating was read; UserInfoStep carries one")
	}
	// A key is never recorded without saying how it was obtained: the schema
	// refuses a heuristic key as repost evidence, and it can only do that if
	// the quality travelled with it.
	for _, l := range page.Listings {
		if l.SellerKey != "" && l.SellerKeyQuality == "" {
			t.Errorf("listing %d has a key with no quality", l.ItemID)
			break
		}
	}
}

// TestAPrivateSellerProfileBecomesAUserKeyAndNotABrandKey.
//
// Merging two people under one key is worse than declining to identify either.
func TestAPrivateSellerProfileBecomesAUserKeyAndNotABrandKey(t *testing.T) {
	for _, c := range []struct {
		link       string
		wantKey    string
		wantQuality string
	}{
		{"/brands/bioniqo?src=search_seller_info&iid=1", "brand:bioniqo", "slug"},
		{"/user/8f3c1d2e9a/profile/?src=search", "user:8f3c1d2e9a", "profile"},
		{"/company/whatever", "", ""},
		{"", "", ""},
	} {
		key, quality := sellerKeyFromLink(c.link)
		if key != c.wantKey || quality != c.wantQuality {
			t.Errorf("%q → (%q, %q), want (%q, %q)",
				c.link, key, quality, c.wantKey, c.wantQuality)
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
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'Seller' -v
```

Expected: `undefined: sellerKeyFromLink` and unknown fields.

- [ ] **Step 3: Add the fields to `avito/model.go`**

Beside `SellerKey`, `SellerName`, `IsCompany`:

```go
	// SellerKeyQuality records how the key was obtained: "slug" from the
	// brand link, "profile" from the private-seller profile path, "" when
	// there is no key. It is not decoration — the schema refuses a heuristic
	// key as repost evidence, and it can only do that if the quality
	// travelled with the key.
	SellerKeyQuality string
	// SellerProfilePath is the path the key came from, kept verbatim so a
	// person can go and look.
	SellerProfilePath string

	// The seller's reputation as this row stated it. Hundredths of a point,
	// matching track.UnitRatingHundredths, so "fell by more than 0.2" is an
	// integer comparison — the same arithmetic as a price in kopecks.
	SellerRatingHundredths *int64
	SellerReviewSummary    string
```

- [ ] **Step 4: Read the second spelling**

Extend the `IVA` struct in `avito/state_desktop.go`:

```go
		UserInfoStep []struct {
			Payload struct {
				Profile struct {
					Link  string `json:"link"`
					Title string `json:"title"`
				} `json:"profile"`
				Rating struct {
					Score   float64 `json:"score"`
					Summary string  `json:"summary"`
				} `json:"rating"`
			} `json:"payload"`
		} `json:"UserInfoStep"`
```

and replace the `UserLogo` block with:

```go
	// Two spellings, and the order is a decision. The brand link is on about
	// nine rows in ten; the profile is on four in ten and carries the name and
	// the rating besides. Where both are present they have been measured to
	// agree, so the brand link wins on being the more available one and the
	// profile is read for everything else it carries.
	if it.UserLogo != nil {
		if key, quality := sellerKeyFromLink(it.UserLogo.Link); key != "" {
			l.SellerKey, l.SellerKeyQuality = key, quality
			l.SellerProfilePath = it.UserLogo.Link
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
		if len(it.IVA.UserInfoStep) > 0 {
			p := it.IVA.UserInfoStep[0].Payload
			if l.SellerKey == "" {
				if key, quality := sellerKeyFromLink(p.Profile.Link); key != "" {
					l.SellerKey, l.SellerKeyQuality = key, quality
					l.SellerProfilePath = p.Profile.Link
				}
			}
			if p.Profile.Title != "" {
				l.SellerName = p.Profile.Title
			}
			if p.Rating.Score > 0 {
				// Rounded rather than truncated: 4.9 in float64 is 4.899999…,
				// and truncating would file every seller a hundredth low
				// for ever.
				h := int64(math.Round(p.Rating.Score * 100))
				l.SellerRatingHundredths = &h
			}
			l.SellerReviewSummary = p.Rating.Summary
		}
	}
```

Add `"math"` to the imports.

- [ ] **Step 5: Write `sellerKeyFromLink`**

Put it in `avito/state_desktop.go` beside `brandSlug`:

```go
// sellerKeyFromLink turns a profile path into a key and says how it got it.
//
// Two shapes and no others. "/brands/<slug>" is a company's own page;
// "/user/<hash>/profile" is a private seller's. Anything else yields nothing at
// all rather than a guess: inventing an identity that silently merges two
// people is worse than declining to identify either, and the schema's
// key_quality has no value for "we made this up".
func sellerKeyFromLink(link string) (key, quality string) {
	if link == "" {
		return "", ""
	}
	// The query carries a per-listing iid, so it is cut before anything else:
	// keeping it would give the same seller a different key on every listing.
	if i := strings.IndexByte(link, '?'); i >= 0 {
		link = link[:i]
	}
	if slug := brandSlug(link); slug != "" {
		return "brand:" + slug, "slug"
	}
	const userPrefix = "/user/"
	if strings.HasPrefix(link, userPrefix) {
		rest := strings.TrimPrefix(link, userPrefix)
		if i := strings.IndexByte(rest, '/'); i > 0 {
			rest = rest[:i]
		}
		if rest != "" {
			return "user:" + rest, "profile"
		}
	}
	return "", ""
}
```

Read `brandSlug` first (`grep -n "func brandSlug" -A 20 avito/state_desktop.go`) and confirm it already strips the query; if it does, the `IndexByte` above is still correct for the `/user/` branch and stays.

- [ ] **Step 6: Run the tests, regenerate the goldens, read the diff**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'Seller' -v
```

Then regenerate `want.json` with the same switch Task 1 used and check the diff shows only the new seller fields.

- [ ] **Step 7: Full check and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./avito ./cmd ./internal && go vet ./... && go test ./... 2>&1 | grep -v "no test files"
```

```bash
git add avito/ && git commit -m "feat(avito): the seller's second spelling, its name and its rating"
```

---

## Task 3: Folding, and the two digests

**Files:**
- Create: `avito/fold.go`, `avito/fold_test.go`

**Interfaces:**
- Consumes: `Listing` with the fields from Tasks 1 and 2.
- Produces:
  - `func FoldTitle(s string) string`
  - `func RepostKey(l Listing) string` — `sha256` hex, `""` when the listing carries nothing to key on
  - `func DescriptionSHA(s string) string` — `sha256` hex of the folded description, `""` for empty

The measured collision facts this task must reproduce: on `desktop-canonical-p1`, **7 distinct folded titles across 50 listings** and **46 distinct full keys**; on `mobile-search-30`, **9 distinct folded titles across 30** and **9 distinct full keys**, because the other three terms are empty.

- [ ] **Step 1: Write the failing test**

```go
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
	} {
		if got := FoldTitle(c.in); got != c.want {
			t.Errorf("FoldTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFoldingIsStableUnderAnEditThatChangesNothingAPersonReads.
//
// The key is computed once at insert precisely so a later edit cannot move it;
// this is the other half — an edit that is only punctuation must not have
// produced a different key in the first place.
func TestFoldingIsStableUnderAnEditThatChangesNothingAPersonReads(t *testing.T) {
	a := FoldTitle("iPhone 15 Pro, 256 ГБ (как новый)")
	b := FoldTitle("iPhone 15 Pro 256 ГБ — как новый")
	if a != b {
		t.Errorf("%q != %q", a, b)
	}
}

// TestTheKeyIsEmptyWhenThereIsNothingToKeyOn.
//
// A key over four empty fields is one value shared by every listing that ever
// lacked them — which would make the candidate seek return the whole table.
// Empty is the honest answer and the linker must skip it.
func TestTheKeyIsEmptyWhenThereIsNothingToKeyOn(t *testing.T) {
	if got := RepostKey(Listing{}); got != "" {
		t.Errorf("RepostKey of nothing = %q", got)
	}
	// A title alone is not enough either: measured, seven distinct titles
	// covered fifty listings.
	if got := RepostKey(Listing{Title: "iPhone 15, 128 ГБ"}); got != "" {
		t.Errorf("RepostKey of a bare title = %q, want empty", got)
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
// phone in the same place never collide.
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

// TestTheMeasuredCollisionCountsHold.
//
// These four numbers are the reason increment 7 is split the way it is, and a
// change to the folding that moved them would change what the linker can do.
// From docs/measurements/2026-08-25-what-the-repost-ladder-can-see.md.
func TestTheMeasuredCollisionCountsHold(t *testing.T) {
	for _, c := range []struct {
		fixture               string
		listings              int
		distinctTitles        int
		distinctKeys          int
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
			t.Errorf("%s: %d distinct repost keys, measured %d",
				c.fixture, len(keys), c.distinctKeys)
		}
	}
}
```

Note on the mobile row: the measurement counted 9 distinct keys using a folded title alone. `RepostKey` returns `""` when the seller, category and location are all empty, so the count of **non-empty** keys on mobile is **0**. That is the same fact stated more usefully, and the test asserts the number this implementation produces.

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'Fold|RepostKey|Collision' -v
```

Expected: `undefined: FoldTitle`.

- [ ] **Step 3: Write `avito/fold.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
)

// This file is the derived half of a listing's identity: the folded title and
// the two digests built on it.
//
// It is in avito/ and not in internal/store because every value here is a pure
// function of one Listing and knows nothing about a database. The schema's rule
// that repost_key is written once and never updated is storage's, and it lives
// in internal/store/pass.go where the ON CONFLICT clause is.

// FoldTitle is the title with everything a person would not notice removed.
//
// unicode-driven and through a strings.Builder, which the spec asks for and
// which is also simply correct: Cyrillic case folding and regexp do not mix
// well, and every title on this site is Cyrillic.
//
// Case-folded, not lower-cased. strings.ToLower is a mapping and unicode's
// SimpleFold is the equivalence — for Russian the two agree, and using the
// right one costs nothing and stops being wrong the first time a title carries
// a German ß or a Greek final sigma, both of which appear in phone listings.
func FoldTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(unicode.ToLower(r))
		default:
			// Punctuation and whitespace alike collapse to one separator. «15,
			// 128 ГБ» and «15 128 ГБ» are the same title to a reader, and a key
			// that disagreed would break every link the day a seller added a
			// comma.
			space = true
		}
	}
	return b.String()
}

// RepostKey is the candidate bucket: seller, micro-category, location and the
// folded title, hashed.
//
// Empty when the seller, the category and the location are all missing, which
// is every listing read from the mobile surface. A key over the title alone
// would put fifty listings in seven buckets — measured — and the linker's
// «exactly one surviving candidate» gate would then reject every pair while
// having done the work of looking. Empty says «this row cannot be keyed» once,
// and the seek is skipped.
//
// The seller is what does the discriminating: with it the same fifty listings
// fall into forty-six buckets.
func RepostKey(l Listing) string {
	if l.SellerKey == "" && l.MicroCategoryID == 0 && l.LocationID == 0 {
		return ""
	}
	title := FoldTitle(l.Title)
	if title == "" {
		return ""
	}
	// A separator no term can contain, so that ("ab","c") and ("a","bc") are
	// different keys.
	h := sha256.New()
	for _, part := range []string{
		l.SellerKey,
		strconv.FormatInt(l.MicroCategoryID, 10),
		strconv.FormatInt(l.LocationID, 10),
		title,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DescriptionSHA is a strong corroborator, and the folding is what makes it
// one: two incarnations of the same advertisement are pasted from the same
// text, and a re-typed line break must not break the match.
//
// Empty for an empty description, and the linker contributes nothing from an
// absent signal — a rule that matters here more than anywhere, because only
// twenty rows in fifty carry a description at all.
func DescriptionSHA(s string) string {
	folded := FoldTitle(s)
	if folded == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(folded))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run the tests**

```bash
cd S:/Projects/Avito_Go && go test ./avito/ -run 'Fold|RepostKey|Collision' -v
```

Expected: five PASS. If `TestTheMeasuredCollisionCountsHold` reports different numbers, **do not adjust the expected numbers** — the folding differs from the measurement's Python and the difference must be understood first. The Python used `NFKC` normalisation, which Go's `unicode.ToLower` does not do; if a count is off by one or two, that is the likely cause and the fix is to say so in the test comment with the real number, not to silently take the new one.

- [ ] **Step 5: Commit**

```bash
git add avito/fold.go avito/fold_test.go && git commit -m "feat(avito): folding, the repost key and the description digest"
```

---

## Task 4: The pass writes the whole row

**Files:**
- Modify: `internal/store/pass.go:233-262` (`upsertListing`)
- Create: `internal/store/seller.go`, `internal/store/seller_test.go`
- Test: `internal/store/pass_test.go` (append)

**Interfaces:**
- Consumes: `avito.RepostKey`, `avito.DescriptionSHA`, `Listing.ImageKey`, `Listing.SellerKey`, `Listing.SellerKeyQuality`.
- Produces:
  - `func upsertSeller(ctx context.Context, tx *sql.Tx, l avito.Listing, at int64) (int64, error)` — returns the surrogate id, or 0 when the listing identifies no seller
  - `func SellerFingerprint(l avito.Listing) string`
  - `func maybeSellerSnapshot(ctx context.Context, tx *sql.Tx, sellerID int64, l avito.Listing, at int64) error`

`upsertListing` writes nine columns of thirty. This task writes the rest.

- [ ] **Step 1: Write the failing test**

```go
// TestAPassFillsTheColumnsTheLadderReads.
//
// upsertListing wrote nine columns of thirty, and the export's «Продавец» was
// empty on all sixty rows of a live run because of it. Every column below is
// one the repost ladder's gates read.
func TestAPassFillsTheColumnsTheLadderReads(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aListing()
	l.ItemID = 500
	l.Title = "iPhone 15, 128 ГБ"
	l.SellerKey, l.SellerKeyQuality = "brand:bioniqo", "slug"
	l.SellerName = "Re:Market"
	l.MicroCategoryID, l.CategoryRootID, l.LocationID = 10, 9, 637640
	l.GeoAddress, l.LocationName = "Москва, ул. Ленина, 28", "Москва"
	lat, lng := 55.75, 37.61
	l.Lat, l.Lng, l.CoordsPrecise = &lat, &lng, true
	l.Description = "Продаю телефон, состояние отличное."
	l.ImageKey = "https://90.img.avito.st/image/1/1.ureJN7aAFl"

	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceDesktop, Class: "ok", Path: "state", Mode: "direct",
	}, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}

	var (
		sellerID                            sql.NullInt64
		micro, root, location               int64
		lat2, lng2                          sql.NullFloat64
		precise                             bool
		geo, locName                        string
		repostKey, descSHA, imageKey        string
	)
	err := s.DB().QueryRow(`
		SELECT seller_id, micro_category_id, category_root_id, location_id,
		       lat, lng, coords_precise, geo_address, location_name,
		       repost_key, description_sha, image_key
		FROM listings WHERE item_id = 500`).Scan(
		&sellerID, &micro, &root, &location, &lat2, &lng2, &precise,
		&geo, &locName, &repostKey, &descSHA, &imageKey)
	if err != nil {
		t.Fatal(err)
	}
	if !sellerID.Valid || sellerID.Int64 == 0 {
		t.Error("seller_id is NULL; the whole ladder's hard gate has no input")
	}
	if micro != 10 || root != 9 || location != 637640 {
		t.Errorf("category/location = %d/%d/%d", micro, root, location)
	}
	if !lat2.Valid || !lng2.Valid || !precise {
		t.Errorf("coords = %v/%v precise=%v", lat2, lng2, precise)
	}
	if geo == "" || locName == "" {
		t.Errorf("address = %q / %q", geo, locName)
	}
	if repostKey != avito.RepostKey(l) {
		t.Errorf("repost_key = %q, want %q", repostKey, avito.RepostKey(l))
	}
	if descSHA != avito.DescriptionSHA(l.Description) {
		t.Errorf("description_sha = %q", descSHA)
	}
	if imageKey != l.ImageKey {
		t.Errorf("image_key = %q", imageKey)
	}
}

// TestTheSellerIsWrittenOnceWithItsKeyQuality.
//
// A heuristic key is never accepted as repost evidence, and the schema can only
// enforce that if the quality was written beside the key.
func TestTheSellerIsWrittenOnceWithItsKeyQuality(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	one, two := aListing(), aListing()
	one.ItemID, two.ItemID = 500, 501
	for _, l := range []*avito.Listing{&one, &two} {
		l.SellerKey, l.SellerKeyQuality, l.SellerName = "brand:bioniqo", "slug", "Re:Market"
		l.MicroCategoryID, l.LocationID = 10, 637640
	}
	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceDesktop, Class: "ok", Path: "state", Mode: "direct",
	}, []avito.Listing{one, two}); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM sellers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d sellers for two listings by one seller", n)
	}
	var quality, name string
	if err := s.DB().QueryRow(
		`SELECT key_quality, display_name FROM sellers`).Scan(&quality, &name); err != nil {
		t.Fatal(err)
	}
	if quality != "slug" || name != "Re:Market" {
		t.Errorf("seller = %q / %q", quality, name)
	}
	// And both listings point at it.
	var pointing int
	if err := s.DB().QueryRow(
		`SELECT count(*) FROM listings WHERE seller_id IS NOT NULL`).Scan(&pointing); err != nil {
		t.Fatal(err)
	}
	if pointing != 2 {
		t.Errorf("%d listings point at a seller", pointing)
	}
}

// TestAListingWithNoSellerStillSaves, which is every listing from the mobile
// surface — thirty of thirty.
func TestAListingWithNoSellerStillSaves(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aListing()
	l.ItemID = 500
	l.SellerKey = ""
	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "cards", Mode: "direct",
	}, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var sellerID sql.NullInt64
	var repostKey string
	if err := s.DB().QueryRow(
		`SELECT seller_id, repost_key FROM listings WHERE item_id = 500`).Scan(
		&sellerID, &repostKey); err != nil {
		t.Fatal(err)
	}
	if sellerID.Valid {
		t.Error("a seller was invented for a listing that identified none")
	}
	if repostKey != "" {
		t.Errorf("repost_key = %q; a key with no seller buckets everything together", repostKey)
	}
}

// TestATitleEditedLaterDoesNotMoveTheKeyItWasLinkedOn.
//
// The whole reason repost_key is written once. A link made against one key must
// not be silently re-pointed at another.
func TestATitleEditedLaterDoesNotMoveTheKeyItWasLinkedOn(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	l := aListing()
	l.ItemID = 500
	l.Title = "iPhone 15, 128 ГБ"
	l.SellerKey, l.SellerKeyQuality = "brand:bioniqo", "slug"
	l.MicroCategoryID, l.LocationID = 10, 637640
	p := Pass{WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceDesktop, Class: "ok", Path: "state", Mode: "direct"}
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := s.DB().QueryRow(
		`SELECT repost_key FROM listings WHERE item_id = 500`).Scan(&first); err != nil {
		t.Fatal(err)
	}

	l.Title = "iPhone 15 Pro Max, 1 ТБ, срочно"
	p.StartedAt, p.FinishedAt = 2000, 2001
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	var second, title string
	if err := s.DB().QueryRow(
		`SELECT repost_key, title FROM listings WHERE item_id = 500`).Scan(
		&second, &title); err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("the key moved with the title: %q → %q", first, second)
	}
	if title != l.Title {
		t.Errorf("the title did not update: %q", title)
	}
}

// TestTheSellerSnapshotFollowsTheSameFingerprintRuleAsAListing.
//
// Write when something moved. A row per pass would be a reputation table
// growing at the rate of collection and saying nothing new.
func TestTheSellerSnapshotFollowsTheSameFingerprintRuleAsAListing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	rating := int64(490)
	l := aListing()
	l.ItemID = 500
	l.SellerKey, l.SellerKeyQuality = "brand:bioniqo", "slug"
	l.MicroCategoryID, l.LocationID = 10, 637640
	l.SellerRatingHundredths = &rating
	l.SellerReviewSummary = "1 898 отзывов"

	p := Pass{WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceDesktop, Class: "ok", Path: "state", Mode: "direct"}
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	p.StartedAt, p.FinishedAt = 2000, 2001
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM seller_snapshots`); n != 1 {
		t.Errorf("%d snapshots for an unchanged rating", n)
	}

	moved := int64(470)
	l.SellerRatingHundredths = &moved
	p.StartedAt, p.FinishedAt = 3000, 3001
	if _, err := s.SavePass(ctx, p, []avito.Listing{l}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT count(*) FROM seller_snapshots`); n != 2 {
		t.Errorf("%d snapshots after the rating moved", n)
	}
}
```

`aListing()` and `countRows` already exist in `internal/store/pass_test.go`. Read them before writing (`grep -n "func aListing" -A 20 internal/store/pass_test.go`) and use them as they are.

- [ ] **Step 2: Run and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'LadderReads|KeyQuality|NoSellerStillSaves|TitleEdited|SellerSnapshot' -v
```

Expected: seller_id NULL, repost_key empty, `sellers` empty.

- [ ] **Step 3: Write `internal/store/seller.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/BlankTrail/avito-monitor/avito"
)

// This file gives sellers its first writer.
//
// The table has existed since migration 0001 and nothing ever wrote a row, so
// listings.seller_id was NULL everywhere and the export's «Продавец» column was
// empty on every line of a live run. That was visible from outside for a whole
// increment before anybody looked at the cause.

// upsertSeller writes the seller this listing names and returns its surrogate
// id, or 0 when the listing names none.
//
// Zero and not an error: thirty of thirty listings from the mobile surface
// identify no seller, so an unnamed seller is the ordinary case and not a
// failure. The caller writes NULL into listings.seller_id for it.
//
// key_quality is written on insert and never downgraded on conflict. A seller
// first seen through a brand slug and later seen through a profile keeps
// «slug»: the two spellings are not one identity, and the row that exists is
// the one links were made against.
func upsertSeller(ctx context.Context, tx *sql.Tx, l avito.Listing, at int64) (int64, error) {
	if l.SellerKey == "" {
		return 0, nil
	}
	quality := l.SellerKeyQuality
	if quality == "" {
		// The schema's CHECK admits three values and this is the one that
		// means «we are not sure how we got this». A heuristic key is never
		// accepted as repost evidence, which is exactly the right treatment
		// for a key whose provenance was lost.
		quality = "heuristic"
	}
	kind := "unknown"
	if l.IsCompany != nil && *l.IsCompany {
		kind = "company"
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sellers
		  (seller_key, key_quality, kind, display_name, profile_path,
		   first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(seller_key) DO UPDATE SET
		    kind         = CASE WHEN excluded.kind = 'unknown'
		                        THEN sellers.kind ELSE excluded.kind END,
		    display_name = CASE WHEN excluded.display_name = ''
		                        THEN sellers.display_name ELSE excluded.display_name END,
		    profile_path = CASE WHEN excluded.profile_path = ''
		                        THEN sellers.profile_path ELSE excluded.profile_path END,
		    last_seen_at = excluded.last_seen_at`,
		l.SellerKey, quality, kind, l.SellerName, l.SellerProfilePath,
		at, at); err != nil {
		return 0, fmt.Errorf("store: seller: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM sellers WHERE seller_key = ?`, l.SellerKey).Scan(&id); err != nil {
		return 0, fmt.Errorf("store: seller id: %w", err)
	}
	return id, nil
}

// SellerFingerprint is what has to move before a reputation reading is worth a
// row.
//
// The same rule the listing series uses, and for the same reason: a row per
// pass would grow at the rate of collection and say nothing new. Rating and
// review summary only — the display name is on the sellers row, where it is
// corrected in place rather than versioned.
func SellerFingerprint(l avito.Listing) string {
	h := sha256.New()
	if l.SellerRatingHundredths != nil {
		h.Write([]byte(strconv.FormatInt(*l.SellerRatingHundredths, 10)))
	}
	h.Write([]byte{0})
	h.Write([]byte(l.SellerReviewSummary))
	return hex.EncodeToString(h.Sum(nil))
}

// maybeSellerSnapshot writes a reputation row when the fingerprint moved.
//
// No anchor rule here, unlike the listing series. M1 emits no events from this
// table, so «unchanged since March» and «unread since March» are not yet
// different questions anybody asks — and an anchor would write rows against a
// distinction nothing reads. The column exists; the rule arrives with the first
// reader.
func maybeSellerSnapshot(ctx context.Context, tx *sql.Tx, sellerID int64,
	l avito.Listing, at int64) error {

	if sellerID == 0 || l.SellerRatingHundredths == nil {
		return nil
	}
	fp := SellerFingerprint(l)

	var last string
	err := tx.QueryRowContext(ctx, `
		SELECT fingerprint FROM seller_snapshots
		WHERE seller_id = ? ORDER BY ts DESC LIMIT 1`, sellerID).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("store: seller snapshot: %w", err)
	}
	if err == nil && last == fp {
		return nil
	}

	// ON CONFLICT DO NOTHING against the (seller_id, ts) unique index: two
	// listings by one seller in one pass carry one reading, and the second must
	// not fail the whole transaction.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO seller_snapshots
		  (seller_id, ts, anchor, fingerprint, rating_hundredths, rating_summary)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT (seller_id, ts) DO NOTHING`,
		sellerID, at, fp, *l.SellerRatingHundredths, l.SellerReviewSummary); err != nil {
		return fmt.Errorf("store: seller snapshot: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Rewrite `upsertListing`**

Replace the body of `upsertListing` in `internal/store/pass.go`. Keep the existing doc comment and extend it; the new clause is the `repost_key` rule.

```go
func upsertListing(ctx context.Context, tx *sql.Tx, l avito.Listing, p Pass) error {
	sellerID, err := upsertSeller(ctx, tx, l, p.StartedAt)
	if err != nil {
		return err
	}
	var seller any
	if sellerID != 0 {
		seller = sellerID
	}

	// A price is not here: prices move and live in listing_snapshots. What is
	// here is the stable half plus the three derived values the repost ladder
	// reads.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO listings
		  (item_id, thread_id, url_path, title, title_norm,
		   category_id, category_root_id, micro_category_id,
		   location_id, location_name, geo_address, lat, lng, coords_precise,
		   seller_id, sort_ts_ms, allow_ts_ms,
		   repost_key, description_sha, image_key,
		   first_seen_at, last_seen_at, state, state_at,
		   discovered_seed, first_watch_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, 'active', ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET
		    url_path     = excluded.url_path,
		    title        = excluded.title,
		    title_norm   = excluded.title_norm,
		    last_seen_at = excluded.last_seen_at,
		    -- Everything below fills a hole and never overwrites a fact. A
		    -- later pass that read less than the first must not erase what the
		    -- first read: the mobile surface carries none of these, and a
		    -- listing in two watches is read from both.
		    location_name    = CASE WHEN excluded.location_name = ''
		                            THEN listings.location_name
		                            ELSE excluded.location_name END,
		    geo_address      = CASE WHEN excluded.geo_address = ''
		                            THEN listings.geo_address
		                            ELSE excluded.geo_address END,
		    category_id       = max(listings.category_id, excluded.category_id),
		    category_root_id  = max(listings.category_root_id, excluded.category_root_id),
		    micro_category_id = max(listings.micro_category_id, excluded.micro_category_id),
		    location_id       = max(listings.location_id, excluded.location_id),
		    lat            = coalesce(listings.lat, excluded.lat),
		    lng            = coalesce(listings.lng, excluded.lng),
		    coords_precise = max(listings.coords_precise, excluded.coords_precise),
		    sort_ts_ms     = coalesce(listings.sort_ts_ms, excluded.sort_ts_ms),
		    allow_ts_ms    = coalesce(listings.allow_ts_ms, excluded.allow_ts_ms),
		    seller_id      = coalesce(listings.seller_id, excluded.seller_id),
		    description_sha = CASE WHEN excluded.description_sha = ''
		                           THEN listings.description_sha
		                           ELSE excluded.description_sha END,
		    image_key       = CASE WHEN excluded.image_key = ''
		                           THEN listings.image_key
		                           ELSE excluded.image_key END,
		    -- repost_key is the one exception to «fill a hole», and it is the
		    -- schema's own rule: written once and never updated, so a title
		    -- edited after a link was made cannot move the key that link was
		    -- made against. It fills an empty key and never replaces one.
		    repost_key = CASE WHEN listings.repost_key = ''
		                      THEN excluded.repost_key
		                      ELSE listings.repost_key END`,
		l.ItemID, l.ItemID, l.URLPath, l.Title, avito.FoldTitle(l.Title),
		l.CategoryID, l.CategoryRootID, l.MicroCategoryID,
		l.LocationID, l.LocationName, l.GeoAddress, l.Lat, l.Lng, l.CoordsPrecise,
		seller, l.SortTimeMS, l.AllowTimeMS,
		avito.RepostKey(l), avito.DescriptionSHA(l.Description), l.ImageKey,
		p.StartedAt, p.StartedAt, p.StartedAt, p.IsSeed, p.WatchID)
	if err != nil {
		return fmt.Errorf("store: save pass: listings: %w", err)
	}
	return maybeSellerSnapshot(ctx, tx, sellerID, l, p.StartedAt)
}
```

`title_norm` has been a declared-and-never-written column since migration 0001; this is what finally writes it, and `internal/store/export.go` says in a comment that the Cyrillic search runs in Go because nothing filled it. **Do not change the export in this task** — the search still runs in Go, because `FoldTitle` strips punctuation and a substring search over a stripped title is not the same query. Leave the comment; a later task may revisit it.

- [ ] **Step 5: Run the tests**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -v 2>&1 | grep -E "^(--- |ok|FAIL)"
```

Expected: all PASS. If `SavePass` now fails a foreign key, check that `upsertSeller` runs before the listing insert inside the same transaction — `sellers` is referenced by `listings.seller_id`.

- [ ] **Step 6: Full check and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./avito ./cmd ./internal && go vet ./... && go test ./... 2>&1 | grep -v "no test files"
```

```bash
git add internal/store/ && git commit -m "feat(store): a pass writes the row the ladder reads, and sellers gets a writer"
```

---

## Task 5: Enrichment — the seller a mobile watch cannot see

**Files:**
- Create: `internal/store/enrich.go`, `internal/store/enrich_test.go`, `cmd/avitomon/enrich.go`
- Modify: `cmd/avitomon/main.go` (one case and one usage line)

**Interfaces:**
- Consumes: `upsertSeller` (Task 4), `avito.RepostKey`.
- Produces:
  - `type EnrichCandidate struct { ItemID int64; URLPath string }`
  - `func (s *Store) ListingsNeedingSeller(ctx context.Context, limit int) ([]EnrichCandidate, error)`
  - `func (s *Store) ApplyEnrichment(ctx context.Context, itemID int64, l avito.Listing, at int64) error`
  - `func (s *Store) EnrichmentQueue(ctx context.Context) (waiting, keyed, total int, err error)`

The user's decision: desktop is the default surface **and** the seller is fetched separately where it is missing. This task is the second half. The rule that keeps it honest: **enrichment spends only headroom.** It never competes with a scheduled pass, because a pass is the product working and enrichment is the product improving.

- [ ] **Step 1: Write the failing test**

```go
// TestOnlyListingsWithNoSellerAreOffered, so the queue empties rather than
// re-fetching the same rows for ever.
func TestOnlyListingsWithNoSellerAreOffered(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	withSeller, without := aListing(), aListing()
	withSeller.ItemID, without.ItemID = 500, 501
	withSeller.SellerKey, withSeller.SellerKeyQuality = "brand:x", "slug"
	withSeller.MicroCategoryID, withSeller.LocationID = 10, 1
	without.SellerKey = ""

	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "cards", Mode: "direct",
	}, []avito.Listing{withSeller, without}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ItemID != 501 {
		t.Errorf("offered %+v, want only 501", got)
	}
	if got[0].URLPath == "" {
		t.Error("a candidate with no path cannot be fetched")
	}
}

// TestALisitngWithNoPathIsNotOffered, because there is nothing to fetch.
func TestAListingWithNoPathIsNotOffered(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.DB().Exec(`
		INSERT INTO listings (item_id, thread_id, url_path, title,
		                      first_seen_at, last_seen_at, state_at)
		VALUES (700, 700, '', 'нечего открывать', 1000, 1000, 1000)`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListingsNeedingSeller(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.ItemID == 700 {
			t.Error("a listing with no url_path was offered for fetching")
		}
	}
}

// TestEnrichmentFillsTheSellerAndSealsTheKeyExactlyOnce.
//
// The key is written at insert and frozen — but a listing collected from the
// mobile surface has no seller at insert, so its key is empty. Enrichment is
// the row becoming complete rather than an edit, so the key is written then,
// and the guard is the UPDATE's own WHERE clause: once seller_id is set, no
// second rewrite is possible.
func TestEnrichmentFillsTheSellerAndSealsTheKeyExactlyOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	bare := aListing()
	bare.ItemID, bare.Title, bare.SellerKey = 501, "iPhone 15, 128 ГБ", ""
	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "cards", Mode: "direct",
	}, []avito.Listing{bare}); err != nil {
		t.Fatal(err)
	}

	full := bare
	full.SellerKey, full.SellerKeyQuality, full.SellerName = "brand:bioniqo", "slug", "Re:Market"
	full.MicroCategoryID, full.LocationID = 10, 637640
	full.Description = "Продаю телефон."
	full.ImageKey = "https://90.img.avito.st/image/1/1.ureJN7aAFl"
	if err := s.ApplyEnrichment(ctx, 501, full, 2000); err != nil {
		t.Fatal(err)
	}

	var sellerID sql.NullInt64
	var key, descSHA, imageKey string
	if err := s.DB().QueryRow(`
		SELECT seller_id, repost_key, description_sha, image_key
		FROM listings WHERE item_id = 501`).Scan(
		&sellerID, &key, &descSHA, &imageKey); err != nil {
		t.Fatal(err)
	}
	if !sellerID.Valid {
		t.Fatal("enrichment did not fill the seller")
	}
	if key != avito.RepostKey(full) {
		t.Errorf("repost_key = %q, want the key of the completed row", key)
	}
	if descSHA == "" || imageKey == "" {
		t.Errorf("the other signals were not filled: %q / %q", descSHA, imageKey)
	}

	// A second enrichment — a re-fetch, a retry, a second watch — must not move
	// the key. This is the same guarantee the title edit has.
	moved := full
	moved.Title = "совсем другой заголовок"
	moved.SellerKey = "brand:someone-else"
	if err := s.ApplyEnrichment(ctx, 501, moved, 3000); err != nil {
		t.Fatal(err)
	}
	var second string
	if err := s.DB().QueryRow(
		`SELECT repost_key FROM listings WHERE item_id = 501`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second != key {
		t.Errorf("a second enrichment moved the key: %q → %q", key, second)
	}
}

// TestTheQueueCountsWhatIsWaitingAndWhatIsDone, which is what the panel shows.
func TestTheQueueCountsWhatIsWaitingAndWhatIsDone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	var list []avito.Listing
	for i := 0; i < 5; i++ {
		l := aListing()
		l.ItemID = int64(600 + i)
		if i < 2 {
			l.SellerKey, l.SellerKeyQuality = "brand:x", "slug"
			l.MicroCategoryID, l.LocationID = 10, 1
		} else {
			l.SellerKey = ""
		}
		list = append(list, l)
	}
	if _, err := s.SavePass(ctx, Pass{
		WatchID: id, StartedAt: 1000, FinishedAt: 1001, Page: 1,
		Surface: avito.SurfaceMobile, Class: "ok", Path: "cards", Mode: "direct",
	}, list); err != nil {
		t.Fatal(err)
	}
	waiting, keyed, total, err := s.EnrichmentQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || keyed != 2 || waiting != 3 {
		t.Errorf("queue = waiting %d, keyed %d, total %d; want 3/2/5",
			waiting, keyed, total)
	}
}
```

- [ ] **Step 2: Run and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'NeedingSeller|NoPathIsNotOffered|Enrichment|QueueCounts' -v
```

Expected: `undefined: ListingsNeedingSeller`.

- [ ] **Step 3: Write `internal/store/enrich.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Enrichment is the second half of one decision.
//
// The mobile surface is the cheaper page — one request instead of two — and it
// carries no seller, no category, no location and no coordinates, measured at
// 0 of 30. Desktop is now what a new watch is offered, but a mobile watch is
// still a legitimate choice and the listings already collected from one are
// still there. So the seller is fetched separately for the rows that lack it.
//
// The rule that keeps this honest is that enrichment spends only headroom. A
// pass is the product working; enrichment is the product improving. Improving
// must never take a request that working needed, so the loop in
// cmd/avitomon/enrich.go asks the pacer what is free and stops when the answer
// is nothing.

// EnrichCandidate is one listing worth one request.
type EnrichCandidate struct {
	ItemID  int64
	URLPath string
}

// ListingsNeedingSeller is the queue, newest first.
//
// Newest first and not oldest: a listing collected an hour ago may still be in
// somebody's window and is worth linking; one from three weeks ago is past the
// repost window anyway. A queue drained oldest-first would spend its whole
// budget on rows that can no longer produce a link.
//
// A row with no url_path is skipped rather than fetched: there is nothing to
// open, and offering it would put a permanently failing item at the head of the
// queue for ever.
func (s *Store) ListingsNeedingSeller(ctx context.Context, limit int) ([]EnrichCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_id, url_path
		FROM listings
		WHERE seller_id IS NULL
		  AND url_path <> ''
		  AND state IN ('active', 'left-window')
		ORDER BY first_seen_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: enrich queue: %w", err)
	}
	defer rows.Close()

	var out []EnrichCandidate
	for rows.Next() {
		var c EnrichCandidate
		if err := rows.Scan(&c.ItemID, &c.URLPath); err != nil {
			return nil, fmt.Errorf("store: enrich queue: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApplyEnrichment fills what a later, richer reading of one listing found.
//
// The WHERE clause is the whole safety property. `seller_id IS NULL` means this
// runs at most once per listing, whatever calls it and however often — so
// repost_key is written exactly once here and can never be moved afterwards,
// which is the same guarantee a title edit has.
//
// Writing the key here rather than leaving it empty is a decision worth stating:
// the schema freezes the key so that an EDIT cannot move it, and a seller
// arriving is not an edit — it is the row becoming complete. A key frozen empty
// would mean the enrichment request bought nothing.
func (s *Store) ApplyEnrichment(ctx context.Context, itemID int64,
	l avito.Listing, at int64) error {

	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: enrich: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sellerID, err := upsertSeller(ctx, tx, l, at)
	if err != nil {
		return err
	}
	if sellerID == 0 {
		// The richer page identified no seller either. Nothing is written and
		// nothing is marked, so the row stays in the queue — which is right:
		// the next attempt may read a page that does carry one. The loop's own
		// per-run limit is what stops this becoming a spin.
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE listings SET
		    seller_id         = ?,
		    micro_category_id = max(micro_category_id, ?),
		    category_root_id  = max(category_root_id, ?),
		    location_id       = max(location_id, ?),
		    location_name     = CASE WHEN ? = '' THEN location_name ELSE ? END,
		    geo_address       = CASE WHEN ? = '' THEN geo_address ELSE ? END,
		    lat               = coalesce(lat, ?),
		    lng               = coalesce(lng, ?),
		    coords_precise    = max(coords_precise, ?),
		    description_sha   = CASE WHEN ? = '' THEN description_sha ELSE ? END,
		    image_key         = CASE WHEN ? = '' THEN image_key ELSE ? END,
		    repost_key        = CASE WHEN repost_key = '' THEN ? ELSE repost_key END
		WHERE item_id = ? AND seller_id IS NULL`,
		sellerID,
		l.MicroCategoryID, l.CategoryRootID, l.LocationID,
		l.LocationName, l.LocationName,
		l.GeoAddress, l.GeoAddress,
		l.Lat, l.Lng, l.CoordsPrecise,
		avito.DescriptionSHA(l.Description), avito.DescriptionSHA(l.Description),
		l.ImageKey, l.ImageKey,
		avito.RepostKey(l),
		itemID); err != nil {
		return fmt.Errorf("store: enrich %d: %w", itemID, err)
	}
	if err := maybeSellerSnapshot(ctx, tx, sellerID, l, at); err != nil {
		return err
	}
	return tx.Commit()
}

// EnrichmentQueue is the three numbers the panel shows: how many listings are
// waiting for a seller, how many have one, and how many there are.
//
// Three and not one, because «412 waiting» means nothing without «of 12 000» —
// and because the ratio is the shipped metric that says whether enrichment is
// keeping up or falling behind.
func (s *Store) EnrichmentQueue(ctx context.Context) (waiting, keyed, total int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
		  sum(CASE WHEN seller_id IS NULL AND url_path <> '' THEN 1 ELSE 0 END),
		  sum(CASE WHEN seller_id IS NOT NULL THEN 1 ELSE 0 END),
		  count(*)
		FROM listings`).Scan(&waiting, &keyed, &total)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("store: enrich queue: %w", err)
	}
	return waiting, keyed, total, nil
}
```

If `Scan` fails on NULL for an empty table, wrap the two sums in `coalesce(..., 0)`.

- [ ] **Step 4: Run the store tests**

```bash
cd S:/Projects/Avito_Go && go test ./internal/store/ -run 'NeedingSeller|NoPathIsNotOffered|Enrichment|QueueCounts' -v
```

Expected: four PASS.

- [ ] **Step 5: Write `cmd/avitomon/enrich.go`**

Read `cmd/avitomon/run.go` first — `openLeaser`, `leaserOpts` and the pacer construction are reused verbatim rather than repeated.

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// enrichCmd fills the seller for listings collected from a surface that does
// not carry one.
//
// It is a separate command and not a step inside `run` on purpose. A pass is
// the product working and this is the product improving; if they shared a
// command, a slow enrichment would delay a scheduled pass, and the request that
// improvement spent would be the one collection needed.
func enrichCmd(args []string) error {
	fs := flag.NewFlagSet("enrich", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "путь к базе")
	limit := fs.Int("limit", 20, "сколько объявлений дополнить за прогон")
	var opts leaserOpts
	fs.StringVar(&opts.control, "control", "", "адрес control API BlankTrail")
	fs.StringVar(&opts.key, "key", "", "ключ BlankTrail")
	fs.StringVar(&opts.proxyFile, "proxies", "", "файл со списком прокси")
	fs.StringVar(&opts.statePath, "state", "", "файл сессии прямого режима")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	candidates, err := s.ListingsNeedingSeller(ctx, *limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("дополнять нечего: у всех объявлений есть продавец")
		return nil
	}

	mode, err := s.TransportMode(ctx)
	if err != nil {
		return err
	}
	leaser, err := openLeaser(ctx, s, mode, opts)
	if err != nil {
		return err
	}
	defer leaser.Close()

	rpm, err := s.RequestsPerMinute(ctx)
	if err != nil {
		return err
	}
	pacer := transport.NewPacer(transport.PacerConfig{
		Target:  rpm,
		Floor:   transport.DefaultFloor,
		Jitter:  transport.DefaultJitter,
		Ceiling: transport.DefaultCeiling,
		Window:  transport.DefaultWindow,
	})
	pacer.SetAddresses(leaser.Caps().Addresses)

	// The desktop surface, always. The whole reason a listing is in this queue
	// is that the surface it was collected from carries no seller, and asking
	// that surface again would spend a request to learn nothing.
	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{
		Surface: avito.SurfaceDesktop,
	})

	done, empty := 0, 0
	for _, c := range candidates {
		if err := s.CheckSpace(ctx); err != nil {
			return err
		}
		res, ferr := f.Fetch(ctx, "https://www.avito.ru"+c.URLPath)
		if ferr != nil {
			fmt.Printf("объявление %d: %v\n", c.ItemID, ferr)
			// A halt stops the whole run. Enrichment is the lowest-priority
			// traffic there is, and continuing to spend requests into a wall
			// would burn the address collection needs.
			var halt *transport.HaltError
			if errorsAs(ferr, &halt) {
				return ferr
			}
			continue
		}
		var found *avito.Listing
		for i := range res.Page.Listings {
			if res.Page.Listings[i].ItemID == c.ItemID {
				found = &res.Page.Listings[i]
				break
			}
		}
		if found == nil || found.SellerKey == "" {
			empty++
			continue
		}
		if err := s.ApplyEnrichment(ctx, c.ItemID, *found, time.Now().Unix()); err != nil {
			return err
		}
		done++
	}

	waiting, keyed, total, err := s.EnrichmentQueue(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("дополнено %d, без продавца на странице %d\n", done, empty)
	fmt.Printf("осталось %d из %d; продавец известен у %d\n", waiting, total, keyed)
	return nil
}
```

`errorsAs` above is a placeholder for `errors.As` — write `errors.As(ferr, &halt)` and import `"errors"`. It is spelled differently here only so a copy-paste does not silently compile with the wrong import set.

In `cmd/avitomon/main.go`, add beside the existing `case "panel":`

```go
	case "enrich":
		err = enrichCmd(os.Args[2:])
```

and beside the usage lines:

```go
	fmt.Fprintln(os.Stderr, "  avitomon enrich [-db avito.db] [-limit 20]")
```

- [ ] **Step 6: Full check and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./avito ./cmd ./internal && go vet ./... && go test ./... 2>&1 | grep -v "no test files"
```

```bash
git add internal/store/ cmd/avitomon/ && git commit -m "feat(enrich): the seller a mobile watch cannot see"
```

---

## Task 6: The panel says which surface, and how far behind enrichment is

**Files:**
- Modify: `internal/web/watches.go` (the default surface), `internal/web/health.go` (the queue)
- Test: `internal/web/watches_test.go`, `internal/web/health_test.go` (append)

**Interfaces:**
- Consumes: `store.EnrichmentQueue`.
- Produces: nothing later tasks read.

The label was corrected in commit `0991e50` and already says what mobile leaves out. What is left is the default itself and the queue.

- [ ] **Step 1: Write the failing test**

```go
// TestANewWatchIsOfferedTheDesktopSurface.
//
// Reversed deliberately. Mobile is the cheaper page — one request instead of
// two — but it carries no seller, no category, no location and no coordinates,
// 0 of 30, so a mobile watch can never link a repost. Cheap and unable to do
// the thing is the wrong default; the label beside it still offers mobile and
// says what it costs.
func TestANewWatchIsOfferedTheDesktopSurface(t *testing.T) {
	body := get(t, newServer(t), "/").Body.String()
	i := strings.Index(body, `name="surface"`)
	if i < 0 {
		t.Fatal("no surface control on the page")
	}
	form := body[i:]
	if !strings.Contains(form[:strings.Index(form, "</select>")], `value="desktop" selected`) {
		t.Errorf("desktop is not the selected option: %s",
			form[:strings.Index(form, "</select>")])
	}
	// And mobile is still offered, with its price.
	if !strings.Contains(body, `value="mobile"`) {
		t.Error("the mobile surface is no longer offered at all")
	}
	if !strings.Contains(body, "не отдаёт ни продавца") {
		t.Error("the screen stopped saying what the mobile surface leaves out")
	}
}

// TestTheHealthScreenSaysHowManyListingsHaveNoSeller.
//
// The queue is a shipped metric and not a debugging aid: it is what says
// whether enrichment is keeping up, and «412 waiting» means nothing without
// «of 12 000».
func TestTheHealthScreenSaysHowManyListingsHaveNoSeller(t *testing.T) {
	s := newServer(t)
	id := aWatch(t, s, "наблюдение")
	// Two with a seller, three without.
	for i := 0; i < 5; i++ {
		item := int64(700 + i)
		seller := "NULL"
		if i < 2 {
			seller = "1"
		}
		if i == 0 {
			if _, err := s.Store.DB().Exec(`
				INSERT INTO sellers (seller_key, key_quality, first_seen_at, last_seen_at)
				VALUES ('brand:x', 'slug', 1000, 1000)`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Store.DB().Exec(`
			INSERT INTO listings (item_id, thread_id, url_path, title, seller_id,
			                      first_seen_at, last_seen_at, state_at)
			VALUES (?, ?, '/m/x', 'т', `+seller+`, 1000, 1000, 1000)`,
			item, item); err != nil {
			t.Fatal(err)
		}
		_ = id
	}

	body := get(t, s, "/health").Body.String()
	if !strings.Contains(body, "продавец") {
		t.Errorf("the health screen says nothing about sellers: %s", body)
	}
	if !strings.Contains(body, "3") || !strings.Contains(body, "5") {
		t.Error("the queue's numbers are not on the screen")
	}
}
```

- [ ] **Step 2: Run and watch the first fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -run 'DesktopSurface|NoSeller' -v
```

- [ ] **Step 3: Flip the default and mark the option**

In `internal/web/watches.go`, the blank draft at line 38 becomes `avito.SurfaceDesktop`, and the two `<option>` elements gain a `selected` attribute driven by the draft rather than being hard-coded. Use the existing `selected(bool) string` helper from `internal/web/results.go`:

```go
	b.WriteString(`<label class="field"><span class="label">Версия сайта</span>` +
		`<select class="input" name="surface">` +
		fmt.Sprintf(`<option value="desktop"%s>десктопная — два запроса на страницу, `+
			`50 объявлений, и только она несёт продавца</option>`,
			selected(d.Surface != avito.SurfaceMobile)) +
		fmt.Sprintf(`<option value="mobile"%s>мобильная — дешевле: `+
			`один запрос на страницу, 30 объявлений</option>`,
			selected(d.Surface == avito.SurfaceMobile)) +
		`</select>` +
		`<span class="hint">Мобильная выдача не отдаёт ни продавца, ни категорию, ` +
		`ни координаты, ни описание. Перевыкладки на таком наблюдении ` +
		`не связываются — их можно дополнить отдельно командой ` +
		`«avitomon enrich», по одному запросу на объявление.</span></label>`)
```

- [ ] **Step 4: Add the queue to Здоровье сбора**

In `internal/web/health.go`, inside the card that already prints the database table counts, add above the table:

```go
	waiting, keyed, total, err := s.Store.EnrichmentQueue(ctx)
	if err == nil && total > 0 {
		fmt.Fprintf(&b, `<p class="sum">Продавец известен у <strong>%d</strong> `+
			`объявлений из %d.</p>`, keyed, total)
		if waiting > 0 {
			fmt.Fprintf(&b, `<p class="hint">У %d %s продавца нет — это те, что `+
				`собраны с мобильной выдачи. Она его не отдаёт. Дополнить: `+
				`<code>avitomon enrich</code>, один запрос на объявление.</p>`,
				waiting, esc(ru.Plural(waiting, "объявления", "объявлений", "объявлений")))
		}
	}
```

Check `ru.Plural`'s three forms against an existing call site before using it — `grep -n "ru.Plural" internal/web/*.go`.

- [ ] **Step 5: Run the tests**

```bash
cd S:/Projects/Avito_Go && go test ./internal/web/ -v 2>&1 | grep -E "^(--- FAIL|ok|FAIL)"
```

The existing `TestTheSurfaceLabelSaysWhichWayRoundItIs` must still pass; if it fails, the label wording changed in a way that dropped one of the two counts, and the wording is what should be fixed.

- [ ] **Step 6: Full check and commit**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./avito ./cmd ./internal && go vet ./... && go test ./... 2>&1 | grep -v "no test files"
```

```bash
git add internal/web/ && git commit -m "feat(web): desktop is the offered surface, and the seller queue is on screen"
```

---

## Task 7: The demonstrable — the export's «Продавец» column stops being empty

**Files:**
- Create: `docs/measurements/2026-08-XX-the-row-fills.md`

The demonstrable for 7A is exactly the defect 6C's measurement recorded: three of the sixteen export columns came back empty on all sixty rows. This task shows them filling, on live data.

- [ ] **Step 1: Full verification**

```bash
cd S:/Projects/Avito_Go && gofmt -l ./cmd ./internal ./avito && go vet ./... && \
  go build ./... && go test ./... 2>&1 | grep -v "no test files"
```

- [ ] **Step 2: Run it against live Avito, both surfaces**

Fresh database. Two watches on the same query, one desktop and one mobile, so the comparison is controlled rather than two different searches:

```bash
cd <scratchpad>/demo7a && ../avitomon.exe panel -db demo.db -port 8766
```

Add both watches through the browser, choose «напрямую», then:

```bash
../avitomon.exe run --once -db demo.db
```

twice, once per watch.

- [ ] **Step 3: Count what filled, per surface**

```bash
../avitomon.exe volume -db demo.db
```

and directly:

```sql
SELECT w.surface, count(*), count(l.seller_id),
       sum(l.repost_key <> ''), sum(l.image_key <> ''), sum(l.description_sha <> '')
FROM watch_members m
JOIN watches w ON w.id = m.watch_id
JOIN listings l ON l.item_id = m.item_id
GROUP BY w.surface;
```

Record the counts. The expectation from the fixtures is roughly 46 of 50 sellers on desktop and 0 of 30 on mobile; **if the live page differs, the live page is what the document says**, and the difference from the fixture is itself worth a sentence.

- [ ] **Step 4: Enrich the mobile rows and count again**

```bash
../avitomon.exe enrich -db demo.db -limit 10
```

Run the same query. Record how many filled, how many pages carried no seller, and what one enrichment request cost in time.

- [ ] **Step 5: Download the export and open it**

Through the panel: `/results` with no filter, XLSX. Open it in Excel and confirm «Продавец» carries numbers where the desktop watch's rows are — the column that was empty on all sixty rows in `2026-08-25-panel-6c.md` §4.2.

- [ ] **Step 6: Write it up and commit**

The document must contain, at minimum: the per-surface fill counts, what enrichment cost and recovered, the image-key fill rate (the spec's shipped metric, first reading), and — if the browser pane still does not composite — the same §1 note 6A, 6B and 6C carry.

```bash
git add docs/measurements/ && git commit -m "docs(measurements): the row fills, and what each surface actually gave"
```

---

## Done when

- `gofmt -l` prints nothing; `go vet ./...` clean; `go test ./...` passes.
- `ImageKey` collapses six variants to one key, refuses fewer than two, and refuses a prefix too short to discriminate.
- The seller is read from both spellings, with `key_quality` recording which, and a key is never recorded without one.
- `FoldTitle` folds Cyrillic case and collapses punctuation with no regex.
- `RepostKey` is empty when there is nothing to key on, and moves with each of its four terms.
- The measured collision counts hold as a test: 7 distinct folded titles across the 50 desktop listings, 46 distinct keys.
- A pass writes `seller_id`, the category triple, the location, the coordinates, `title_norm`, `repost_key`, `description_sha` and `image_key`.
- `sellers` has rows, and two listings by one seller produce one.
- A title edited later does not move `repost_key`; neither does a second enrichment.
- A seller snapshot is written when the rating moves and not when it does not.
- Enrichment offers only listings with no seller and a path, newest first, and fills the key exactly once.
- The panel offers desktop by default, still offers mobile, and says what mobile costs.
- Здоровье сбора shows how many listings have a seller, out of how many.
- The demonstrable is performed on live data and the export's «Продавец» column carries values.

## What this plan deliberately does not build

**The linker.** No candidate seek, no gates, no ladder, no `repost_links`, no `RecomputeThread`, no Перепубликации screen, no alert suppression. That is 7B, and it is a separate plan because this one produces working, testable software on its own: the export column fills, the panel shows the coverage, and the enrichment command works — none of which needs a link to exist.

**A scheduler for enrichment.** `avitomon enrich` is run by hand, exactly as `avitomon run --once` is. M1 has no scheduler and this plan does not smuggle one in for the lower-priority traffic.

**Backfilling the sellers of already-collected listings beyond the queue's own order.** `ListingsNeedingSeller` is newest-first and limited per run. A historical backfill of a large database is a different operation with a different budget question, and nobody has asked for one.

**Reading `seller_snapshots` anywhere.** M1 writes this table and emits no events from it — the spec says so. The panel does not show a rating and no rule can fire on one.

**Changing the export's Cyrillic search to use `title_norm`.** This plan finally writes that column, but `FoldTitle` strips punctuation, so a substring search over it is a different query from the one `internal/store/export.go` runs today. Making the search faster is not the same as making it right, and it is not this plan's job.

## Self-review

**Spec coverage.** §Identity and lifecycle's repost paragraphs: `repost_key`'s composition and its write-once rule (Tasks 3, 4, 5); folding without regex (Task 3); `sellers` with the surrogate id and `key_quality`, and the heuristic key never accepted as evidence (Tasks 2, 4); the image key from the longest common prefix and its shipped metric (Tasks 1, 7). The five gates, the ladder, `repost_links`, `repost_pins`, `RecomputeThread`, retraction and the Перепубликации screen are increment 7B and are named above as out of scope.

**Placeholder scan.** One deliberate stand-in: `errorsAs` in Task 5 Step 5, which the step's own text tells the reader to write as `errors.As`. Two steps ask the executor to read an existing helper before using it (`parseFixture`, `aListing`, `ru.Plural`) rather than repeating its body — that is a reference to code that exists, not a placeholder. Task 1 Step 7 does not name the golden-regeneration switch because this plan's author did not confirm which one this repository uses; the step gives the command to find it and the check to run afterwards, which is honest rather than invented.

**Type consistency.** `Listing.ImageKey` (Task 1) is read by `avito.RepostKey`'s neighbours and by `upsertListing` (Task 4) and `ApplyEnrichment` (Task 5) under that exact name. `SellerKeyQuality` (Task 2) is read by `upsertSeller` (Task 4). `avito.FoldTitle`, `avito.RepostKey`, `avito.DescriptionSHA` (Task 3) are called by Tasks 4 and 5 with those signatures. `store.EnrichCandidate{ItemID, URLPath}` (Task 5) is consumed only by `cmd/avitomon/enrich.go` in the same task. `store.EnrichmentQueue` returns `(waiting, keyed, total int, err error)` in Task 5 and is called with that shape in Task 6.

**One known weakness, stated rather than hidden.** Task 3's `TestTheMeasuredCollisionCountsHold` pins numbers produced by a Python folding that used `NFKC` normalisation, which the Go implementation does not do. The step tells the executor not to silently adopt a different number but to understand the difference first. If Go's folding legitimately produces 8 rather than 7 distinct titles, the right action is to change the test's expected value **and** add a sentence to the measurement document saying the two foldings differ and by how much.
