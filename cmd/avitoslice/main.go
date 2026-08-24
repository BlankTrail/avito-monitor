// SPDX-License-Identifier: AGPL-3.0-or-later

// avitoslice asks which listings each identity is shown.
//
// It exists because the first two passes ever written to a database disagreed
// with each other completely: same URL, minutes apart, thirty listings each,
// zero in common. The result set turned out to depend on the identity, which
// nothing in the measured surface had said and nothing in the design had
// assumed.
//
// That finding leaves three questions the absence machinery cannot be written
// without, and this answers all three from one place:
//
//   - does one identity keep seeing its own slice, or does the slice drift
//     inside a session too?
//   - do two ports alive at the same time see two different markets? If they
//     do, a watch must be pinned to a port, and the pool stops being a
//     throughput lever for monitoring.
//   - does the desktop surface behave the same way?
//
// It holds ONE pool open across every pass, which is the whole point: opening
// a fresh one per pass is what produced the confounded reading in the first
// place.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	"github.com/BlankTrail/avito-monitor/internal/transport/bt"
	"github.com/BlankTrail/avito-monitor/internal/transport/direct"
)

const keyEnv = "AVITOMON_BLANKTRAIL_KEY"

// reading is one pass: who asked, and what they were shown.
type reading struct {
	n       int
	session string
	label   string
	egress  string
	ids     map[int64]bool
	order   []int64
	class   string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("avitoslice", flag.ExitOnError)
	mode := fs.String("mode", "direct", "direct|blanktrail")
	surfaceFlag := fs.String("surface", "mobile", "mobile|desktop")
	passes := fs.Int("passes", 6, "how many fetches to make through the one pool")
	gap := fs.Duration("gap", 0, "extra pause between fetches, on top of the pacer's floor")
	ports := fs.Int("ports", 2, "blanktrail: ports in the pool")
	sessions := fs.Int("sessions", 2, "direct: identities in the pool")
	control := fs.String("control", "http://127.0.0.1:8891", "BlankTrail control API")
	key := fs.String("key", "", "BlankTrail API key (or "+keyEnv+")")
	statePath := fs.String("state", "", "direct: where identities are persisted")
	timeout := fs.Duration("timeout", 30*time.Minute, "the whole run")
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

	var leaser transport.Leaser
	var err error
	switch *mode {
	case "blanktrail":
		apiKey := *key
		if apiKey == "" {
			apiKey = os.Getenv(keyEnv)
		}
		if apiKey == "" {
			return fmt.Errorf("blanktrail mode needs a key: -key or %s", keyEnv)
		}
		leaser, err = bt.Open(ctx, bt.Config{
			ControlURL: *control, APIKey: apiKey,
			Ports: *ports, Surface: surface, Insecure: true,
		})
	case "direct":
		leaser, err = direct.Open(direct.Config{
			Surface: surface, Sessions: *sessions,
			Root: originOf(target), StatePath: *statePath,
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
		Ceiling: 100000, // the site's tolerance is not what this run is asking about
		Window:  transport.DefaultWindow,
	})
	pacer.SetAddresses(leaser.Caps().Addresses)
	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{Surface: surface})

	fmt.Printf("mode=%s surface=%s passes=%d ports/sessions=%d gap=%s\n\n",
		*mode, *surfaceFlag, *passes, max(*ports, *sessions), *gap)

	var rs []reading
	for i := 1; i <= *passes; i++ {
		res, ferr := f.Fetch(ctx, target)
		r := reading{
			n: i, session: res.Session, label: res.Label, egress: res.Egress,
			class: string(res.Verdict.Class), ids: map[int64]bool{},
		}
		for _, l := range res.Page.Listings {
			r.ids[l.ItemID] = true
			r.order = append(r.order, l.ItemID)
		}
		rs = append(rs, r)
		fmt.Printf("%2d  %-11s %2d listings  %-22s %s\n",
			i, r.class, len(r.ids), short(r.label), short(r.session))
		if ferr != nil {
			fmt.Printf("      %v\n", ferr)
		}
		if *gap > 0 && i < *passes {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(*gap):
			}
		}
	}
	report(rs)
	return nil
}

// report is the whole output: who saw what, and how much of it they shared.
func report(rs []reading) {
	fmt.Println("\noverlap matrix (shared listings / the smaller set)")
	fmt.Printf("%6s", "")
	for _, b := range rs {
		fmt.Printf("%6d", b.n)
	}
	fmt.Println()
	for _, a := range rs {
		fmt.Printf("%6d", a.n)
		for _, b := range rs {
			fmt.Printf("%6s", ratio(a, b))
		}
		fmt.Println()
	}

	// Grouped by endpoint, because the question is whether the endpoint is what
	// decides the slice.
	byLabel := map[string][]reading{}
	for _, r := range rs {
		byLabel[r.label] = append(byLabel[r.label], r)
	}
	labels := make([]string, 0, len(byLabel))
	for l := range byLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	fmt.Println("\nwithin one endpoint, and across endpoints:")
	var sameSum, sameN, crossSum, crossN float64
	for i, a := range rs {
		for j, b := range rs {
			if j <= i {
				continue
			}
			v := overlapFraction(a, b)
			if a.label == b.label {
				sameSum, sameN = sameSum+v, sameN+1
			} else {
				crossSum, crossN = crossSum+v, crossN+1
			}
		}
	}
	if sameN > 0 {
		fmt.Printf("  same endpoint   %.0f%% mean overlap over %.0f pairs\n", 100*sameSum/sameN, sameN)
	}
	if crossN > 0 {
		fmt.Printf("  different one   %.0f%% mean overlap over %.0f pairs\n", 100*crossSum/crossN, crossN)
	}
	if sameN > 0 && crossN > 0 {
		fmt.Println("\nIf those two numbers differ, the endpoint decides the slice, and a")
		fmt.Println("watch that takes whichever endpoint is rested is differencing two")
		fmt.Println("different markets against each other.")
	}

	for _, l := range labels {
		fmt.Printf("\n%s\n", l)
		for _, r := range byLabel[l] {
			fmt.Printf("  pass %-3d %s\n", r.n, headOf(r.order, 4))
		}
	}
}

func overlapFraction(a, b reading) float64 {
	smaller := len(a.ids)
	if len(b.ids) < smaller {
		smaller = len(b.ids)
	}
	if smaller == 0 {
		return 0
	}
	shared := 0
	for id := range a.ids {
		if b.ids[id] {
			shared++
		}
	}
	return float64(shared) / float64(smaller)
}

func ratio(a, b reading) string {
	if a.n == b.n {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", 100*overlapFraction(a, b))
}

func headOf(ids []int64, n int) string {
	if len(ids) < n {
		n = len(ids)
	}
	parts := make([]string, 0, n)
	for _, id := range ids[:n] {
		parts = append(parts, fmt.Sprint(id))
	}
	return strings.Join(parts, " ")
}

func short(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}

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
