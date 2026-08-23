// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEmptyBody  = errors.New("avito: empty body")
	ErrNotUTF8    = errors.New("avito: body is not valid UTF-8")
	ErrNoCyrillic = errors.New("avito: body carries no Cyrillic")
)

// Document is one fetched page, handed to this package by a caller who did the
// fetching, the decompressing and the decoding.
type Document struct {
	Surface Surface
	URL     string
	Body    []byte
}

// Check enforces the precondition x/net/html's own documentation states and
// does not verify: the input is assumed to be UTF-8.
//
// The Cyrillic test looks like superstition and is not. Avito serves br and
// zstd, and a body that reaches a reader still compressed produces no wall
// markers and no items — so a block reads as a healthy 200 and a parse failure
// reads as an empty market, both silently. A compressed stream is nearly always
// invalid UTF-8, and on the rare occasion it is not, it has no Cyrillic in it.
// Any page this program fetches from a Russian classified site has some.
func (d Document) Check() error {
	if len(d.Body) == 0 {
		return ErrEmptyBody
	}
	if !utf8.Valid(d.Body) {
		return ErrNotUTF8
	}
	for _, r := range string(d.Body) {
		if unicode.Is(unicode.Cyrillic, r) {
			return nil
		}
	}
	return ErrNoCyrillic
}
