// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TestFingerprintBindsTheWholeIdentity. The hello id, the user agent, the
// sec-ch-ua string and the platform are one fact about one browser. Setting one
// without the others produces a client that claims to be Chrome on Windows in
// its headers and something else in its handshake, which is a stronger signal
// than either alone.
func TestFingerprintBindsTheWholeIdentity(t *testing.T) {
	for _, fp := range []Fingerprint{ChromeWindows(), ChromeAndroid()} {
		if fp.HelloID.Client == "" {
			t.Errorf("%s: no hello id", fp.Name)
		}
		if fp.UserAgent == "" || fp.SecCHUA == "" || fp.Platform == "" {
			t.Errorf("%s: an incomplete identity: ua=%q sec-ch-ua=%q platform=%q",
				fp.Name, fp.UserAgent, fp.SecCHUA, fp.Platform)
		}
		if fp.AcceptEncoding != "gzip, deflate, br, zstd" {
			t.Errorf("%s: Accept-Encoding = %q; Chrome sends all four and this "+
				"program decodes all four", fp.Name, fp.AcceptEncoding)
		}
	}
}

// TestClaimedVersionMatchesTheHandshake. The User-Agent must name the same
// Chrome the hello id presents. Claiming a newer browser than utls can
// impersonate is an internal contradiction, which is a sharper signal than
// simply being out of date.
func TestClaimedVersionMatchesTheHandshake(t *testing.T) {
	for _, fp := range []Fingerprint{ChromeWindows(), ChromeAndroid()} {
		if !strings.Contains(fp.UserAgent, "Chrome/"+chromeMajor+".") {
			t.Errorf("%s: user agent %q does not claim Chrome %s, which is what "+
				"the hello id presents", fp.Name, fp.UserAgent, chromeMajor)
		}
		if !strings.Contains(fp.SecCHUA, `v="`+chromeMajor+`"`) {
			t.Errorf("%s: sec-ch-ua %q disagrees with the handshake's version",
				fp.Name, fp.SecCHUA)
		}
	}
}

// TestChromeAndroidIsTheMobileSurface. In direct mode nothing rewrites our
// headers, so the header set IS the surface. This is the one direction never
// tested against the live site — every mobile page in the measurement set
// arrived through a BlankTrail iOS port.
func TestChromeAndroidIsTheMobileSurface(t *testing.T) {
	fp := ChromeAndroid()
	if fp.Platform != `"Android"` {
		t.Errorf("platform = %q, want \"Android\"", fp.Platform)
	}
	if !strings.Contains(fp.UserAgent, "Android") || !strings.Contains(fp.UserAgent, "Mobile") {
		t.Errorf("user agent = %q, want an Android mobile one", fp.UserAgent)
	}
}

// TestDialTLSNegotiatesH2 is the trap this whole file exists to avoid.
//
// http.Transport.ForceAttemptHTTP2 detects h2 by type-asserting the connection
// to *tls.Conn. A *utls.UConn fails that assertion silently, and the client
// then speaks HTTP/1.1 over a perfect Chrome handshake — a combination no
// Chrome produces and a cleaner signal than a plain Go client would have been.
// So h2 is negotiated through http2.Transport.DialTLSContext instead, and the
// dialler refuses anything else rather than degrading quietly.
func TestDialTLSNegotiatesH2(t *testing.T) {
	addr, roots := tlsEcho(t, []string{"h2", "http/1.1"})
	conn, err := dialTLS(context.Background(), ChromeWindows(), "tcp", addr, roots, "")
	if err != nil {
		t.Fatalf("dialTLS: %v", err)
	}
	defer conn.Close()
	u, ok := conn.(*utls.UConn)
	if !ok {
		t.Fatalf("conn is %T, want *utls.UConn — the whole point is that the "+
			"handshake is not Go's", conn)
	}
	if got := u.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Errorf("negotiated %q, want h2", got)
	}
}

// TestDialTLSRefusesADowngrade. A server that will not do h2 is a surprise
// worth failing on: HTTP/1.1 under a Chrome handshake is its own fingerprint.
func TestDialTLSRefusesADowngrade(t *testing.T) {
	addr, roots := tlsEcho(t, []string{"http/1.1"})
	_, err := dialTLS(context.Background(), ChromeWindows(), "tcp", addr, roots, "")
	if err == nil {
		t.Fatal("a downgrade to HTTP/1.1 was accepted silently")
	}
	if !isNotH2(err) {
		t.Errorf("err = %v, want one wrapping ErrNotH2", err)
	}
}

// TestTheHandshakeIsNotGos. Without this the package could regress to
// crypto/tls and every other test here would still pass — a Go client with
// perfect headers is exactly what this package exists not to be.
func TestTheHandshakeIsNotGos(t *testing.T) {
	addr, roots := tlsEcho(t, []string{"h2"})
	conn, err := dialTLS(context.Background(), ChromeWindows(), "tcp", addr, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, isStdlib := conn.(*tls.Conn); isStdlib {
		t.Fatal("the connection is a crypto/tls one: Go's ClientHello ships no " +
			"GREASE, a cipher order no browser produces, and an extension order " +
			"identical in every Go binary on earth")
	}
}

// tlsEcho starts a TLS listener offering the given ALPN protocols and returns
// its address and a pool trusting its throwaway certificate. It never reads or
// writes: every test here is about the handshake.
func tlsEcho(t *testing.T, protos []string) (addr string, roots *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		NextProtos:   protos,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				if tc, ok := c.(*tls.Conn); ok {
					_ = tc.HandshakeContext(context.Background())
				}
				time.Sleep(50 * time.Millisecond)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String(), pool
}
