// SPDX-License-Identifier: AGPL-3.0-or-later

// avitomon is the monitor. Today it does one thing: fetch one page through
// either transport and say what came back.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	"github.com/BlankTrail/avito-monitor/internal/transport/bt"
	"github.com/BlankTrail/avito-monitor/internal/transport/direct"
)

// keyEnv is where the BlankTrail API key comes from when it is not on the
// command line. It is never written to a file this program creates, never
// logged, and never included in an error.
const keyEnv = "AVITOMON_BLANKTRAIL_KEY"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetchCmd(os.Args[2:])
	case "transport":
		err = transportCmd(os.Args[2:])
	case "volume":
		err = volumeCmd(os.Args[2:])
	case "watch":
		err = watchCmd(os.Args[2:])
	case "run":
		err = runCmd(os.Args[2:])
	case "changes":
		err = changesCmd(os.Args[2:])
	case "notify":
		err = notifyCmd(os.Args[2:])
	case "target":
		err = targetCmd(os.Args[2:])
	case "enrich":
		err = enrichCmd(os.Args[2:])
	case "panel":
		err = panelCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		var halt *transport.HaltError
		if errors.As(err, &halt) {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "STOPPED  %s\n", halt.Verdict.Class)
			fmt.Fprintf(os.Stderr, "  status  %d\n", halt.Status)
			if halt.Verdict.Wall.Marker != "" {
				fmt.Fprintf(os.Stderr, "  marker  %q (%s)\n",
					halt.Verdict.Wall.Marker, halt.Verdict.Wall.Label)
			}
			fmt.Fprintf(os.Stderr, "  url     %s\n\n", halt.URL)
			fmt.Fprintln(os.Stderr, wrap(halt.Boundary(), 76))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  avitomon fetch [flags] <url>")
	fmt.Fprintln(os.Stderr, "  avitomon transport show|set direct|blanktrail")
	fmt.Fprintln(os.Stderr, "  avitomon watch add [flags] <url>")
	fmt.Fprintln(os.Stderr, "  avitomon run --once [flags]")
	fmt.Fprintln(os.Stderr, "  avitomon changes [flags]")
	fmt.Fprintln(os.Stderr, "  avitomon volume [flags]")
	fmt.Fprintln(os.Stderr, "  avitomon target list|add [flags] <chat id>")
	fmt.Fprintln(os.Stderr, "  avitomon notify [-test -chat <id> -rung <name>] [flags]")
	fmt.Fprintln(os.Stderr, "  avitomon enrich [-db avito.db] [-limit 20]")
	fmt.Fprintln(os.Stderr, "  avitomon panel [-db avito.db] [-port 8765]")
}

func fetchCmd(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	mode := fs.String("mode", "direct", "direct|blanktrail")
	surfaceFlag := fs.String("surface", "mobile", "mobile|desktop")
	control := fs.String("control", "http://127.0.0.1:8891", "BlankTrail control API")
	key := fs.String("key", "", "BlankTrail API key (or "+keyEnv+")")
	ports := fs.Int("ports", 2, "BlankTrail: ports to open")
	sessions := fs.Int("sessions", 1, "direct: identities to hold")
	proxies := fs.String("proxies", "", "file of egress proxies, one per line "+
		"(scheme://[user:pass@]host:port; socks5 assumed)")
	statePath := fs.String("state", "", "direct: where identities are persisted")
	root := fs.String("root", "", "direct: the URL a warm-up navigates to (default: the target's origin)")
	timeout := fs.Duration("timeout", 3*time.Minute, "the first request may be solving a challenge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("exactly one URL is required")
	}
	target := fs.Arg(0)

	surface := avito.SurfaceMobile
	if *surfaceFlag == "desktop" {
		surface = avito.SurfaceDesktop
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// One file, one format, both modes. Each port or session takes its own
	// entry, because an address is the thing the budget is counted per: N
	// ports sharing one IP rest that IP no better than one port would.
	var upstreams []blanktrail.Upstream
	var err error
	if *proxies != "" {
		upstreams, err = readProxies(ctx, *proxies)
		if err != nil {
			return err
		}
	}

	var leaser transport.Leaser
	switch *mode {
	case "blanktrail":
		apiKey := *key
		if apiKey == "" {
			apiKey = os.Getenv(keyEnv)
		}
		if apiKey == "" {
			return fmt.Errorf("blanktrail mode needs a key: -key or %s", keyEnv)
		}
		var channels []blanktrail.Channel
		if len(upstreams) > 0 {
			// One port, one proxy: the pool hands each freshly opened port the
			// next entry, so N ports carry N addresses rather than N sessions
			// behind one.
			channels = []blanktrail.Channel{
				blanktrail.NewListChannel("pool", blanktrail.NewStaticRotor(upstreams)),
			}
		}
		leaser, err = bt.Open(ctx, bt.Config{
			ControlURL: *control, APIKey: apiKey,
			Ports: *ports, Surface: surface,
			Channels: channels, Addresses: len(upstreams),
			// The proxy re-signs origins with its own MITM CA. Increment 3
			// pins it through Preflight; here the operator is at the terminal.
			Insecure: true,
		})
	case "direct":
		r := *root
		if r == "" {
			r = originOf(target)
		}
		leaser, err = direct.Open(direct.Config{
			Surface: surface, Sessions: *sessions,
			Root: r, StatePath: *statePath,
			Upstreams: upstreamURLs(upstreams),
		})
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		return err
	}
	defer leaser.Close()

	pacer := transport.NewPacer(transport.PacerConfig{
		Floor:   transport.DefaultFloor,
		Jitter:  transport.DefaultJitter,
		Ceiling: transport.DefaultCeiling,
		Window:  transport.DefaultWindow,
	})
	pacer.SetAddresses(leaser.Caps().Addresses)

	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{Surface: surface})
	res, ferr := f.Fetch(ctx, target)
	report(res, leaser, pacer)
	return ferr
}

