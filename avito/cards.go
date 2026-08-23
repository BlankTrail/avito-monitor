// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// cardFields is what one pass collected from one card: the text of every
// element carrying a data-marker we asked for, and the attributes of every
// element carrying a data-marker or itemprop we asked for.
type cardFields struct {
	ID    string            // the card's own data-item-id, when it had one
	Text  map[string]string // data-marker -> subtree text
	Attrs map[string]string // "<marker|prop>#<attr>" -> value
}

func (c cardFields) text(marker string) string { return c.Text[marker] }

func (c cardFields) attr(key, attr string) (string, bool) {
	v, ok := c.Attrs[key+"#"+attr]
	return v, ok
}

// cardSpec says which card openings to recognise and which fields to keep.
type cardSpec struct {
	// isCard decides, from a start tag's data-marker, whether a card begins
	// here, and returns the card's identifier when the marker carries one.
	isCard func(marker string) (id string, ok bool)
	// wantText are data-marker values whose subtree text is kept.
	wantText map[string]bool
	// wantAttr maps a data-marker or itemprop value to the attributes kept
	// from the element carrying it.
	wantAttr map[string][]string
}

// scanCards walks the document ONCE and returns one cardFields per card.
//
// The obvious implementation — find a card, re-serialise its subtree, then
// tokenise that subtree again per field — is what the first version did, and it
// does not survive a real page. Real markup does not close every element, so a
// depth counter keyed on the tag name can run to the end of the document; with
// fifty cards on a 2.28 MB page that is fifty full scans and fifty full copies,
// and the parse simply stops finishing. One pass with a small stack is both
// correct on unbalanced markup and linear in the size of the page.
func scanCards(body []byte, spec cardSpec) []cardFields {
	var out []cardFields

	z := html.NewTokenizer(bytes.NewReader(body))
	// Card state.
	inCard := false
	var cardTag []byte
	cardDepth := 0
	var cur cardFields
	// Field-capture state: the element whose text we are collecting.
	capturing := ""
	var capTag []byte
	capDepth := 0
	var capBuf bytes.Buffer

	finish := func() {
		if inCard {
			out = append(out, cur)
		}
		inCard, capturing = false, ""
		cardDepth, capDepth = 0, 0
	}

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			finish()
			return out
		}

		switch tt {
		case html.TextToken:
			if capturing != "" {
				capBuf.Write(z.Text())
			}

		case html.StartTagToken, html.SelfClosingTagToken:
			tag, hasAttr := z.TagName()
			owned := append([]byte(nil), tag...)
			void := tt == html.SelfClosingTagToken || voidElements[string(owned)]

			var marker, prop, itemID string
			attrs := map[string]string{}
			if hasAttr {
				for {
					k, v, more := z.TagAttr()
					key, val := string(k), string(v)
					switch key {
					case "data-marker":
						marker = val
					case "itemprop", "itemProp":
						prop = val
					case "data-item-id":
						itemID = val
					}
					attrs[key] = val
					if !more {
						break
					}
				}
			}

			// A new card begins. If one was already open, real markup left it
			// unclosed; close it here rather than swallowing the rest of the
			// document.
			if id, ok := spec.isCard(marker); ok {
				finish()
				inCard = true
				cardTag = owned
				cardDepth = 1
				if id != "" {
					itemID = id
				}
				cur = cardFields{ID: itemID, Text: map[string]string{}, Attrs: map[string]string{}}
				continue
			}
			if !inCard {
				continue
			}
			if !void && bytes.Equal(owned, cardTag) {
				cardDepth++
			}

			// Attributes we were asked to keep, from either handle.
			for _, key := range []string{marker, prop} {
				if key == "" {
					continue
				}
				for _, a := range spec.wantAttr[key] {
					if v, ok := attrs[a]; ok {
						if _, taken := cur.Attrs[key+"#"+a]; !taken {
							cur.Attrs[key+"#"+a] = v
						}
					}
				}
			}
			// Text we were asked to keep. The first occurrence wins: a card
			// repeating a marker is the site's business, not ours.
			if capturing == "" && marker != "" && spec.wantText[marker] && !void {
				if _, taken := cur.Text[marker]; !taken {
					capturing = marker
					capTag = owned
					capDepth = 1
					capBuf.Reset()
				}
			} else if capturing != "" && !void && bytes.Equal(owned, capTag) {
				capDepth++
			}

		case html.EndTagToken:
			if !inCard {
				continue
			}
			name, _ := z.TagName()
			if capturing != "" && bytes.Equal(name, capTag) {
				capDepth--
				if capDepth == 0 {
					cur.Text[capturing] = collapse(capBuf.String())
					capturing = ""
				}
			}
			if bytes.Equal(name, cardTag) {
				cardDepth--
				if cardDepth == 0 {
					finish()
				}
			}
		}
	}
}

// collapse squeezes runs of whitespace, so a value read out of pretty-printed
// markup compares equal to the same value read out of minified markup. It is
// strings.Fields and not a byte scan on purpose: Fields treats U+00A0 as space,
// which is what subtreeText already does, and two different collapse rules in
// one package would make the two extraction paths disagree about a price label
// for no reason a reader could ever guess.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
