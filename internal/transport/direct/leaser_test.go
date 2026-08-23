// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/testutil/fakeavito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// hostOf is the bare host of a URL, for scoping a jar at a test origin.
func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func TestDirectLeaserSatisfiesTheSeam(t *testing.T) {
	var _ transport.Leaser = (*Leaser)(nil)
	var _ transport.Lease = (*sessionLease)(nil)
}

func openAgainst(t *testing.T, srv *fakeavito.Server, sessions int, statePath string) *Leaser {
	t.Helper()
	cfg := Config{
		Surface: avito.SurfaceMobile, Sessions: sessions,
		StatePath: statePath, Plain: true,
	}
	if srv != nil {
		cfg.Root = srv.URL() + "/"
		// The jar is scoped at construction, and a jar scoped to avito.ru
		// discards every cookie a loopback origin sets — correctly. The test
		// origin is where it is, so the scope follows it.
		cfg.CookieHost = hostOf(t, srv.URL())
	}
	l, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// TestCapsSayWhatDirectModeCannotDo. SolvesChallenges is false and it is the
// field the whole product is priced on: POST /web/N/firewallCaptcha/verify
// needs an X-Cube header carrying a WebGL proof of work, which no HTTP client
// can produce.
func TestCapsSayWhatDirectModeCannotDo(t *testing.T) {
	l := openAgainst(t, nil, 1, "")
	if l.Caps().SolvesChallenges {
		t.Error("SolvesChallenges = true in direct mode; nothing here can " +
			"produce an X-Cube proof of work, and a ladder told otherwise would " +
			"retry something that cannot succeed")
	}
	if l.Mode() != transport.ModeDirect {
		t.Errorf("Mode = %q", l.Mode())
	}
	if l.Caps().Addresses != 1 {
		t.Errorf("Addresses = %d with no upstreams, want 1", l.Caps().Addresses)
	}
}

// TestWarmUpHappensBeforeTheFirstRealRequest. A fresh cookieless request
// collects a 429 immediately; the way out is a root navigation, and Acquire
// must have done it before the caller's first request.
func TestWarmUpHappensBeforeTheFirstRealRequest(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 1, "")

	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	req, err := http.NewRequest(http.MethodGet, srv.URL()+"/all?q=iphone", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := lease.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d after a warm-up, want 200 — Acquire must leave the "+
			"identity warm, or every first request is spent learning that",
			resp.StatusCode)
	}
}

// TestOneRequestInFlightPerSession. Two concurrent requests on one jar
// necessarily send the same nonce, and nonce reuse is exactly what a nonce
// detects. The mutex makes that impossible rather than merely discouraged.
//
// The origin here is a deliberately CONCURRENT one rather than fakeavito.
// fakeavito holds one lock for its whole handler, so it serialises callers by
// itself and this test would pass against it whether or not the session locked
// anything — a test that cannot fail is worse than no test. This server counts
// how many handlers are running at once and holds each one long enough for an
// overlap to be visible.
func TestOneRequestInFlightPerSession(t *testing.T) {
	var inFlight, peak int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	// No Root: this test is about the lock, not about the warm-up, and a
	// warm-up against a server that mints no identity would fail for an
	// unrelated reason.
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

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodGet, origin.URL+"/all?q=x", nil)
			if err != nil {
				return
			}
			if resp, err := lease.Do(req); err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got > 1 {
		t.Errorf("peak concurrent requests on one session = %d, want 1 — two "+
			"requests on one jar send the same nonce, which is exactly what a "+
			"nonce detects", got)
	}
}

// TestTheOriginUsedAboveReallyIsConcurrent. Without this the previous test
// could be passing because the server serialises rather than because the
// session locks, and it would keep passing after the lock was removed.
func TestTheOriginUsedAboveReallyIsConcurrent(t *testing.T) {
	var inFlight, peak int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := http.Get(origin.URL + "/x"); err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&peak) < 2 {
		t.Error("the origin serialised eight plain clients, so the lock test " +
			"above proves nothing about the session's mutex")
	}
}

// TestTwoSessionsDoNotBlockEachOther is the other half. The lock is per
// identity, so mode (b) scales the way mode (a) does: more rested identities.
func TestTwoSessionsDoNotBlockEachOther(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 2, "")
	a, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := l.Acquire(ctx)
	if err != nil {
		t.Fatalf("a second lease could not be acquired while the first was held: %v", err)
	}
	defer b.Release()
	if a.Label() == b.Label() {
		t.Error("two leases are the same identity; they must be two")
	}
}

