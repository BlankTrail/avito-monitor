// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"net/http"
	"testing"
)

// TestAcceptLanguageIsOursInBothModes pins the one header that belongs to this
// program whichever mode is running. BlankTrail overrides User-Agent and
// sec-ch-ua with whatever its solver browser presents, and it passes
// Accept-Language through verbatim — so if we do not send it, nobody does, and
// Avito needs it.
func TestAcceptLanguageIsOursInBothModes(t *testing.T) {
	for _, p := range []Profile{
		{OwnedByProxy: true},
		{OwnedByProxy: false, UserAgent: "UA", SecCHUA: `"Chromium";v="131"`},
	} {
		req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
		if err != nil {
			t.Fatal(err)
		}
		p.Apply(req)
		if got := req.Header.Get("Accept-Language"); got != AcceptLanguage {
			t.Errorf("OwnedByProxy=%v: Accept-Language = %q, want %q",
				p.OwnedByProxy, got, AcceptLanguage)
		}
	}
}

// TestProxyOwnedProfileSetsNoIdentityHeaders is the header-ownership rule made
// executable. In BlankTrail mode the proxy rewrites User-Agent and sec-ch-ua to
// match the TLS handshake it is presenting; a second opinion from here would
// contradict the handshake underneath, which is worse than sending nothing.
func TestProxyOwnedProfileSetsNoIdentityHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	Profile{OwnedByProxy: true, UserAgent: "should-not-be-sent", SecCHUA: "nor-this"}.Apply(req)
	for _, h := range []string{"User-Agent", "sec-ch-ua", "sec-ch-ua-platform"} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("%s = %q; the proxy owns it and our value would contradict "+
				"the TLS handshake it is presenting", h, got)
		}
	}
}

// TestDirectProfileSetsTheWholeSet is the mirror. In direct mode nothing
// rewrites anything, so a missing header IS the fingerprint.
func TestDirectProfileSetsTheWholeSet(t *testing.T) {
	p := Profile{
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0 Safari/537.36",
		SecCHUA:        `"Chromium";v="131", "Not_A Brand";v="24", "Google Chrome";v="131"`,
		Platform:       `"Windows"`,
		AcceptEncoding: "gzip, deflate, br, zstd",
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Apply(req)
	want := map[string]string{
		"User-Agent":         p.UserAgent,
		"sec-ch-ua":          p.SecCHUA,
		"sec-ch-ua-platform": p.Platform,
		"sec-ch-ua-mobile":   "?0",
		"Accept-Encoding":    p.AcceptEncoding,
		"Accept-Language":    AcceptLanguage,
	}
	for h, w := range want {
		if got := req.Header.Get(h); got != w {
			t.Errorf("%s = %q, want %q", h, got, w)
		}
	}
}
