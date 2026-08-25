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
// the two digests built from it.
//
// It is here and not in internal/store because every value is a pure function
// of one Listing and knows nothing about a database. The schema's rule that
// repost_key is written once and never updated is storage's, and it lives in
// internal/store/pass.go where the ON CONFLICT clause is.

// FoldTitle is the title with everything a person would not notice removed.
//
// unicode-driven, through a strings.Builder. The spec asks for that and it is
// also simply correct: Cyrillic case folding and regexp do not mix well, and
// every title on this site is Cyrillic.
//
// Punctuation and whitespace alike collapse to one separator, and to a
// separator rather than to nothing. «15, 128 ГБ» and «15 128 ГБ» are the same
// title to a reader and must fold alike; «Продам-куплю» and «продамкуплю» are
// not, and a folding that ran them together would bucket two listings on the
// strength of a hyphen.
func FoldTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	gap := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if gap && b.Len() > 0 {
				b.WriteByte(' ')
			}
			gap = false
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		gap = true
	}
	return b.String()
}

// RepostKey is the candidate bucket: seller, micro-category, location and the
// folded title, hashed together.
//
// Empty when the seller, the category and the location are all missing, which
// is every listing read from the mobile surface. That refusal is the point. A
// key over the title alone puts fifty listings in seven buckets — measured —
// and the linker's «exactly one surviving candidate» gate would then reject
// every pair after doing the work of looking. Empty says «this row cannot be
// keyed» once, and the seek never happens.
//
// The seller is what does the discriminating: with it, the same fifty listings
// fall into forty-seven buckets.
//
// Empty also for a title that folds to nothing, because the title is the term
// the ladder actually compares — the other three only say where to look.
//
// Each term is followed by a NUL, which no term can contain. Without it
// ("brand:x1", 0) and ("brand:x", 10) hash alike, and a seller slug ending in a
// digit beside a category id is exactly that shape.
func RepostKey(l Listing) string {
	if l.SellerKey == "" && l.MicroCategoryID == 0 && l.LocationID == 0 {
		return ""
	}
	title := FoldTitle(l.Title)
	if title == "" {
		return ""
	}
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
// Empty for an empty description, and the ladder contributes nothing from an
// absent signal. That rule matters more here than anywhere: only twenty rows in
// fifty carry a description at all, so a weighted average that renormalised
// over present signals would let a title-and-price pair score as high as a
// title-price-description-image pair. That is a false-positive factory, and a
// false link silently withholds an alert and looks like data rather than a bug.
func DescriptionSHA(s string) string {
	folded := FoldTitle(s)
	if folded == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(folded))
	return hex.EncodeToString(sum[:])
}
