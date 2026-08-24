// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The two cookies that carry the identity on this target.
const (
	// IdentityName is the long-lived one, and the thing that gets flagged. It
	// is what a restart must not throw away.
	IdentityName = "srv_id"
	// NonceName is a sixty-second anti-bot value, reissued on every response
	// and echoed on the next request.
	NonceName = "_avisc"
)

const (
	// NonceTTL is the measured sixty seconds.
	NonceTTL = 60 * time.Second
	// RePrimeAt is when a session goes and fetches a fresh nonce rather than
	// risking the edge of the window.
	RePrimeAt = 55 * time.Second
)

// Jar is a cookie jar for exactly one host tree.
//
// It is written rather than taken from net/http/cookiejar for three reasons and
// all three are load-bearing.
//
// First, the nonce. The standard jar honours Max-Age and stops returning a
// lapsed cookie, so a request made one second late goes out with NO nonce. That
// is strictly worse than a one-second-stale one and comes back as a 429 that
// looks like ordinary rate limiting — a wrong diagnosis leading to a wrong
// remedy. This jar keeps serving the last value it saw and reports its age, and
// the session re-primes at fifty-five seconds rather than waiting to find out.
//
// Second, it persists. srv_id is the expensive thing, and a restart that threw
// it away would start from the cold state every time.
//
// Third, it is scoped to one host tree at construction, so no cookie from this
// target can ever be sent anywhere else, and publicsuffix is not needed.
type Jar struct {
	mu    sync.Mutex
	host  string
	items map[string]string
	// nonceAt is when the nonce was last written. An age, not an expiry.
	nonceAt time.Time
	now     func() time.Time
	off     time.Duration
}

// JarState is the persistable form.
type JarState struct {
	Host    string            `json:"host"`
	Items   map[string]string `json:"items"`
	NonceAt time.Time         `json:"nonce_at"`
}

// NewJar makes an empty jar scoped to host and every subdomain of it.
func NewJar(host string) *Jar {
	return &Jar{host: strings.ToLower(host), items: map[string]string{}, now: time.Now}
}

// RestoreJar rebuilds a jar from a snapshot.
func RestoreJar(s JarState) *Jar {
	j := NewJar(s.Host)
	for k, v := range s.Items {
		j.items[k] = v
	}
	j.nonceAt = s.NonceAt
	return j
}

// Snapshot copies the jar out for persistence.
func (j *Jar) Snapshot() JarState {
	j.mu.Lock()
	defer j.mu.Unlock()
	items := make(map[string]string, len(j.items))
	for k, v := range j.items {
		items[k] = v
	}
	return JarState{Host: j.host, Items: items, NonceAt: j.nonceAt}
}

// advance moves this jar's clock. Tests only.
func (j *Jar) advance(d time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.off += d
}

func (j *Jar) clock() time.Time { return j.now().Add(j.off) }

// SetCookies records what a response set. A deletion — a negative Max-Age, or
// an empty value with any Max-Age set — is honoured, because ignoring a server
// that explicitly clears a cookie is a different bug from the one this jar
// exists to avoid, and a jar making both mistakes would be worse than the
// standard one.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if !j.owns(u) {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		switch {
		case c.MaxAge < 0, c.Value == "" && c.MaxAge != 0:
			delete(j.items, c.Name)
		default:
			j.items[c.Name] = c.Value
			if c.Name == NonceName {
				j.nonceAt = j.clock()
			}
		}
	}
}

// Cookies returns everything this jar holds for a URL it owns — including a
// nonce past its stated lifetime, which is the whole point.
func (j *Jar) Cookies(u *url.URL) []*http.Cookie {
	if !j.owns(u) {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*http.Cookie, 0, len(j.items))
	for k, v := range j.items {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}

// NonceAge is how long ago the nonce was last written. A session re-primes off
// this rather than off an expiry, because the remedy for an old nonce is to go
// and get a new one, not to stop sending the old one.
func (j *Jar) NonceAge() time.Duration {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.nonceAt.IsZero() {
		return NonceTTL * 100 // never seen: as stale as it gets
	}
	return j.clock().Sub(j.nonceAt)
}

// Identity is the long-lived cookie, or "" before a warm-up.
func (j *Jar) Identity() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.items[IdentityName]
}

// Clear empties the jar. RenewIdentity uses it: the identity being replaced is
// the whole of what the jar holds.
func (j *Jar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.items = map[string]string{}
	j.nonceAt = time.Time{}
}

// owns is host-tree scoping done at construction rather than by public-suffix
// rules. "avito.ru" owns "www.avito.ru" and "m.avito.ru" and nothing else — in
// particular not "avito.ru.evil.test", which a suffix check written without the
// leading dot would accept.
func (j *Jar) owns(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return h == j.host || strings.HasSuffix(h, "."+j.host)
}
