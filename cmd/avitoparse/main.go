// SPDX-License-Identifier: AGPL-3.0-or-later

// avitoparse reads a captured page from disk and prints what the extractor made
// of it. It touches no network, which is the point: every question about why a
// page parsed the way it did is answered here rather than by fetching it again
// and getting a different page.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
)

func main() {
	surface := flag.String("surface", "", "desktop|mobile (default: guessed from the file name)")
	asJSON := flag.Bool("json", false, "print the whole Page as JSON")
	audit := flag.Bool("audit", false, "run the second extraction path too and compare")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: avitoparse [-surface desktop|mobile] [-json] [-audit] <file[.gz]>")
		os.Exit(2)
	}
	path := flag.Arg(0)

	body, err := readMaybeGzip(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	s := avito.Surface(*surface)
	if s == "" {
		s = avito.SurfaceDesktop
		if strings.Contains(path, "mobile") {
			s = avito.SurfaceMobile
		}
	}

	page, err := avito.Extract(avito.Document{Surface: s, Body: body}, avito.Options{AuditDOM: *audit})
	if err != nil {
		fmt.Fprintln(os.Stderr, "extract:", err)
		if hit, ok := (avito.Document{Body: body}).Wall(); ok {
			fmt.Fprintf(os.Stderr, "the body is a wall: %s (%q)\n", hit.Label, hit.Marker)
		}
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(page); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("surface=%s kind=%s rule=%s path=%s script-type=%q\n",
		page.Surface, page.Kind, page.Report.Rule, page.Report.Path, page.Report.ScriptType)
	fmt.Printf("listings=%d found=%d dropped=%d ok=%v empty-declared=%v",
		len(page.Listings), page.Report.Found, page.Report.Dropped, page.Ok(), page.EmptyDeclared)
	if page.TotalCount != nil {
		fmt.Printf(" total=%d", *page.TotalCount)
	}
	fmt.Println()
	if page.CanonicalURL != "" {
		fmt.Println("canonical:", page.CanonicalURL)
	}
	if a := page.Audit; a != nil {
		fmt.Printf("audit: agreed=%d only-state=%d only-dom=%d\n", a.Agreed, a.OnlyState, a.OnlyDOM)
	}
	for _, w := range page.Report.Warnings {
		fmt.Printf("warn  %s: %s\n", w.Code, w.Detail)
	}
	for i, l := range page.Listings {
		price := "—"
		switch l.PriceKind {
		case avito.PriceExact:
			price = fmt.Sprintf("%d.%02d", l.Price.Minor/100, l.Price.Minor%100)
		case avito.PriceNegotiable:
			price = "договорная"
		}
		fmt.Printf("%3d  %-12d %14s  %s\n", i+1, l.ItemID, price, l.Title)
	}
}

func readMaybeGzip(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		r = zr
	}
	return io.ReadAll(r)
}
