// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decodingTransport decodes the body itself, because nothing else will.
//
// This is not an optimisation, it is the difference between working and
// silently not. Go's transports decompress gzip automatically only when they
// set Accept-Encoding themselves; a client that sets the header explicitly —
// which this one must, because Chrome's exact set is part of the fingerprint —
// gets the raw bytes back. And br and zstd are never handled at all.
//
// The failure mode of getting this wrong is the one the whole design is shaped
// around: an undecoded body carries no wall markers and no listings, so a block
// reads as a healthy 200 and a parse failure reads as an empty market. It was
// caught here by avito.Document.Check refusing a body that is not valid UTF-8 —
// on the very first live request this package ever made.
type decodingTransport struct{ base http.RoundTripper }

func (d decodingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := d.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if enc == "" || enc == "identity" {
		return resp, nil
	}
	body, err := decoder(enc, resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("direct: decode %s: %w", enc, err)
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{Reader: body, Closer: resp.Body}
	// The body no longer matches the header, and a reader that trusted either
	// would be wrong about the other.
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return resp, nil
}

// decoder returns a reader for one content coding. An unknown coding is an
// error rather than a pass-through: handing an undecoded body onward is exactly
// the silent failure this file exists to prevent, and we only ever advertise
// codings we can read.
func decoder(enc string, r io.Reader) (io.Reader, error) {
	switch enc {
	case "gzip", "x-gzip":
		return gzip.NewReader(r)
	case "deflate":
		return flate.NewReader(r), nil
	case "br":
		return brotli.NewReader(r), nil
	case "zstd":
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	default:
		return nil, fmt.Errorf("unadvertised content coding %q", enc)
	}
}
