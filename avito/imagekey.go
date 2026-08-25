// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

// minImageKeyLen is the floor under a usable key.
//
// A real key measured 44 characters, of which the leading
// "https://NN.img.avito.st/image/N/N." is fixed and about ten discriminate.
// Two URLs from different hosts share only "https://", which is eight — so any
// prefix shorter than that fixed part cannot be a photo id and is refused. 34
// is the fixed part at its shortest.
const minImageKeyLen = 34

// ImageKey is one photo's identity, taken from its size variants.
//
// Measured rather than parsed: a photo arrives as exactly six variants sharing
// a prefix and diverging in the middle of a token, so the longest common prefix
// is what they have in common and no rule about Avito's URL grammar is needed.
//
// The spec called that prefix «the object id». It is the first ten
// discriminating characters of a longer token — enough to tell photos apart,
// which is what it is for, and not the same claim. Whether those ten survive a
// re-upload is unverified, which is why the fraction of confirmed links
// carrying this term is a shipped metric rather than an assumption.
//
// Fewer than two variants yields nothing. A single URL is its own prefix, and
// that prefix is the part that differs between variants — so it would match
// nothing and be believed anyway. An absent signal must contribute nothing.
//
// The result does not depend on the order the variants arrive in: the longest
// common prefix of a set is the same whichever pair is folded first. That
// matters because they come out of a JSON object and Go randomises map range
// order per run, and a key that changed between runs of one build would be a
// link that appeared and vanished with nothing to blame.
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
	return prefix
}

// commonPrefix walks bytes and not runes, deliberately: these are URLs, which
// are ASCII, and a rune-wise walk would buy nothing but a slower loop.
func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