// TestMostRestedIsLeasedFirst mirrors what the BlankTrail pool does, so the two
// modes behave the same way from the ladder's point of view.
func TestMostRestedIsLeasedFirst(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 2, "")
	first, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	used := first.Label()
	first.Release()

	second, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if second.Label() == used {
		t.Errorf("the just-used identity %q was handed out again while a rested "+
			"one was free", used)
	}
}

// TestRotateEgressWithoutAPoolIsAnHonestRefusal. With no upstreams there is
// nowhere to move to, and pretending otherwise would have the ladder believe it
// had applied a remedy.
func TestRotateEgressWithoutAPoolIsAnHonestRefusal(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 1, "")
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	err = lease.RotateEgress(context.Background())
	if err == nil {
		t.Fatal("RotateEgress succeeded with no upstreams configured")
	}
	if !isNoOtherAddress(err) {
		t.Errorf("err = %v, want one wrapping ErrNoOtherAddress", err)
	}
}

// TestRenewIdentityReplacesTheJarAndTheSession. It is the remedy for a 403 and
// for a soft block, both of which are bound to the identity rather than the IP.
func TestRenewIdentityReplacesTheJarAndTheSession(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 1, "")
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	before := lease.Session()
	if err := lease.RenewIdentity(context.Background()); err != nil {
		t.Fatalf("RenewIdentity: %v", err)
	}
	if lease.Session() == before {
		t.Error("the session did not change; a caller keys its visit identity " +
			"on this and would carry the burnt one forward")
	}
}

// TestIdentitySurvivesARestart. srv_id is the expensive thing, and a restart
// that threw it away would start cold every time — which is the state the whole
// design exists to leave.
func TestIdentitySurvivesARestart(t *testing.T) {
	srv := fakeavito.New(t)
	path := filepath.Join(t.TempDir(), "sessions.json")

	l := openAgainst(t, srv, 1, path)
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := lease.(*sessionLease).s.jar.Identity()
	lease.Release()
	if id == "" {
		t.Fatal("the warm-up minted no identity")
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	restarted := openAgainst(t, srv, 1, path)
	got := restarted.sessions[0].jar.Identity()
	if got != id {
		t.Errorf("identity after a restart = %q, want %q", got, id)
	}
	if !restarted.sessions[0].restored {
		t.Error("the restored session is not marked restored, so its first 403 " +
			"would be blamed on the address")
	}
}

// TestFirst403OnARestoredJarIsNotBlamedOnTheAddress. A laptop that suspended on
// one network and resumed on another presents a full-history identity from
// nowhere. Counting that towards "this address is burnt" would have every
// laptop that changed networks overnight report its ISP as blacklisted.
func TestFirst403OnARestoredJarIsNotBlamedOnTheAddress(t *testing.T) {
	srv := fakeavito.New(t)
	l := openAgainst(t, srv, 1, "")
	s := l.sessions[0]
	s.restored = true
	if s.Note403() {
		t.Error("the first 403 on a restored jar counted towards the burnt total")
	}
	if !s.Note403() {
		t.Error("the second 403 did not count; only the first is forgiven")
	}
}

// TestRotateEgressMovesBetweenConfiguredAddresses.
func TestRotateEgressMovesBetweenConfiguredAddresses(t *testing.T) {
	srv := fakeavito.New(t)
	l, err := Open(Config{
		Surface: avito.SurfaceMobile, Sessions: 1, Root: srv.URL() + "/",
		Upstreams: []string{"a", "b"}, Plain: true,
		CookieHost: hostOf(t, srv.URL()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !l.Caps().RotatesEgress || l.Caps().Addresses != 2 {
		t.Errorf("Caps = %+v, want two addresses and rotation", l.Caps())
	}
	lease, err := l.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	before := lease.(*sessionLease).s.egress
	if err := lease.RotateEgress(context.Background()); err != nil {
		t.Fatalf("RotateEgress: %v", err)
	}
	if after := lease.(*sessionLease).s.egress; after == before {
		t.Errorf("the address did not move: still %q", after)
	}
}
