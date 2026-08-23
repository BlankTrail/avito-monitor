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
	"crypto/x509"
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
	CA       *x509.CertPool
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
	return &Leaser{
		pool: pool,
		caps: transport.Caps{
			SolvesChallenges: true,
			RotatesEgress:    rotates,
			Addresses:        addresses(cfg.Ports, rotates),
		},
		prof: transport.Profile{OwnedByProxy: true, Surface: cfg.Surface},
	}, nil
}

// addresses is how many distinct egresses the budget may multiply by. With no
// channels configured every port shares the host's own address, and saying so
// is what stops a larger pool from looking like a larger budget.
func addresses(ports int, rotates bool) int {
	if !rotates {
		return 1
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
	return &portLease{Lease: lease, prof: l.prof}, nil
}

// portLease adds the two seam methods the SDK does not have. Everything else —
// Do, Session, RotateEgress, RenewIdentity, Release — is the SDK's own, which
// is why this file is short.
type portLease struct {
	*blanktrail.Lease
	prof transport.Profile
}

func (p *portLease) Label() string { return "blanktrail:" + strconv.Itoa(p.Port()) }

func (p *portLease) Profile() transport.Profile { return p.prof }

// Warm is always true here. The proxy owns the session and its cookie jar, and
// a port that has just been opened has already done whatever warming the
// target needs — the cold state this reports on is a property of a jar this
// program manages, and in this mode it manages none.
func (p *portLease) Warm() bool { return true }
