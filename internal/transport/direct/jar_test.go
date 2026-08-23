// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestJarServesALapsedNonceAnyway is the reason this jar is written rather than
// taken from net/http/cookiejar.
//
// _avisc is a sixty-second anti-bot nonce. The standard jar honours Max-Age and
// simply stops returning a lapsed cookie, so a request made one second late
// goes out with NO nonce — which is strictly worse than a one-second-stale one,
// and comes back as a 429 that looks like ordinary rate limiting.
func TestJarServesALapsedNonceAnyway(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/all?q=x")
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n1", MaxAge: 60}})

	j.advance(90 * time.Second)

	found := ""
	for _, c := range j.Cookies(u) {
		if c.Name == NonceName {
			found = c.Value
		}
	}
	if found != "n1" {
		t.Fatal("the jar dropped a lapsed nonce; sending none is strictly worse " +
			"than sending a stale one, and the standard jar does exactly the " +
			"wrong thing here")
	}
	if age := j.NonceAge(); age < 90*time.Second {
		t.Errorf("NonceAge = %v, want at least 90s — the session re-primes off "+
			"this number", age)
	}
}

// TestJarIsScopedToOneHostAtConstruction. No cookie from this target may ever
// be sent anywhere else, and scoping at construction is what makes publicsuffix
// unnecessary.
func TestJarIsScopedToOneHostAtConstruction(t *testing.T) {
	j := NewJar("avito.ru")
	j.SetCookies(mustURL(t, "https://www.avito.ru/"),
		[]*http.Cookie{{Name: IdentityName, Value: "srv-1"}})

	for _, other := range []string{
		"https://example.com/", "https://avito.ru.evil.test/", "https://notavito.ru/",
	} {
		if got := j.Cookies(mustURL(t, other)); len(got) != 0 {
			t.Errorf("%s received %d cookies from the avito jar", other, len(got))
		}
	}
	if got := j.Cookies(mustURL(t, "https://m.avito.ru/all")); len(got) == 0 {
		t.Error("a subdomain of the scoped host received nothing")
	}
}

// TestNonceIsOverwrittenOnEveryResponse. The server reissues it every time and
// a jar that kept the first value would go stale inside a minute.
func TestNonceIsOverwrittenOnEveryResponse(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n1"}})
	j.advance(10 * time.Second)
	j.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n2"}})

	if j.NonceAge() > time.Second {
		t.Errorf("NonceAge = %v after a fresh value; the age must reset with it",
			j.NonceAge())
	}
	for _, c := range j.Cookies(u) {
		if c.Name == NonceName && c.Value != "n2" {
			t.Errorf("nonce = %q, want n2", c.Value)
		}
	}
}

// TestIdentitySurvivesASnapshot. srv_id is the long-lived identity and the
// expensive thing: it is what a restart must not throw away.
func TestIdentitySurvivesASnapshot(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{
		{Name: IdentityName, Value: "srv-42"},
		{Name: NonceName, Value: "n1"},
	})
	restored := RestoreJar(j.Snapshot())
	if restored.Identity() != "srv-42" {
		t.Errorf("Identity after a round trip = %q, want srv-42", restored.Identity())
	}
}

// TestDeletionIsHonoured. A jar that ignored Max-Age entirely would keep a
// cookie the server explicitly cleared, which is a different bug from the one
// this jar exists to avoid — and a jar making both mistakes would be worse than
// the standard one.
func TestDeletionIsHonoured(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{{Name: "v", Value: "1"}})
	j.SetCookies(u, []*http.Cookie{{Name: "v", Value: "", MaxAge: -1}})
	for _, c := range j.Cookies(u) {
		if c.Name == "v" {
			t.Error("a cookie the server deleted is still being sent")
		}
	}
}

// TestClearEmptiesEverything. RenewIdentity replaces the identity, and the jar
// is the whole of what the identity is.
func TestClearEmptiesEverything(t *testing.T) {
	j := NewJar("avito.ru")
	u := mustURL(t, "https://www.avito.ru/")
	j.SetCookies(u, []*http.Cookie{
		{Name: IdentityName, Value: "srv-1"},
		{Name: NonceName, Value: "n1"},
	})
	j.Clear()
	if j.Identity() != "" {
		t.Error("the identity survived Clear")
	}
	if len(j.Cookies(u)) != 0 {
		t.Error("cookies survived Clear")
	}
	if j.NonceAge() < NonceTTL {
		t.Error("NonceAge after Clear reads fresh; a cleared jar has no nonce " +
			"and must read as stale so the session re-primes")
	}
}

// TestTheStandardJarWouldHaveDroppedIt is the counter-example, run rather than
// asserted, so the reason this file exists cannot be argued away later.
func TestTheStandardJarWouldHaveDroppedIt(t *testing.T) {
	std, err := newStdJarForComparison()
	if err != nil {
		t.Fatal(err)
	}
	u := mustURL(t, "https://www.avito.ru/")
	std.SetCookies(u, []*http.Cookie{{
		Name: NonceName, Value: "n1", Expires: time.Now().Add(-time.Second),
	}})
	if got := std.Cookies(u); len(got) != 0 {
		t.Skip("this Go version's cookiejar kept an expired cookie; the " +
			"comparison no longer holds and the argument in jar.go needs re-reading")
	}
	// And ours keeps it, which is the whole point.
	ours := NewJar("avito.ru")
	ours.SetCookies(u, []*http.Cookie{{Name: NonceName, Value: "n1", MaxAge: 60}})
	ours.advance(2 * NonceTTL)
	if len(ours.Cookies(u)) == 0 {
		t.Error("ours dropped it too, which makes this package pointless")
	}
}

// newStdJarForComparison is net/http/cookiejar, used only by the test above to
// demonstrate the behaviour this package deliberately does not copy.
func newStdJarForComparison() (http.CookieJar, error) {
	return cookiejar.New(nil)
}
