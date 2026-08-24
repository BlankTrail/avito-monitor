// SPDX-License-Identifier: AGPL-3.0-or-later

package bt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/testutil/fakebt"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// TestLeaserSatisfiesTheSeam is a compile-time assertion written as a test so
// that its failure names the method that is missing.
func TestLeaserSatisfiesTheSeam(t *testing.T) {
	var _ transport.Leaser = (*Leaser)(nil)
	var _ transport.Lease = (*portLease)(nil)
}

func open(t *testing.T, srv *fakebt.Server, surface avito.Surface, ports int) *Leaser {
	t.Helper()
	l, err := Open(context.Background(), Config{
		ControlURL: srv.URL(), APIKey: srv.Key(), Ports: ports,
		Surface: surface, Insecure: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// openBodies returns the decoded bodies of every ports/open call.
func openBodies(t *testing.T, srv *fakebt.Server) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range srv.Requests() {
		if rec.Path != "/api/v1/ports/open" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(rec.Body), &m); err != nil {
			t.Fatalf("decode open body %q: %v", rec.Body, err)
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		t.Fatal("no port was opened")
	}
	return out
}

func TestOpenLeasesAPortAndReportsItsCaps(t *testing.T) {
	srv := fakebt.New(t)
	l := open(t, srv, avito.SurfaceMobile, 2)

	if l.Mode() != transport.ModeBlankTrail {
		t.Errorf("Mode = %q", l.Mode())
	}
	if !l.Caps().SolvesChallenges {
		t.Error("SolvesChallenges = false; this is the mode that solves them, " +
			"and it is what the product is priced on")
	}

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()
	if lease.Session() == "" {
		t.Error("Session is empty; it is what a caller keys a visit on")
	}
	if lease.Label() == "" {
		t.Error("Label is empty; it is what a person reads in a log line")
	}
	if !lease.Profile().OwnedByProxy {
		t.Error("Profile.OwnedByProxy = false in BlankTrail mode; the proxy " +
			"rewrites User-Agent and sec-ch-ua to match its own handshake, and " +
			"a second opinion from here contradicts it")
	}
}

// TestPortsAreOpenedAsHTTPConnect is not a style choice. Go's http.Transport
// resolves a socks5:// proxy's hostname LOCALLY and there is no socks5h, so the
// proxy's MITM never sees the name it needs for SNI and the certificate it
// mints — the handshake dies as an EOF. CONNECT carries the hostname.
func TestPortsAreOpenedAsHTTPConnect(t *testing.T) {
	srv := fakebt.New(t)
	open(t, srv, avito.SurfaceMobile, 1)
	for _, body := range openBodies(t, srv) {
		if got := body["protocol"]; got != "http" {
			t.Errorf("protocol = %v, want http — Go cannot use a socks5 port "+
				"against a MITM that needs the hostname", got)
		}
	}
}

// TestCaptchaPolicyIsSent proves the one SDK extension is actually wired, not
// merely available.
func TestCaptchaPolicyIsSent(t *testing.T) {
	srv := fakebt.New(t)
	open(t, srv, avito.SurfaceMobile, 1)
	for _, body := range openBodies(t, srv) {
		if body["captcha_action"] != "rotate_retry" {
			t.Errorf("captcha_action = %v, want rotate_retry", body["captcha_action"])
		}
		if body["captcha_max_attempts"] != float64(3) {
			t.Errorf("captcha_max_attempts = %v, want 3", body["captcha_max_attempts"])
		}
	}
}

// TestSurfaceIsAPortProfileDecision. Measured: BlankTrail replaces the
// User-Agent with its port profile's own, so an iPhone UA through a Chrome port
// still gets the desktop site. The surface is chosen by opening the port with
// an iOS profile, not by setting a header.
func TestSurfaceIsAPortProfileDecision(t *testing.T) {
	for _, c := range []struct {
		surface   avito.Surface
		os, brows string
	}{
		{avito.SurfaceMobile, "ios", "safari"},
		{avito.SurfaceDesktop, "windows", "chrome"},
	} {
		t.Run(string(c.surface), func(t *testing.T) {
			srv := fakebt.New(t)
			open(t, srv, c.surface, 1)
			for _, body := range openBodies(t, srv) {
				if body["os"] != c.os || body["browser"] != c.brows {
					t.Errorf("os/browser = %v/%v, want %s/%s",
						body["os"], body["browser"], c.os, c.brows)
				}
			}
		})
	}
}

// TestRotationIsReachableFromOneStatusOnly. CountFailure is where "rotate on
// 429 only" is encoded, at the layer that owns rotation, so no ladder above can
// override it however wrong it gets.
func TestRotationIsReachableFromOneStatusOnly(t *testing.T) {
	for status, want := range map[int]bool{429: true, 403: false, 439: false, 500: false, 200: false} {
		if got := CountFailure(status); got != want {
			t.Errorf("CountFailure(%d) = %v, want %v — rotating on anything but "+
				"429 throws away a clearance worth 19-23 seconds", status, got, want)
		}
	}
}

// TestAddressesDoNotMultiplyWithoutChannels. With no upstreams every port
// shares the host's own address, and saying one is what stops a larger pool
// from looking like a larger budget.
func TestAddressesDoNotMultiplyWithoutChannels(t *testing.T) {
	srv := fakebt.New(t)
	l := open(t, srv, avito.SurfaceMobile, 4)
	if got := l.Caps().Addresses; got != 1 {
		t.Errorf("Addresses = %d with four ports and no channels, want 1", got)
	}
	if l.Caps().RotatesEgress {
		t.Error("RotatesEgress = true with nowhere to rotate to")
	}
}

// TestAddressesNeverExceedsTheProxyPool. Ten ports over three proxies is three
// addresses. Counting ports would licence three times the request rate those
// addresses can carry, which is the budget lying in the direction that gets an
// installation banned.
func TestAddressesNeverExceedsTheProxyPool(t *testing.T) {
	cases := []struct {
		ports, pool int
		rotates     bool
		want        int
	}{
		{ports: 10, pool: 3, rotates: true, want: 3},
		{ports: 2, pool: 8, rotates: true, want: 2},
		{ports: 4, pool: 4, rotates: true, want: 4},
		{ports: 4, pool: 0, rotates: true, want: 4}, // pool unknown: trust ports
		{ports: 10, pool: 3, rotates: false, want: 1},
	}
	for _, c := range cases {
		if got := addresses(c.ports, c.pool, c.rotates); got != c.want {
			t.Errorf("addresses(ports=%d, pool=%d, rotates=%v) = %d; want %d",
				c.ports, c.pool, c.rotates, got, c.want)
		}
	}
}