// report prints one line of verdict and then the listings, in the same shape
// avitoparse prints, so the two are comparable at a glance.
func report(res transport.Result, leaser transport.Leaser, pacer *transport.Pacer) {
	b := pacer.Budget()
	fmt.Printf("mode=%s class=%s status=%d attempts=%d\n",
		leaser.Mode(), res.Verdict.Class, res.Status, res.Attempts)
	fmt.Printf("rule=%s path=%s listings=%d ok=%v empty-declared=%v",
		res.Page.Report.Rule, res.Page.Report.Path,
		len(res.Page.Listings), res.Page.Ok(), res.Page.EmptyDeclared)
	if res.Page.TotalCount != nil {
		fmt.Printf(" total=%d", *res.Page.TotalCount)
	}
	fmt.Println()
	fmt.Printf("session=%s label=%s\n", res.Session, res.Label)
	fmt.Printf("budget: %d req/hour per address × %d address(es) = about %d watches at 5 min\n",
		b.PerAddressPerHour, b.Addresses, b.WatchesAtFiveMinutes)
	if res.Verdict.Detail != "" {
		fmt.Println("detail:", res.Verdict.Detail)
	}
	if res.Page.CanonicalURL != "" {
		fmt.Println("canonical:", res.Page.CanonicalURL)
	}
	for _, w := range res.Page.Report.Warnings {
		fmt.Printf("warn  %s: %s\n", w.Code, w.Detail)
	}
	for i, l := range res.Page.Listings {
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

// originOf is the scheme and host of a URL, which is what a warm-up navigates
// to. Parsed by hand rather than with net/url so the command keeps the same
// shape as the rest of the tree, where a URL is a string until something needs
// its parts.
func originOf(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return raw
	}
	rest := raw[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return raw[:i+3] + rest + "/"
}

// wrap breaks a paragraph at a width, so the boundary message reads as prose in
// a terminal rather than as one long line.
func wrap(s string, width int) string {
	var out strings.Builder
	line := 0
	for _, word := range strings.Fields(s) {
		if line > 0 && line+1+len(word) > width {
			out.WriteByte('\n')
			line = 0
		} else if line > 0 {
			out.WriteByte(' ')
			line++
		}
		out.WriteString(word)
		line += len(word)
	}
	return out.String()
}

// readProxies loads the egress list. The format is the SDK's, so one file
// serves both modes and neither grows a parser of its own.
//
// A file that was named and yielded nothing is an error rather than a silent
// fall back to the host's own address: the reason to pass -proxies is that
// leaving from one IP is not what was wanted, and doing it anyway while the
// budget counts otherwise is how an installation gets banned.
func readProxies(ctx context.Context, path string) ([]blanktrail.Upstream, error) {
	src := blanktrail.Source{Kind: "file", Location: path, DefaultScheme: "socks5"}
	ups, bad, err := src.Load(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bad {
		fmt.Fprintf(os.Stderr, "warn  unparsable proxy line: %s\n", b)
	}
	if len(ups) == 0 {
		return nil, fmt.Errorf("%s yielded no usable proxies", path)
	}
	return ups, nil
}

func upstreamURLs(ups []blanktrail.Upstream) []string {
	if len(ups) == 0 {
		return nil
	}
	out := make([]string, 0, len(ups))
	for _, u := range ups {
		out = append(out, u.URL())
	}
	return out
}
