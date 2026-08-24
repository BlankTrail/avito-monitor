// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// payload carries Cyrillic on purpose: an undecoded body is not valid UTF-8,
// and that is precisely how this defect was found on the live site.
const payload = "<html><body>Объявления, 41988 найдено</body></html>"

func compress(t *testing.T, coding, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch coding {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "deflate":
		fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		w = fw
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w = zw
	default:
		t.Fatalf("no writer for %q", coding)
	}
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDecodesEveryAdvertisedCoding is the regression for the defect that made
// the first live request of this package return "body is not valid UTF-8".
//
// Chrome advertises four codings and so must we, because the header is part of
// the fingerprint. Go decompresses gzip only when it sets Accept-Encoding
// itself, which this client never lets it do, and it has never handled br or
// zstd at all. Every coding we advertise, we decode here or nowhere.
func TestDecodesEveryAdvertisedCoding(t *testing.T) {
	for _, coding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(coding, func(t *testing.T) {
			body := compress(t, coding, payload)
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", coding)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(body)
			}))
			defer origin.Close()

			c := &http.Client{Transport: decodingTransport{base: newPlainTransport()}}
			resp, err := c.Get(origin.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != payload {
				t.Errorf("body = %q; want the decoded page", got)
			}
			// A reader downstream must not be told the body is still coded.
			if h := resp.Header.Get("Content-Encoding"); h != "" {
				t.Errorf("Content-Encoding = %q after decoding; a caller that "+
					"believed it would decode twice", h)
			}
			if !resp.Uncompressed {
				t.Error("Uncompressed = false, but we decompressed it")
			}
		})
	}
}

// TestIdentityPassesThroughUntouched: the common case must not be wrapped in a
// decoder that has nothing to do.
func TestIdentityPassesThroughUntouched(t *testing.T) {
	for _, header := range []string{"", "identity"} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Content-Encoding", header)
			}
			io.WriteString(w, payload)
		}))
		c := &http.Client{Transport: decodingTransport{base: newPlainTransport()}}
		resp, err := c.Get(origin.URL)
		if err != nil {
			origin.Close()
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		origin.Close()
		if string(got) != payload {
			t.Errorf("Content-Encoding %q: body = %q; want it untouched", header, got)
		}
	}
}

// TestUnknownCodingIsAnError: we only ever advertise four, so a fifth coming
// back means something is wrong. Passing the bytes on undecoded is the silent
// failure this file exists to prevent — the caller would read a healthy 200
// carrying an unreadable body and call the market empty.
func TestUnknownCodingIsAnError(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "exi")
		io.WriteString(w, payload)
	}))
	defer origin.Close()

	c := &http.Client{Transport: decodingTransport{base: newPlainTransport()}}
	resp, err := c.Get(origin.URL) //nolint:bodyclose // there is no body on the error path
	if err == nil {
		resp.Body.Close()
		t.Fatal("an unadvertised coding was accepted; it must be refused")
	}
}

// TestSessionDecodesThroughTheLeaser proves the decoder is actually wired into
// the transport a lease uses, not merely present in the package. The live
// defect was exactly this gap: the dependencies were vendored and unused.
func TestSessionDecodesThroughTheLeaser(t *testing.T) {
	body := compress(t, "br", payload)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "br")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(body)
	}))
	defer origin.Close()

	l, err := Open(Config{
		Surface: avito.SurfaceMobile, Sessions: 1, Plain: true,
		CookieHost: hostOf(t, origin.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	req, err := http.NewRequest(http.MethodGet, origin.URL+"/all?q=x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := lease.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("body through a lease = %q; want it decoded", got)
	}
	// And the guard that caught this on the live site agrees it is readable.
	if err := (avito.Document{
		Surface: avito.SurfaceMobile, URL: origin.URL, Body: got,
	}).Check(); err != nil {
		t.Errorf("Document.Check on a decoded body: %v", err)
	}
}
