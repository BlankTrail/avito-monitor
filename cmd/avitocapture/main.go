// SPDX-License-Identifier: AGPL-3.0-or-later

// avitocapture fetches one URL and freezes it as a fixture.
//
// It is a developer tool and it does no fingerprinting: it goes through a
// BlankTrail port, which does. The real transport is increment 2, and this
// command must not grow into it — anything it learns about retries, sessions or
// classification belongs there instead.
package main

import (
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type meta struct {
	URL        string `json:"url"`
	Surface    string `json:"surface"`
	CapturedAt string `json:"captured_at"`
	// FrozenNow is what a test runs its clock at, so a page with a validity
	// window cannot make the suite fail on a day nobody changed anything.
	FrozenNow int64  `json:"frozen_now"`
	Status    int    `json:"status"`
	Bytes     int    `json:"bytes"`
	ProxyUsed string `json:"proxy_used"`
}

func main() {
	proxy := flag.String("proxy", "", "proxy URL, e.g. socks5://127.0.0.1:20000")
	surface := flag.String("surface", "desktop", "desktop|mobile")
	out := flag.String("out", "avito/testdata", "directory to write into")
	name := flag.String("name", "", "fixture name (required)")
	timeout := flag.Duration("timeout", 120*time.Second, "the first request may be solving a challenge")
	flag.Parse()
	if flag.NArg() != 1 || *name == "" {
		fmt.Fprintln(os.Stderr, "usage: avitocapture -name <fixture> [-proxy URL] [-surface desktop|mobile] <url>")
		os.Exit(2)
	}

	tr := &http.Transport{}
	if *proxy != "" {
		u, err := url.Parse(*proxy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proxy:", err)
			os.Exit(1)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	// The proxy re-signs origins with its own MITM CA. This is a capture tool,
	// not a security boundary, and increment 2 pins the CA properly.
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec

	req, err := http.NewRequest(http.MethodGet, flag.Arg(0), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "request:", err)
		os.Exit(1)
	}
	// Accept-Language is ours in both modes: BlankTrail passes it through
	// verbatim and Avito needs it.
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

	client := &http.Client{Transport: tr, Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}
	htmlPath := filepath.Join(*out, *name+".html.gz")
	f, err := os.Create(htmlPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gzip:", err)
		os.Exit(1)
	}
	if _, err := zw.Write(body); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	if err := zw.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "gzip close:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close:", err)
		os.Exit(1)
	}

	now := time.Now()
	m := meta{
		URL:        flag.Arg(0),
		Surface:    *surface,
		CapturedAt: now.UTC().Format(time.RFC3339),
		FrozenNow:  now.Unix(),
		Status:     resp.StatusCode,
		Bytes:      len(body),
		ProxyUsed:  *proxy,
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "meta:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(*out, *name+".meta.json"), append(mb, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "meta:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes, http %d)\n", htmlPath, len(body), resp.StatusCode)
	fmt.Println("REMEMBER: this page carries third-party personal data. Scrub before publishing.")
}
