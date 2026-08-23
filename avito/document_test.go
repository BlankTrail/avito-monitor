// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
)

func TestDocumentCheck(t *testing.T) {
	good := []byte("<html><body><div>Телефоны</div></body></html>")

	// A really-compressed body, because the failure this guards against is a
	// body that arrived compressed and was read anyway. Asserting against a
	// hand-written byte slice would prove nothing.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(good); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := buf.Bytes()

	cases := []struct {
		name string
		body []byte
		want error
	}{
		{"a real page", good, nil},
		{"empty", nil, ErrEmptyBody},
		{"invalid utf-8", []byte{0xff, 0xfe, 0xfd, 0xfc}, ErrNotUTF8},
		{"valid utf-8 with no Cyrillic is not an Avito page", []byte("<html>hello</html>"), ErrNoCyrillic},
		// The one that matters. A gzip stream is usually invalid UTF-8, and
		// when it happens not to be it still has no Cyrillic — so the two
		// checks together catch it either way.
		{"still compressed", compressed, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Document{Body: c.body}.Check()
			if c.name == "still compressed" {
				if err == nil {
					t.Fatal("a compressed body passed Check; it would read " +
						"downstream as 'the query matched nothing'")
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Check() = %v, want %v", err, c.want)
			}
		})
	}
}
