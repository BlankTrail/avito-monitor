// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// DefaultCookieHost is the host tree a jar is scoped to unless a caller says
// otherwise. Scoping at construction is what makes publicsuffix unnecessary and
// what guarantees no cookie from this target reaches anywhere else.
const DefaultCookieHost = "avito.ru"

// Config is what opening a direct Leaser needs.
type Config struct {
	Surface avito.Surface
	// Sessions is how many identities to hold. Two requests on two jars share
	// nothing, so this is where mode (b) gets its throughput — never from
	// concurrency on one identity.
	Sessions int
	// Root is the URL a warm-up navigates to in order to mint an identity.
	Root string
	// Upstreams are the egress addresses, one per session where there are
	// enough. Empty means the host's own address for everyone.
	Upstreams []string
	// StatePath is where the identities are persisted. Empty means they are
	// not, and a restart then starts cold.
	StatePath string
	Roots     *x509.CertPool
	// CookieHost is the host tree the jars are scoped to. It defaults to
	// avito.ru and exists as a field because a test origin necessarily lives
	// somewhere else — a jar scoped to avito.ru silently discards every cookie
	// a loopback server sets, which is the jar behaving correctly and the test
	// setup being wrong.
	CookieHost string
	// Plain makes the sessions speak cleartext HTTP/1.1. Tests against a
	// loopback httptest server only; it is a Go client with no fingerprint,
	// which is the state this package exists to leave.
	Plain bool
	Now   func() time.Time
}

// Leaser hands out direct sessions as transport leases.
type Leaser struct {
	mu       sync.Mutex
	cfg      Config
	sessions []*session
	caps     transport.Caps
	now      func() time.Time
}

// state is the persisted form: the jars, and which address each was using.
type state struct {
	Jars    []JarState `json:"jars"`
	Egress  []string   `json:"egress"`
	Written time.Time  `json:"written"`
}

// Open builds the sessions and restores any that were saved.
func Open(cfg Config) (*Leaser, error) {
	if cfg.Sessions <= 0 {
		cfg.Sessions = 1
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CookieHost == "" {
		cfg.CookieHost = DefaultCookieHost
	}
	fp := FingerprintFor(cfg.Surface)
	l := &Leaser{cfg: cfg, now: cfg.Now}

	saved := load(cfg.StatePath)
	for i := 0; i < cfg.Sessions; i++ {
		jar := NewJar(cfg.CookieHost)
		egress := egressFor(cfg.Upstreams, i)
		restored := false
		if i < len(saved.Jars) && saved.Jars[i].Host == cfg.CookieHost {
			jar = RestoreJar(saved.Jars[i])
			restored = jar.Identity() != ""
			if i < len(saved.Egress) && saved.Egress[i] != "" {
				egress = saved.Egress[i]
			}
		}
		s := newSession(i, fp, jar, egress, cfg.Roots, cfg.Plain)
		s.restored = restored
		l.sessions = append(l.sessions, s)
	}
	l.caps = transport.Caps{
		// The field the whole product is priced on. POST
		// /web/N/firewallCaptcha/verify needs an X-Cube header carrying a WebGL
		// proof of work, which nothing here can produce, and a ladder told
		// otherwise would retry something that cannot succeed.
		SolvesChallenges: false,
		RotatesEgress:    len(cfg.Upstreams) > 1,
		Addresses:        addressCount(cfg.Upstreams),
		Exits:            len(cfg.Upstreams),
	}
	return l, nil
}

func egressFor(upstreams []string, i int) string {
	if len(upstreams) == 0 {
		return "" // the host's own address
	}
	return upstreams[i%len(upstreams)]
}

func addressCount(upstreams []string) int {
	if len(upstreams) == 0 {
		return 1
	}
	seen := map[string]bool{}
	for _, u := range upstreams {
		seen[u] = true
	}
	return len(seen)
}

func (l *Leaser) Mode() transport.Mode { return transport.ModeDirect }
func (l *Leaser) Caps() transport.Caps { return l.caps }

// Close saves the identities. Losing srv_id costs a cold start on every
// session, and the cold state is what the whole design exists to leave.
func (l *Leaser) Close() error { return l.Save() }

// Acquire returns the identity that has rested longest, warmed and ready.
//
// The selection deliberately mirrors blanktrail.Pool.take: skip the leased,
// take the one used longest ago. The two modes must behave the same way from
// the ladder's point of view, and the SDK's version is the one that has been in
// production.
func (l *Leaser) Acquire(ctx context.Context) (transport.Lease, error) {
	s, err := l.take(ctx)
	if err != nil {
		return nil, err
	}
	if s.needsWarmUp() && l.cfg.Root != "" {
		if err := l.warmOnSomeExit(ctx, s); err != nil {
			l.give(s)
			return nil, err
		}
	}
	return &sessionLease{s: s, l: l}, nil
}

// maxExitTries bounds how many configured addresses one warm-up may walk.
//
// Enough to get past the bad stretch of a real list and no more. Measured on
// 11 October 2026 against a provider's list of fifteen thousand: four exits in
// five presented a forged *.avito.ru certificate signed by «None, LLC, Dallas»,
// so the expected walk to a clean one is a handful, and walking all fifteen
// thousand on one Acquire would be a scan nobody asked for.
const maxExitTries = 12

// warmOnSomeExit warms the session up, and if the exit it is on cannot carry
// the request at all, moves to the next configured address and tries there.
//
// Before this a single bad address killed collection: the warm-up runs before
// the ladder, its error went straight back to the pass, and the ladder's own
// rotation never got a chance to run. On the list above that was four passes in
// five failing on «certificate signed by unknown authority» — correctly refused,
// and then never retried anywhere else.
//
// Every warm-up failure is treated as the exit's fault, and that is not a
// guess: the warm-up is a plain navigation to the site's front page, which the
// site answers on every address that can reach it. What does not come back is
// a dial that failed, a proxy that refused, or a certificate that was not the
// site's — all three properties of the exit. With one address there is nowhere
// to move and the error stands as it did.
func (l *Leaser) warmOnSomeExit(ctx context.Context, s *session) error {
	err := s.warmUp(ctx, l.cfg.Root)
	if err == nil || len(l.cfg.Upstreams) < 2 {
		return err
	}
	tries := min(len(l.cfg.Upstreams)-1, maxExitTries)
	for i := 0; i < tries && err != nil; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lease := &sessionLease{s: s, l: l}
		if rerr := lease.RotateEgress(ctx); rerr != nil {
			return err
		}
		err = s.warmUp(ctx, l.cfg.Root)
	}
	if err != nil {
		return fmt.Errorf("%w (tried %d exits)", err, tries+1)
	}
	return nil
}

