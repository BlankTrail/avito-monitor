// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrNoOtherAddress is returned by RotateEgress when there is nowhere to move
// to. An honest refusal, because the ladder would otherwise believe a remedy
// had been applied and count an attempt against a budget it did not spend.
var ErrNoOtherAddress = errors.New("direct: no other egress address configured")

func isNoOtherAddress(err error) bool { return errors.Is(err, ErrNoOtherAddress) }

// session is one identity: one jar, one fingerprint, one egress, one lock.
//
// The lock is per session and not per process. Two concurrent requests on one
// jar necessarily send the same nonce, and nonce reuse is exactly what a nonce
// detects — but two requests on two jars share nothing and are free, which is
// how this mode scales at all.
type session struct {
	mu sync.Mutex

	fp     Fingerprint
	jar    *Jar
	client *http.Client
	roots  *x509.CertPool
	plain  bool // tests against a cleartext loopback server

	index   int
	seq     int
	egress  string
	lastUse time.Time
	leased  bool
	warm    bool

	// restored says this identity came off disk rather than being minted here.
	// Its first 403 is treated as the exit address having changed rather than
	// as evidence the address is burnt.
	restored bool
	burnt    int
}

func newSession(index int, fp Fingerprint, jar *Jar, egress string, roots *x509.CertPool, plain bool) *session {
	s := &session{fp: fp, jar: jar, index: index, egress: egress, roots: roots, plain: plain}
	s.rebuildClient()
	return s
}

// rebuildClient gives the session a transport of its own. It is called on
// creation and on every RenewIdentity, and never shared: a pooled connection
// carries the previous identity's TLS session, which would undo a renewal
// quietly.
func (s *session) rebuildClient() {
	var rt http.RoundTripper
	if s.plain {
		rt = newPlainTransport()
	} else {
		rt = newTransport(s.fp, s.roots, s.egress)
	}
	// The decoder is not optional. This client sets Accept-Encoding itself,
	// because Chrome's exact set is part of the fingerprint, and Go then stops
	// decompressing gzip on its own and never handled br or zstd at all.
	rt = decodingTransport{base: rt}
	s.client = &http.Client{Transport: rt, Jar: s.jar, Timeout: 120 * time.Second}
}

// Session is the identity string a caller keys a visit on. It moves exactly
// when the identity does — on a renewal, and on nothing else.
func (s *session) Session() string {
	return fmt.Sprintf("%s#%d/%s", s.fp.Name, s.seq, s.jar.Identity())
}

// Label is what a person reads in a log line.
func (s *session) Label() string {
	return "direct:" + s.fp.Name + "#" + strconv.Itoa(s.index)
}

// Note403 records a refusal and reports whether it counts towards the total
// that means this address is finished.
//
// The first one on a restored jar does not. A laptop that suspended on one
// network and resumed on another presents a full-history identity from
// nowhere, and conflating the two would have every such laptop report its ISP
// as blacklisted.
func (s *session) Note403() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restored {
		s.restored = false
		return false
	}
	s.burnt++
	return true
}

// needsWarmUp is true before the first identity, and again once the nonce is
// close enough to its sixty-second life that a request would race it.
func (s *session) needsWarmUp() bool {
	return !s.warm || s.jar.Identity() == "" || s.jar.NonceAge() >= RePrimeAt
}

// warmUp performs the root navigation that mints an identity.
//
// A fresh cookieless request collects a 429 immediately, so this is neither
// optional nor lazy: Acquire does it before handing the lease out, or the
// caller's first real request is spent learning it was needed.
func (s *session) warmUp(ctx context.Context, root string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root, nil)
	if err != nil {
		return err
	}
	s.fp.Profile().Apply(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("direct: warm-up: %w", err)
	}
	defer resp.Body.Close()
	// Drain, so the connection can be reused rather than abandoned mid-body.
	_, _ = io.Copy(io.Discard, resp.Body)
	if s.jar.Identity() == "" {
		return fmt.Errorf("direct: warm-up returned %d and minted no identity",
			resp.StatusCode)
	}
	s.warm = true
	return nil
}

// renew replaces the fingerprint's cookie jar and the session string, keeping
// the address. The remedy for a 403 and for a soft block, both of which are
// bound to the identity rather than to the IP.
func (s *session) renew() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jar.Clear()
	s.seq++
	s.warm = false
	s.restored = false
	s.rebuildClient()
}

// recycle is renew plus the fingerprint. It is what this mode has in place of
// closing a port: everything the target could have recognised is replaced —
// jar, TLS hello, header set, connections — and only the exit address stays.
//
// The distinction was learned on the other transport. A BlankTrail port that
// had stopped returning data was renewed six times in one fetch, twenty-seven
// minutes, and answered with an empty page every time, because renewing rotates
// the profile and leaves the endpoint itself in place. Here there is no proxy
// holding state, so the strongest available reset is this one.
func (s *session) recycle(fp Fingerprint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fp = fp
	s.jar.Clear()
	s.seq++
	s.warm = false
	s.restored = false
	s.burnt = 0
	s.rebuildClient()
}
