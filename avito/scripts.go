// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// attrOf returns the named attribute of the tag the tokenizer is sitting on.
// The value is copied: x/net/html documents its slices as invalid after the
// next Next(), and a reused buffer corrupts data only on documents large
// enough to trigger it — which is to say only in production.
func attrOf(z *html.Tokenizer, name string) (string, bool) {
	for {
		k, v, more := z.TagAttr()
		if string(k) == name {
			return string(v), true // string() copies
		}
		if !more {
			return "", false
		}
	}
}

// scriptTextByAttr finds the first <script> carrying attr=value and returns its
// text, together with whatever its type attribute said.
//
// Selection is on the data attribute and never on the type. The type is a
// deliberate non-MIME chosen so no browser executes the block; it is
// presentation trivia and the likeliest thing to be renamed, and requiring it
// would make a cosmetic change read as a total parse failure. It is returned so
// a drift report can say it moved.
//
// The text is returned exactly as the server sent it. Nothing here unescapes:
// x/net/html implements the HTML5 script-data state, so Text() is already the
// raw bytes, and a second pass would rewrite a literal &amp; inside a JSON
// string to & — corrupting a title with no error anywhere.
func scriptTextByAttr(body []byte, attr, value string) (text, scriptType string, found bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", "", false
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" || !hasAttr {
				continue
			}
			var typ string
			match := false
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case attr:
					if string(v) == value {
						match = true
					}
				case "type":
					typ = string(v)
				}
				if !more {
					break
				}
			}
			if !match {
				continue
			}
			if z.Next() != html.TextToken {
				return "", typ, false
			}
			return string(z.Text()), typ, true // string() copies
		}
	}
}

// scriptTextByPrefix finds the first <script> with no src whose text begins
// with the given prefix, and returns that text.
//
// The mobile surface's state block carries no attributes at all, so there is
// nothing to select on but its content. That is weaker than an attribute and it
// is recorded as such by the caller.
func scriptTextByPrefix(body []byte, prefix string) (string, bool) {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return "", false
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			hasSrc := false
			if hasAttr {
				for {
					k, _, more := z.TagAttr()
					if string(k) == "src" {
						hasSrc = true
					}
					if !more {
						break
					}
				}
			}
			if hasSrc {
				continue
			}
			if z.Next() != html.TextToken {
				continue
			}
			text := string(z.Text()) // string() copies
			if strings.HasPrefix(strings.TrimSpace(text), prefix) {
				return text, true
			}
		}
	}
}

// textOfMarker returns the subtree text of the first element carrying
// data-marker=name, with runs of whitespace collapsed.
//
// It walks the element's own depth rather than reading a fixed number of bytes
// after the marker. Reading ahead N characters returns the class attribute,
// which sits between the marker and the text — that is not a hypothetical, it
// is the mistake that was made while measuring this site, and it is exactly the
// fragility the no-regex rule exists to prevent.
func textOfMarker(body []byte, name string) string {
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			tag, hasAttr := z.TagName()
			if !hasAttr {
				continue
			}
			owned := append([]byte(nil), tag...)
			marker, ok := attrOf(z, "data-marker")
			if !ok || marker != name {
				continue
			}
			return subtreeText(z, owned)
		}
	}
}

// voidElements never have a closing tag, so they must not change the depth the
// subtree walk is counting.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// subtreeText collects the text of the element the tokenizer has just entered,
// including every nested element, stopping at that element's own closing tag.
func subtreeText(z *html.Tokenizer, tag []byte) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		switch z.Next() {
		case html.ErrorToken:
			depth = 0
		case html.StartTagToken:
			name, _ := z.TagName()
			if !voidElements[string(name)] && bytes.Equal(name, tag) {
				depth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if bytes.Equal(name, tag) {
				depth--
			}
		case html.TextToken:
			b.Write(z.Text())
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// subtreeHTML re-serialises the element the tokenizer has just entered, so the
// element can be walked again for its own markers without re-scanning the page.
func subtreeHTML(z *html.Tokenizer, tag []byte) []byte {
	var buf bytes.Buffer
	depth := 1
	for depth > 0 {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		buf.Write(z.Raw()) // Write copies
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			if !voidElements[string(name)] && bytes.Equal(name, tag) {
				depth++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if bytes.Equal(name, tag) {
				depth--
			}
		}
	}
	return buf.Bytes()
}

// scanMarkerAttrs walks the document once and returns one named attribute of
// every element carrying one of the wanted data-marker values.
//
// The companion to scanMarkers, which returns subtree text. The seller on an
// item page is an anchor whose href is the answer and whose text is the whole
// seller block run together — «Re:Market4,91898 отзывовКомпания · На Авито c
// 2020 года» — so the text is not something to parse a name out of, and the
// attribute is not something to reach with a pattern. One walk, one attribute,
// no regex against markup.
//
// Both attributes are read in a single pass over the token, because the
// tokenizer's attribute cursor only moves forward: attrOf consumes what it
// walked past, so calling it twice on one token would look for the second
// attribute starting after the first — and find it only when the document
// happened to order them that way.
//
// First occurrence wins, the same rule scanMarkers keeps: a marker repeated
// later is a second rendering of the same thing, and taking the last would make
// the answer depend on how far the page got.
func scanMarkerAttrs(body []byte, want map[string]bool, attr string) map[string]string {
	out := map[string]string{}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			if _, hasAttr := z.TagName(); !hasAttr {
				continue
			}
			marker, value := "", ""
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "data-marker":
					marker = string(v) // string() copies
				case attr:
					value = string(v)
				}
				if !more {
					break
				}
			}
			if marker == "" || !want[marker] || value == "" {
				continue
			}
			if _, taken := out[marker]; !taken {
				out[marker] = value
			}
		}
	}
}
