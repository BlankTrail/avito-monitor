// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestEmptyEgressDialsDirectly: the common case must not go looking for a
// proxy that was never configured.
func TestEmptyEgressDialsDirectly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	conn, err := dialThrough(context.Background(), "", "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialThrough with no egress: %v", err)
	}
	conn.Close()
}

// connectProxy is a minimal HTTP CONNECT proxy that records what it was asked
// to reach and then splices the two connections.
type connectProxy struct {
	ln      net.Listener
	target  atomic.Value // string
	auth    atomic.Value // string
	tunnels int32
}

func newConnectProxy(t *testing.T) *connectProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &connectProxy{ln: ln}
	go p.serve()
	t.Cleanup(func() { ln.Close() })
	return p
}

func (p *connectProxy) addr() string { return p.ln.Addr().String() }

func (p *connectProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

func (p *connectProxy) handle(c net.Conn) {
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		c.Close()
		return
	}
	if req.Method != http.MethodConnect {
		io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		c.Close()
		return
	}
	p.target.Store(req.Host)
	p.auth.Store(req.Header.Get("Proxy-Authorization"))

	up, err := net.Dial("tcp", req.Host)
	if err != nil {
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		c.Close()
		return
	}
	io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	atomic.AddInt32(&p.tunnels, 1)
	go func() { io.Copy(up, br); up.Close() }()
	go func() { io.Copy(c, up); c.Close() }()
}

// TestHTTPEgressCarriesTheBytes is the test that stops Caps.Addresses from
// lying. An upstream that is configured but not dialled would multiply the
// published budget while every request still left from one IP.
func TestHTTPEgressCarriesTheBytes(t *testing.T) {
	// The origin is a bare TCP echo of a fixed line, so this test is about the
	// tunnel and not about HTTP.
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		c, err := origin.Accept()
		if err != nil {
			return
		}
		io.WriteString(c, "through")
		c.Close()
	}()

	p := newConnectProxy(t)
	conn, err := dialThrough(context.Background(),
		"http://user:pass@"+p.addr(), "tcp", origin.Addr().String())
	if err != nil {
		t.Fatalf("dialThrough via CONNECT: %v", err)
	}
	defer conn.Close()

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "through" {
		t.Errorf("read %q through the tunnel; want the origin's bytes", got)
	}
	if tgt, _ := p.target.Load().(string); tgt != origin.Addr().String() {
		t.Errorf("proxy was asked for %q; want %q — the target must be resolved "+
			"at the proxy, not here", tgt, origin.Addr().String())
	}
	if a, _ := p.auth.Load().(string); !strings.HasPrefix(a, "Basic ") {
		t.Errorf("Proxy-Authorization = %q; credentials in the egress URL must "+
			"be sent", a)
	}
}

// TestRefusedCONNECTIsAnError: a proxy that will not open the tunnel must not
// leave a caller holding a connection to the proxy itself.
func TestRefusedCONNECTIsAnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(c)
			http.ReadRequest(br) //nolint:errcheck // the point is the refusal
			io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
			c.Close()
		}
	}()
	_, err = dialThrough(context.Background(), "http://"+ln.Addr().String(),
		"tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("a refused CONNECT was reported as a connection")
	}
	if !strings.Contains(err.Error(), "407") {
		t.Errorf("err = %v; it should name what the proxy said", err)
	}
}

// TestUnsupportedEgressSchemeIsRefused. Guessing a scheme would send the
// request from the host's own address while the budget counted an extra one.
func TestUnsupportedEgressSchemeIsRefused(t *testing.T) {
	_, err := dialThrough(context.Background(), "quic://10.0.0.1:1080", "tcp", "example.org:443")
	if err == nil {
		t.Fatal("an unsupported egress scheme was accepted")
	}
	if !strings.Contains(err.Error(), "quic") {
		t.Errorf("err = %v; it should name the scheme it will not use", err)
	}
}

// TestRotateEgressRebuildsTheClient. The egress is captured in the transport's
// dial closure, so reassigning the field alone changes a log label and routes
// nothing differently — a rotation the ladder would believe and that never
// happened.
func TestRotateEgressRebuildsTheClient(t *testing.T) {
	s := newSession(0, ChromeAndroid(), NewJar("example.org"), "socks5://a:1080", nil, false)
	before := s.client
	s.egress = "socks5://b:1080"
	s.rebuildClient()
	if s.client == before {
		t.Fatal("rebuildClient reused the client, so the old egress is still " +
			"captured in its dial closure")
	}
}
