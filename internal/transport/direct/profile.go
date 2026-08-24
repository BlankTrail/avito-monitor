// SPDX-License-Identifier: AGPL-3.0-or-later

// Package direct is the pure-Go half of the transport seam: a client that
// genuinely works against this target without a proxy, and that stops loudly at
// the one boundary it cannot cross.
package direct

import (
	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	utls "github.com/refraction-networking/utls"
)

// Fingerprint binds one browser's TLS hello to the headers that go with it.
//
// They are one fact and this struct exists so they cannot be set apart. A
// client whose headers say Chrome 133 and whose handshake says Chrome 96 is a
// stronger signal than either would have been alone.
type Fingerprint struct {
	Name           string
	HelloID        utls.ClientHelloID
	UserAgent      string
	SecCHUA        string
	Platform       string
	AcceptEncoding string
	Surface        avito.Surface
}

// Profile is the header half, for the seam.
func (f Fingerprint) Profile() transport.Profile {
	return transport.Profile{
		UserAgent:      f.UserAgent,
		SecCHUA:        f.SecCHUA,
		Platform:       f.Platform,
		AcceptEncoding: f.AcceptEncoding,
		Surface:        f.Surface,
		OwnedByProxy:   false, // nothing here rewrites our headers
	}
}

// chromeEncoding is Chrome's own set, and this program decodes all four.
// Advertising br and zstd and then failing to decode them is the silent failure
// that reads downstream as "the query matched nothing".
const chromeEncoding = "gzip, deflate, br, zstd"

// chromeMajor is the version this package presents, and it is capped by what
// utls ships rather than by what Chrome has released.
//
// This is a real and unfixable gap in mode (b), and it is written down rather
// than left to be discovered. A live BlankTrail port was measured presenting
// Chrome_151_win; utls's newest Chrome hello is 133. Direct mode therefore
// cannot present a current browser at all. Claiming 151 in the User-Agent over
// a 133 handshake would be worse — an internal contradiction is a sharper
// signal than an old browser — so the version claimed here matches the
// handshake exactly, and the cost is that this client looks like somebody who
// has not updated Chrome in a while. That is a thing real people are; a client
// whose headers and handshake disagree is not.
const chromeMajor = "133"

// ChromeWindows is the desktop identity.
//
// Chrome and not Safari, despite BlankTrail presenting iOS successfully:
// BlankTrail drives a real browser and can. x/net/http2 emits pseudo-headers in
// the order :authority, :method, :path, :scheme — Chrome's, and neither
// Firefox's nor Safari's — and offers no hook to change it. A Safari handshake
// over Chrome's framing is a self-contradiction, and worse than an honest
// Chrome.
func ChromeWindows() Fingerprint {
	return Fingerprint{
		Name:    "chrome-" + chromeMajor + "-win",
		HelloID: utls.HelloChrome_133,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/" + chromeMajor + ".0.0.0 Safari/537.36",
		SecCHUA: `"Google Chrome";v="` + chromeMajor + `", "Chromium";v="` +
			chromeMajor + `", "Not_A Brand";v="24"`,
		Platform:       `"Windows"`,
		AcceptEncoding: chromeEncoding,
		Surface:        avito.SurfaceDesktop,
	}
}

// ChromeAndroid is the mobile identity, and the untested one.
//
// Every mobile page in the measurement set arrived through a BlankTrail iOS
// port, so whether a utls client sending a mobile header set is served
// m.avito.ru or redirected to www is unverified — and it decides whether mode
// (b) gets the cheap surface at all. If it does not, direct mode is
// desktop-only, which doubles its request cost and halves the number of
// watches it can carry. It is the first thing the soak run answers.
func ChromeAndroid() Fingerprint {
	return Fingerprint{
		Name:    "chrome-" + chromeMajor + "-android",
		HelloID: utls.HelloChrome_133,
		UserAgent: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/" + chromeMajor + ".0.0.0 Mobile Safari/537.36",
		SecCHUA: `"Google Chrome";v="` + chromeMajor + `", "Chromium";v="` +
			chromeMajor + `", "Not_A Brand";v="24"`,
		Platform:       `"Android"`,
		AcceptEncoding: chromeEncoding,
		Surface:        avito.SurfaceMobile,
	}
}

// FingerprintFor picks the identity that matches a surface.
func FingerprintFor(s avito.Surface) Fingerprint {
	if s == avito.SurfaceMobile {
		return ChromeAndroid()
	}
	return ChromeWindows()
}
