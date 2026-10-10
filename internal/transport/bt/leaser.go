// SPDX-License-Identifier: AGPL-3.0-or-later

// Package bt is the BlankTrail half of the transport seam.
//
// Almost nothing here is new. The SDK already leases the most-rested port and
// already refuses to hand one back before its cooldown has elapsed; this
// package configures that for this target and wraps a lease so it satisfies the
// seam. What it must get right is the configuration, because three of those
// settings are the difference between a working monitor and one that pays for a
// challenge solve and then throws it away.
package bt

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// CountFailure is handed to the pool and is where "rotate the egress on 429
// only" is encoded — at the layer that owns rotation, so that no ladder above
// can rotate on a 403 or a 439 however wrong it gets.
//
// A Qrator clearance is domain-wide, long-lived, costs nineteen to twenty-three
// seconds and is destroyed by any upstream change. A 403 says this identity is
// finished; a 439 says solve a challenge. Neither is fixed by a new address,
// and both are made worse by throwing away a clearance that was working.
func CountFailure(status int) bool { return status == 429 }

// DefaultPace is 3.5 seconds between two requests on the same port: the
// measurement was forty of forty at three-second spacing, and a default should
// not sit on the edge of what worked.
const DefaultPace = 3500 * time.Millisecond

// Config is what opening a pool needs.
type Config struct {
	ControlURL string
	APIKey     string
	// Ports is how many identities to hold. Throughput scales with ports and
	// never with goroutines: one solve per (port, host), and parallel requests
	// to one host through one port coalesce onto a single solve.
	Ports    int
	Surface  avito.Surface
	Channels []blanktrail.Channel
	// Addresses is how many distinct egress addresses the channels can produce.
	// It bounds the budget: a pool of ten ports over three proxies is three
	// addresses, not ten, and a budget that multiplied by ten would licence
	// three times the request rate the addresses can carry.
	Addresses int
	CA        *x509.CertPool
	// Insecure skips CA verification. Tests only: the proxy re-signs origins
	// with its own MITM CA, and production pins it through Preflight.
	Insecure bool
	// Pace is the floor between two requests on the SAME port. The pool derives
	// its cooldown from it, so N ports serve N times as fast while each
	// identity still rests the full interval.
	Pace time.Duration
}

// Leaser hands out BlankTrail ports as transport leases.
type Leaser struct {
	pool *blanktrail.Pool
	caps transport.Caps
	prof transport.Profile

	// origin distinguishes this pool's identities from those of any other
	// pool, including the one this process had before it restarted.
	//
	// The SDK spells a session "port#counter", and the counter starts at zero
	// every time a pool is opened. Two genuinely different identities on port
	// 20000 therefore both record "20000#0" — which was measured happening:
	// two runs minutes apart wrote the same session string and shared eight of
	// thirty listings, while a truly stable identity shares thirty of thirty.
	//
	// The seam's contract is that Session changes exactly when the identity
	// does. Reopening a port mints a new identity at the proxy, so the string
	// has to move with it.
	origin string
}

