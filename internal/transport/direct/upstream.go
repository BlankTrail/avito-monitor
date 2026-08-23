// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// dialThrough opens a TCP connection to addr, leaving through egress.
//
// An empty egress is the host's own address. Anything else must actually carry
// the bytes: Caps.Addresses multiplies the published request budget by the
// number of configured addresses, so an upstream that is recorded but not
// dialled would double the budget a person is shown while every request still
// left from one IP. A budget that lies is worse than no budget.
//
// Both schemes resolve the target name AT THE PROXY rather than here. The
// target is the point of an exit address; resolving it locally would leak the
// lookup from the host's own resolver and defeat the arrangement.
func dialThrough(ctx context.Context, egress, network, addr string) (net.Conn, error) {
	if egress == "" {
		return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, addr)
	}
	u, err := url.Parse(egress)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("direct: unusable egress %q: %w", egress, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h", "":
		var auth *proxy.Auth
		if u.User != nil {
			pw, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pw}
		}
		d, err := proxy.SOCKS5(network, u.Host, auth,
			&net.Dialer{Timeout: dialTimeout})
		if err != nil {
			return nil, fmt.Errorf("direct: socks5 %s: %w", u.Host, err)
		}
		// x/net/proxy sends a hostname as an FQDN address type, so the name
		// reaches the proxy unresolved. This assertion holds for the socks5
		// dialler; the fallback exists so a future change cannot silently lose
		// the request's deadline.
		if cd, ok := d.(proxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)

	case "http", "https":
		return dialConnect(ctx, u, network, addr)

	default:
		return nil, fmt.Errorf("direct: egress scheme %q is not supported", u.Scheme)
	}
}

// dialConnect tunnels through an HTTP proxy with CONNECT.
//
// Written out rather than delegated to http.Transport's Proxy field, because
// that path builds its own TLS on top and this package must do the handshake
// itself with utls — the whole reason the package exists.
func dialConnect(ctx context.Context, u *url.URL, network, addr string) (net.Conn, error) {
	raw, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, u.Host)
	if err != nil {
		return nil, fmt.Errorf("direct: connect to proxy %s: %w", u.Host, err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if u.User != nil {
		pw, _ := u.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(u.User.Username(), pw))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	if err := req.Write(raw); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("direct: CONNECT %s: %w", addr, err)
	}
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("direct: CONNECT %s: %w", addr, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = raw.Close()
		return nil, fmt.Errorf("direct: proxy %s refused CONNECT %s: %s",
			u.Host, addr, resp.Status)
	}
	// Anything the proxy sent past the response header would be the start of
	// the TLS server hello, and a reader that buffered it would hand utls a
	// stream missing its first bytes. Refuse rather than corrupt the handshake.
	if br.Buffered() > 0 {
		_ = raw.Close()
		return nil, fmt.Errorf("direct: proxy %s spoke before the tunnel opened", u.Host)
	}
	_ = raw.SetDeadline(time.Time{})
	return raw, nil
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
