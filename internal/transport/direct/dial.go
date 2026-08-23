// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// ErrNotH2 is returned when the peer would not negotiate HTTP/2.
//
// It is an error rather than a fallback on purpose. HTTP/1.1 under a byte-exact
// Chrome handshake is a combination no Chrome produces, so degrading quietly
// would replace one fingerprint problem with a sharper one.
var ErrNotH2 = errors.New("direct: peer did not negotiate h2")

func isNotH2(err error) bool { return errors.Is(err, ErrNotH2) }

// dialTimeout bounds the TCP connect. The handshake gets the request's context.
const dialTimeout = 15 * time.Second

// dialTLS opens one connection wearing the given fingerprint.
//
// ALPN offers h2 and http/1.1 in that order, because Chrome does; the result is
// then required to be h2 for the reason ErrNotH2 gives.
func dialTLS(ctx context.Context, fp Fingerprint, network, addr string, roots *x509.CertPool) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	raw, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := utls.UClient(raw, &utls.Config{
		ServerName: host,
		RootCAs:    roots,
		NextProtos: []string{"h2", "http/1.1"},
	}, fp.HelloID)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("direct: handshake with %s: %w", host, err)
	}
	if got := conn.ConnectionState().NegotiatedProtocol; got != "h2" {
		_ = conn.Close()
		return nil, fmt.Errorf("direct: %s negotiated %q: %w", host, got, ErrNotH2)
	}
	return conn, nil
}

// newTransport wires the dialler into an HTTP/2 transport.
//
// It is http2.Transport.DialTLSContext and NOT http.Transport with
// ForceAttemptHTTP2, and the difference is the single easiest thing to get
// wrong in this package. ForceAttemptHTTP2 decides whether it may speak h2 by
// type-asserting the connection to *tls.Conn. A *utls.UConn is not one, the
// assertion fails silently, and the client then speaks HTTP/1.1 over a
// byte-exact Chrome handshake — which is a cleaner signal than a plain Go
// client would have been, and which nothing in the code says out loud.
//
// A transport is never shared between identities: a pooled connection carries
// the previous identity's TLS session, which would undo a renewal quietly.
func newTransport(fp Fingerprint, roots *x509.CertPool) *http2.Transport {
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dialTLS(ctx, fp, network, addr, roots)
		},
		ReadIdleTimeout: 30 * time.Second,
	}
}

// newPlainTransport is for tests against a loopback httptest server, which
// speaks cleartext HTTP/1.1. Nothing in production may use it: it is a Go
// client with no fingerprint at all, which is the state this whole package
// exists to leave.
func newPlainTransport() http.RoundTripper {
	return &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConnsPerHost: 1,
	}
}
