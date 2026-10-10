// SPDX-License-Identifier: AGPL-3.0-or-later

package direct

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

// Verdict is what one exit turned out to be when asked to reach the site.
type Verdict string

const (
	// ExitOK reached the site and was shown the site's own certificate.
	ExitOK Verdict = "ok"
	// ExitUnreachable would not carry the connection at all.
	ExitUnreachable Verdict = "unreachable"
	// ExitIntercepting carried the connection and answered the TLS handshake
	// with a certificate the site did not issue.
	//
	// The case this probe exists for. Measured on 11 October 2026: four exits
	// in five of a provider's list of fifteen thousand presented *.avito.ru
	// signed by «None, LLC, Dallas». Such an exit reads every request in the
	// clear and rewrites what it likes; the collector refuses it, and a person
	// should know before the first pass rather than after a day of them.
	ExitIntercepting Verdict = "intercepting"
	// ExitTLSFailed reached the site and the handshake did not complete.
	ExitTLSFailed Verdict = "tls-failed"
)

// Probe is one exit's answer.
type Probe struct {
	Verdict Verdict
	// Issuer is the certificate's issuer, said only for an intercepting exit:
	// the name of whoever is in the middle is the useful part of the answer.
	Issuer  string
	Elapsed time.Duration
	Err     error
}

// ProbeExit opens TLS to host:443 through egress and checks the certificate
// against the system's roots. No HTTP request is sent: whether the exit carries
// a genuine connection is the whole question, and asking the site for a page
// would spend a request of the budget on a settings screen.
func ProbeExit(ctx context.Context, egress, host string) Probe {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	raw, err := dialThrough(ctx, egress, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return Probe{Verdict: ExitUnreachable, Err: err, Elapsed: time.Since(start)}
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}

	conn := tls.Client(raw, &tls.Config{ServerName: host})
	err = conn.HandshakeContext(ctx)
	p := Probe{Elapsed: time.Since(start), Err: err}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	switch {
	case err == nil:
		p.Verdict = ExitOK
	case errors.As(err, &unknown):
		p.Verdict = ExitIntercepting
		if c := unknown.Cert; c != nil {
			// The organisation is the name a person recognises; the full
			// distinguished name escapes its commas and reads as noise.
			p.Issuer = c.Issuer.String()
			switch {
			case len(c.Issuer.Organization) > 0:
				p.Issuer = c.Issuer.Organization[0]
			case c.Issuer.String() == c.Subject.String():
				// «CN=m.avito.ru» as an issuer reads like the site's own
				// signature; it is a certificate that signed itself.
				p.Issuer = "самоподписанный"
			}
		}
	case errors.As(err, &invalid), errors.As(err, &hostname):
		p.Verdict = ExitIntercepting
	default:
		p.Verdict = ExitTLSFailed
	}
	return p
}
