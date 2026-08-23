// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"encoding/json"
	"strings"
)

// initialDataPrefix is what the mobile state block's text begins with. The
// block carries no attributes at all, so its content is the only handle there
// is — weaker than an attribute, and the caller records that it was used.
const initialDataPrefix = "window.__initialData__"

// mobileSearchState is the part of __initialData__ this package reads. The listings
// are NOT in it — measured: urlPath and priceDetailed appear zero times — they
// are the server-rendered cards. What is here is what the classifier needs.
type mobileSearchState struct {
	Search struct {
		Count       *int64 `json:"count"`
		MainCount   *int64 `json:"mainCount"`
		EmptySearch string `json:"emptySearch"`
	} `json:"search"`
}

// firstJSString returns the first double-quoted JavaScript string literal in s,
// with its quotes and escapes intact.
//
// A two-state escape-aware scan, and deliberately neither a regex nor a split
// on the quote: the block continues with further statements after the literal,
// and an escaped quote inside the JSON must not end it.
func firstJSString(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	esc := false
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		if esc {
			esc = false
			continue
		}
		switch c {
		case '\\':
			esc = true
		case '"':
			return s[i : j+1], true
		}
	}
	return "", false
}

// readMobileState finds and decodes window.__initialData__. It is a JS string
// literal holding JSON text, so it unmarshals twice.
func readMobileState(body []byte) (mobileSearchState, bool) {
	text, ok := scriptTextByPrefix(body, initialDataPrefix)
	if !ok {
		return mobileSearchState{}, false
	}
	lit, ok := firstJSString(text)
	if !ok {
		return mobileSearchState{}, false
	}
	var inner string
	if err := json.Unmarshal([]byte(lit), &inner); err != nil {
		return mobileSearchState{}, false
	}
	var st mobileSearchState
	if err := json.Unmarshal([]byte(inner), &st); err != nil {
		return mobileSearchState{}, false
	}
	return st, true
}