// Open builds the pool.
func Open(ctx context.Context, cfg Config) (*Leaser, error) {
	if cfg.Ports <= 0 {
		cfg.Ports = 2
	}
	if cfg.Pace <= 0 {
		cfg.Pace = DefaultPace
	}
	cl, err := blanktrail.NewClient(cfg.ControlURL, cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("bt: control client: %w", err)
	}
	channels := cfg.Channels
	rotates := len(channels) > 0
	if len(channels) == 0 {
		channels = []blanktrail.Channel{blanktrail.NewDirectChannel("direct")}
	}
	pool, err := blanktrail.NewPool(ctx, blanktrail.PoolConfig{
		Client:         cl,
		Threads:        1,
		PortsPerThread: cfg.Ports,
		Spec:           Spec(cfg.Surface),
		Channels:       channels,
		CA:             cfg.CA,
		Insecure:       cfg.Insecure,
		DelayMin:       cfg.Pace,
		DelayMax:       cfg.Pace,
		// Rotation is caused, never scheduled — and this is the layer that owns
		// it, so the setting cannot be overridden from above.
		CountFailure: CountFailure,
		// Proactive renewal off. Renewing an identity discards a clearance that
		// cost nineteen to twenty-three seconds and is domain-wide and
		// long-lived, so it happens when something asks for it and not on a
		// schedule.
		RenewAfterRequests: 0,
		RenewAfterInterval: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("bt: open pool: %w", err)
	}
	origin := make([]byte, 6)
	if _, err := rand.Read(origin); err != nil {
		return nil, fmt.Errorf("bt: identity origin: %w", err)
	}
	return &Leaser{
		origin: hex.EncodeToString(origin),
		pool:   pool,
		caps: transport.Caps{
			SolvesChallenges: true,
			RotatesEgress:    rotates,
			Addresses:        addresses(cfg.Ports, cfg.Addresses, rotates),
			Exits:            cfg.Addresses,
		},
		prof: transport.Profile{OwnedByProxy: true, Surface: cfg.Surface},
	}, nil
}

// addresses is how many distinct egresses the budget may multiply by.
//
// Two ways to get it wrong, and both inflate the figure a person is shown. With
// no channels configured every port shares the host's own address, so a larger
// pool is not a larger budget. And with a proxy list SHORTER than the pool,
// ports double up on addresses — ten ports over three proxies is three, and the
// smaller of the two is the honest answer.
func addresses(ports, pool int, rotates bool) int {
	if !rotates {
		return 1
	}
	if pool > 0 && pool < ports {
		return pool
	}
	return ports
}

// Spec is where the surface is decided, and it is a port-profile decision
// rather than a header one. Measured: BlankTrail replaces the User-Agent with
// its port profile's own, so an iPhone UA through a Chrome/Windows port is
// still served the desktop site.
func Spec(surface avito.Surface) blanktrail.PortSpec {
	s := blanktrail.DefaultPortSpec()
	// HTTP CONNECT and not SOCKS. Go's http.Transport resolves a socks5://
	// proxy's hostname LOCALLY and has no socks5h, so the proxy's MITM never
	// sees the name it needs for SNI and the certificate it mints, and the
	// handshake dies as an EOF. CONNECT carries the hostname.
	s.Protocol = "http"
	s.JSSolver = true
	s.KeepSessions = true
	s.Decompress = true
	s.H2Spoofing = true
	s.SpoofHeaders = true
	s.SpoofUserAgent = true
	// The one SDK extension, and the reason it was made: a live port reports
	// both of these and the policy matters most on the target whose paid mode
	// exists to pass an interactive challenge.
	s.CaptchaAction = "rotate_retry"
	s.CaptchaMaxAttempts = 3
	if surface == avito.SurfaceMobile {
		s.OS, s.Browser = "ios", "safari"
	} else {
		s.OS, s.Browser = "windows", "chrome"
	}
	return s
}

func (l *Leaser) Mode() transport.Mode { return transport.ModeBlankTrail }
func (l *Leaser) Caps() transport.Caps { return l.caps }
func (l *Leaser) Close() error         { return l.pool.Close() }

// Acquire blocks until a port has rested and returns the one that rested
// longest. Pool.take does the choosing; this only wraps what it hands back.
func (l *Leaser) Acquire(ctx context.Context) (transport.Lease, error) {
	lease, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	return &portLease{Lease: lease, prof: l.prof, origin: l.origin}, nil
}

// portLease adds the two seam methods the SDK does not have. Everything else —
// Do, Session, RotateEgress, RenewIdentity, Release — is the SDK's own, which
// is why this file is short.
type portLease struct {
	*blanktrail.Lease
	prof   transport.Profile
	origin string
}

// Session is the SDK's, prefixed by this pool's origin.
//
// Without the prefix the string repeats across restarts — the SDK counts from
// zero each time a pool opens — and a caller keying a visit on it would treat
// two different identities as one. Measured: that is not hypothetical, and the
// two identities in question saw eight of thirty listings in common.
func (p *portLease) Session() string { return p.origin + "/" + p.Lease.Session() }

func (p *portLease) Label() string { return "blanktrail:" + strconv.Itoa(p.Port()) }

// Egress is the exit address, which is not the port. With no channels
// configured every port leaves through the host's own IP and they all report
// "direct" — which is the point: the per-address budget must see one address,
// not one per port.
func (p *portLease) Egress() string { return p.Lease.Egress().String() }

func (p *portLease) Profile() transport.Profile { return p.prof }

// Warm is always true here. The proxy owns the session and its cookie jar, and
// a port that has just been opened has already done whatever warming the
// target needs — the cold state this reports on is a property of a jar this
// program manages, and in this mode it manages none.
func (p *portLease) Warm() bool { return true }

// BlamesExit reads the port's refusal, when the error is one.
func (l *portLease) BlamesExit(err error) bool {
	ref, ok := blanktrail.Refusal(err)
	return ok && ref.BlamesExit()
}