func (l *Leaser) take(ctx context.Context) (*session, error) {
	for {
		l.mu.Lock()
		var best *session
		for _, s := range l.sessions {
			if s.leased {
				continue
			}
			if best == nil || s.lastUse.Before(best.lastUse) {
				best = s
			}
		}
		if best != nil {
			best.leased = true
			best.lastUse = l.now()
			l.mu.Unlock()
			return best, nil
		}
		l.mu.Unlock()
		// Every identity is in use. The pacer, not this loop, is what enforces
		// the gap between requests; this only waits for one to come free.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (l *Leaser) give(s *session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s.leased = false
	s.lastUse = l.now()
}

// Save writes the identities to disk, through a temporary file and a rename,
// so a crash mid-write cannot leave a half-file where the identity used to be.
func (l *Leaser) Save() error {
	if l.cfg.StatePath == "" {
		return nil
	}
	l.mu.Lock()
	st := state{Written: l.now()}
	for _, s := range l.sessions {
		st.Jars = append(st.Jars, s.jar.Snapshot())
		st.Egress = append(st.Egress, s.egress)
	}
	l.mu.Unlock()

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.cfg.StatePath), 0o755); err != nil {
		return err
	}
	tmp := l.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.cfg.StatePath)
}

func load(path string) state {
	var st state
	if path == "" {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

// sessionLease is one identity, held.
type sessionLease struct {
	s *session
	l *Leaser
}

func (x *sessionLease) Session() string            { return x.s.Session() }
func (x *sessionLease) Label() string              { return x.s.Label() }
func (x *sessionLease) Profile() transport.Profile { return x.s.fp.Profile() }

// Warm is what the classifier reads to tell a cold start from a throttle.
func (x *sessionLease) Warm() bool {
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	return x.s.warm
}

// Do takes the session's mutex for the whole request.
//
// One request in flight per identity, and not one per process. Two concurrent
// requests on one jar necessarily send the same nonce, and nonce reuse is
// exactly what a nonce detects; two requests on two jars share nothing and are
// free. The jar writes the response's cookies back on its own — the nonce is
// reissued on every response, and a client that stored only the first would go
// stale inside a minute.
func (x *sessionLease) Do(req *http.Request) (*http.Response, error) {
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	x.s.fp.Profile().Apply(req)
	return x.s.client.Do(req)
}

// RotateEgress moves to another configured address, keeping the identity.
// With nothing to move to it refuses honestly rather than reporting success,
// because the ladder would otherwise believe a remedy had been applied.
func (x *sessionLease) RotateEgress(ctx context.Context) error {
	ups := x.l.cfg.Upstreams
	if len(ups) < 2 {
		return fmt.Errorf("direct: rotate %s: %w", x.s.Label(), ErrNoOtherAddress)
	}
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	for i, u := range ups {
		if u == x.s.egress {
			x.s.egress = ups[(i+1)%len(ups)]
			x.s.rebuildClient()
			return nil
		}
	}
	x.s.egress = ups[0]
	// The client must be rebuilt, not merely the field reassigned. The egress
	// is captured in the transport's dial closure, and a pooled connection
	// still runs over the OLD address — so a rotation without this line changes
	// a log label and nothing else, which is the failure mode the ladder can
	// least afford to be lied to about.
	x.s.rebuildClient()
	return nil
}

// RenewIdentity replaces the fingerprint's jar and the session string, keeping
// the address. It is the remedy for a 403 and for a soft block.
func (x *sessionLease) RenewIdentity(ctx context.Context) error {
	x.s.renew()
	if x.l.cfg.Root == "" {
		return nil
	}
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	return x.s.warmUp(ctx, x.l.cfg.Root)
}

// Egress is the exit address, which is not the session. Sessions with no
// upstream all leave through the host's own IP and all report "direct": the
// per-address budget must count one address there, not one per jar.
func (x *sessionLease) Egress() string {
	if x.s.egress == "" {
		return "direct"
	}
	return x.s.egress
}

// Recycle replaces everything about this endpoint except the address: the
// fingerprint, the jar, the session and the connections. It is what this mode
// has in place of closing a port, and it is the remedy for a soft block.
func (x *sessionLease) Recycle(ctx context.Context) error {
	x.s.recycle(FingerprintFor(x.l.cfg.Surface))
	if x.l.cfg.Root == "" {
		return nil
	}
	x.s.mu.Lock()
	defer x.s.mu.Unlock()
	return x.s.warmUp(ctx, x.l.cfg.Root)
}

func (x *sessionLease) Release() { x.l.give(x.s) }
