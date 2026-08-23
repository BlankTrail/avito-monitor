// SPDX-License-Identifier: AGPL-3.0-or-later

// Package transport owns HTTP and owns being blocked. The avito package owns
// Avito. Neither imports the other's concerns: the extractor says what it saw,
// and this package decides what that means and what to do about it.
package transport

import (
	"net/http"

	"github.com/BlankTrail/avito-monitor/avito"
)

// Mode is which transport is running. It is not a preference: the two differ in
// what they can do, and Caps says so rather than leaving a caller to infer it
// from the mode's name.
type Mode string

const (
	ModeBlankTrail Mode = "blanktrail"
	ModeDirect     Mode = "direct"
)

// Caps is what this transport can actually do, so the ladder above it never has
// to ask which mode it is talking to.
type Caps struct {
	// SolvesChallenges is false for direct mode, and it is the difference the
	// whole product is priced on: POST /web/N/firewallCaptcha/verify needs an
	// X-Cube header carrying a WebGL proof of work, which no HTTP client can
	// produce. A ladder told otherwise would retry something that cannot
	// succeed, spending the budget to arrive at the same wall.
	SolvesChallenges bool
	// RotatesEgress is false when every identity shares the host's own address.
	RotatesEgress bool
	// Addresses is how many distinct egress addresses are configured. It is
	// what the budget arithmetic multiplies, and it is why "I added ports and
	// it got banned faster" has an answer.
	Addresses int
}

// AcceptLanguage is ours in both modes. BlankTrail rewrites User-Agent and
// sec-ch-ua to match the handshake it presents, but it passes this one through
// verbatim — so if this program does not send it, nobody does, and Avito needs
// it. One constant, one test.
const AcceptLanguage = "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"

// Accept is Chrome's own document accept string. It is not identity — nothing
// rewrites it — so it is ours in both modes too.
const Accept = "text/html,application/xhtml+xml,application/xml;q=0.9," +
	"image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"

// Profile is the header-ownership split made into a type rather than a
// convention, because a convention is the kind of thing a well-meaning commit
// forgets.
type Profile struct {
	UserAgent      string
	SecCHUA        string
	Platform       string // sec-ch-ua-platform, quoted as the header wants it
	AcceptEncoding string
	Surface        avito.Surface

	// OwnedByProxy means the transport underneath rewrites the identity
	// headers to match its own TLS handshake. Setting them here would
	// contradict that handshake, which is worse than sending nothing at all.
	OwnedByProxy bool
}

// Apply writes this program's headers onto a request. It never writes an
// identity header when the proxy owns it, and it always writes Accept-Language.
func (p Profile) Apply(req *http.Request) {
	req.Header.Set("Accept", Accept)
	req.Header.Set("Accept-Language", AcceptLanguage)
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	if p.OwnedByProxy {
		return
	}
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
	if p.SecCHUA != "" {
		req.Header.Set("sec-ch-ua", p.SecCHUA)
		req.Header.Set("sec-ch-ua-mobile", mobileHint(p.Surface))
	}
	if p.Platform != "" {
		req.Header.Set("sec-ch-ua-platform", p.Platform)
	}
	if p.AcceptEncoding != "" {
		req.Header.Set("Accept-Encoding", p.AcceptEncoding)
	}
}

func mobileHint(s avito.Surface) string {
	if s == avito.SurfaceMobile {
		return "?1"
	}
	return "?0"
}
