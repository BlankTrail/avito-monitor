// SPDX-License-Identifier: AGPL-3.0-or-later

// avitosoak walks one query repeatedly through the real ladder and writes down
// what happened, so that the throughput figures in this repository are measured
// rather than extrapolated.
//
// It is not a load test and it is not a benchmark. It exists to answer three
// questions that currently have no answer: whether a warm session holds at the
// configured spacing and for how many requests, whether several warm identities
// on one address beat one, and what honest number replaces "300-700".
//
// It never runs unattended. The operator watches it, and it stops on the first
// sign that continuing would be rude to the target.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	"github.com/BlankTrail/avito-monitor/internal/transport/bt"
	"github.com/BlankTrail/avito-monitor/internal/transport/direct"
)

const keyEnv = "AVITOMON_BLANKTRAIL_KEY"

// blockSize is the reporting granularity. Ten is small enough that a wall shows
// up in the block it happened in, and large enough that one bad request does
// not read as a collapse.
const blockSize = 10

// maxConsecutiveBad ends an arm when the target has plainly stopped answering
// this address. Three is enough to tell a single refusal from a wall, and small
// enough that a wall costs minutes rather than hours: each exhausted fetch
// spends about fifteen minutes climbing the backoff ladder.
const maxConsecutiveBad = 3

// record is one request. The field names are the JSONL schema; changing one
// invalidates every file already written, so they are named for what they are
// rather than for what this run happens to call them.
type record struct {
	N        int     `json:"n"`
	At       string  `json:"at"`
	Elapsed  float64 `json:"elapsed_s"`
	Class    string  `json:"class"`
	Status   int     `json:"status"`
	Attempts int     `json:"attempts"`
	Session  string  `json:"session"`
	Label    string  `json:"label"`
	Rule     string  `json:"rule"`
	Path     string  `json:"path"`
	Listings int     `json:"listings"`
	Total    *int64  `json:"total,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Err      string  `json:"error,omitempty"`
	// Trace is every attempt inside this fetch, "class/status" each. It is
	// recorded because Class above is the FINAL verdict: a request the site
	// blocked and that then succeeded after a renewal records "ok", and the
	// push-back would be invisible. The first live soak run had exactly one
	// such request, and it read as untroubled.
	Trace []string `json:"trace,omitempty"`
	// Pushback is true when the site refused at least once inside this fetch,
	// whatever the outcome. It is the honest denominator for a ban rate.
	Pushback bool `json:"pushback,omitempty"`
}

// good is what counts as the site having answered. An empty page is a fine
// answer — the market really can be empty — and everything else is not.
func (r record) good() bool {
	return r.Class == string(transport.ClassOK) || r.Class == string(transport.ClassEmpty)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("avitosoak", flag.ExitOnError)
	mode := fs.String("mode", "direct", "direct|blanktrail")
	arm := fs.String("arm", "", "name of this arm, used for the output filenames")
	requests := fs.Int("requests", 100, "how many requests to attempt")
	spacing := fs.Duration("spacing", transport.DefaultFloor, "floor between two requests on ONE identity")
	// The default ceiling is stricter than the default floor: 60 per 10 minutes
	// is 6/min, while a 3.5 s floor allows 17/min. Left alone, a soak measures
	// our own safety limit rather than the site's tolerance — and the first
	// question this run exists to answer is about the site. Pass a large number
	// to stress the floor; leave it alone to verify the shipped budget is safe.
	// Zero is not "off": the Pacer reads a non-positive ceiling as "use the
	// default", so unlimited is spelled as a number nothing will reach.
	ceiling := fs.Int("ceiling", transport.DefaultCeiling, "max requests per address per window")
	window := fs.Duration("window", transport.DefaultWindow, "the ceiling's window")
	surfaceFlag := fs.String("surface", "mobile", "mobile|desktop")
	sessions := fs.Int("sessions", 1, "direct: warm identities to hold — the second question this run answers")
	ports := fs.Int("ports", 2, "blanktrail: ports to open")
	control := fs.String("control", "http://127.0.0.1:8891", "BlankTrail control API")
	key := fs.String("key", "", "BlankTrail API key (or "+keyEnv+")")
	proxies := fs.String("proxies", "", "file of egress proxies, one per line")
	statePath := fs.String("state", "", "direct: where identities are persisted between arms")
	out := fs.String("out", ".", "directory for the JSONL")
	noRenewFlag := fs.Bool("no-renew", false, "experiment: make RenewIdentity a "+
		"no-op, so a block is met with the ladder's wait and nothing else — "+
		"this is what separates 'the pause healed it' from 'the new identity did'")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("exactly one URL is required")
	}
	target := fs.Arg(0)
	if *arm == "" {
		*arm = *mode
	}

	surface := avito.SurfaceMobile
	if *surfaceFlag == "desktop" {
		surface = avito.SurfaceDesktop
	}

	// Interrupt stops the run and still writes the summary. A soak abandoned
	// halfway with no output would have annoyed the target for nothing.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var upstreams []blanktrail.Upstream
	var err error
	if *proxies != "" {
		if upstreams, err = readProxies(ctx, *proxies); err != nil {
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
			channels = []blanktrail.Channel{
				blanktrail.NewListChannel("pool", blanktrail.NewStaticRotor(upstreams)),
			}
		}
		leaser, err = bt.Open(ctx, bt.Config{
			ControlURL: *control, APIKey: apiKey,
			Ports: *ports, Surface: surface,
			Channels: channels, Addresses: len(upstreams),
			Pace: *spacing, Insecure: true,
		})
	case "direct":
		leaser, err = direct.Open(direct.Config{
			Surface: surface, Sessions: *sessions,
			Root: originOf(target), StatePath: *statePath,
			Upstreams: upstreamURLs(upstreams),
		})
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		return err
	}
	defer leaser.Close()

	if *noRenewFlag {
		leaser = noRenew{leaser}
		fmt.Println("EXPERIMENT: RenewIdentity is a no-op; a block is met with " +
			"the wait alone, on the identity the site refused")
	}

	pacer := transport.NewPacer(transport.PacerConfig{
		Floor:   *spacing,
		Jitter:  transport.DefaultJitter,
		Ceiling: *ceiling,
		Window:  *window,
	})
	pacer.SetAddresses(leaser.Caps().Addresses)
	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{Surface: surface})

	path := fmt.Sprintf("%s/soak-%s.jsonl", strings.TrimRight(*out, "/\\"), *arm)
	jf, err := os.Create(path)
	if err != nil {
		return err
	}
	defer jf.Close()
	enc := json.NewEncoder(jf)

	fmt.Printf("arm=%s mode=%s surface=%s requests=%d spacing=%s ceiling=%d/%s sessions=%d addresses=%d\n",
		*arm, *mode, *surfaceFlag, *requests, *spacing, *ceiling, *window,
		*sessions, leaser.Caps().Addresses)
	fmt.Printf("writing %s — watch this; it is not meant to run unattended\n\n", path)

	started := time.Now()
	var recs []record
	var stopped string
	consecutiveBad := 0

	for i := 1; i <= *requests; i++ {
		t0 := time.Now()
		res, ferr := f.Fetch(ctx, target)
		r := record{
			N: i, At: t0.UTC().Format(time.RFC3339), Elapsed: time.Since(t0).Seconds(),
			Class: string(res.Verdict.Class), Status: res.Status, Attempts: res.Attempts,
			Session: res.Session, Label: res.Label,
			Rule: res.Page.Report.Rule, Path: string(res.Page.Report.Path),
			Listings: len(res.Page.Listings), Total: res.Page.TotalCount,
			Detail: res.Verdict.Detail,
		}
		for _, st := range res.Trace {
			r.Trace = append(r.Trace, fmt.Sprintf("%s/%d", st.Class, st.Status))
			if st.Remedy != transport.RemedyNone {
				r.Pushback = true
			}
		}
		if ferr != nil {
			r.Err = ferr.Error()
		}
		recs = append(recs, r)
		if err := enc.Encode(r); err != nil {
			return err
		}
		note := ""
		if r.Pushback {
			note = "  PUSHBACK " + strings.Join(r.Trace, " -> ")
		}
		fmt.Printf("%4d  %-11s %3d  %2d listings  %6.1fs  %s%s\n",
			r.N, r.Class, r.Status, r.Listings, r.Elapsed,
			shortSession(r.Session), note)

		if ctx.Err() != nil {
			stopped = "interrupted by the operator"
			break
		}
		var halt *transport.HaltError
		if errors.As(ferr, &halt) {
			// The wall in mode (b). Continuing past it is the thing the ladder
			// refuses to do, and a measurement harness has even less excuse.
			stopped = fmt.Sprintf("halted at request %d: %s (status %d, marker %q)",
				i, halt.Verdict.Class, halt.Status, halt.Verdict.Wall.Marker)
			break
		}
		if r.Class == string(transport.ClassChallenge) {
			stopped = fmt.Sprintf("challenge at request %d — this transport cannot pass one", i)
			break
		}
		if ferr != nil && !r.good() {
			stopped = fmt.Sprintf("gave up at request %d: %v", i, ferr)
			break
		}
		// A run of refusals means the target has stopped answering this
		// address, and the ladder exhausting its backoffs returns no error to
		// say so — it just costs about fifteen minutes per fetch. Without this,
		// the first four-jar arm spent two hours and forty minutes hammering an
		// address that had already said no, which is precisely what this
		// harness promised not to do.
		if !r.good() {
			consecutiveBad++
			if consecutiveBad >= maxConsecutiveBad {
				stopped = fmt.Sprintf(
					"stopped at request %d after %d refusals in a row — the "+
						"address is not answering and nothing here can fix that",
					i, consecutiveBad)
				break
			}
		} else {
			consecutiveBad = 0
		}
	}

	summarise(os.Stdout, *arm, recs, time.Since(started), stopped)
	return nil
}

// summarise prints the thing the run is actually for. Blocks of ten, because a
// wall shows up in the block it happened in.
func summarise(w *os.File, arm string, recs []record, wall time.Duration, stopped string) {
	fmt.Fprintf(w, "\n--- %s: %d requests in %s ---\n", arm, len(recs), wall.Round(time.Second))
	if len(recs) == 0 {
		return
	}

	fmt.Fprintf(w, "\n%-10s %6s %6s %s\n", "block", "good", "bad", "classes")
	for start := 0; start < len(recs); start += blockSize {
		end := start + blockSize
		if end > len(recs) {
			end = len(recs)
		}
		var good int
		classes := map[string]int{}
		for _, r := range recs[start:end] {
			if r.good() {
				good++
			} else {
				classes[r.Class]++
			}
		}
		fmt.Fprintf(w, "%-10s %6d %6d %s\n",
			fmt.Sprintf("%d-%d", start+1, end), good, (end-start)-good, tally(classes))
	}

	var good, pushed int
	classes := map[string]int{}
	identities := map[string]int{}
	labels := map[string]int{}
	var firstBad, firstPush int
	for _, r := range recs {
		classes[r.Class]++
		if r.Pushback {
			pushed++
			if firstPush == 0 {
				firstPush = r.N
			}
		}
		identities[r.Session]++
		labels[r.Label]++
		if r.good() {
			good++
		} else if firstBad == 0 {
			firstBad = r.N
		}
	}

	fmt.Fprintf(w, "\ngood            %d/%d (%.1f%%)\n",
		good, len(recs), 100*float64(good)/float64(len(recs)))
	fmt.Fprintf(w, "classes         %s (FINAL verdicts)\n", tally(classes))
	fmt.Fprintf(w, "pushback        %d/%d fetches met a refusal at least once\n",
		pushed, len(recs))
	if firstPush > 0 {
		fmt.Fprintf(w, "first pushback  request %d\n", firstPush)
	} else {
		fmt.Fprintf(w, "first pushback  none in %d requests\n", len(recs))
	}
	fmt.Fprintf(w, "identities      %d distinct, %d requests each on average\n",
		len(identities), len(recs)/max(1, len(identities)))
	fmt.Fprintf(w, "egress labels   %d distinct\n", len(labels))
	if firstBad > 0 {
		fmt.Fprintf(w, "first bad       request %d\n", firstBad)
	} else {
		fmt.Fprintf(w, "first bad       none in %d requests — this is a FLOOR, not a limit\n", len(recs))
	}
	fmt.Fprintf(w, "rate            %.1f requests/minute sustained\n",
		float64(len(recs))/wall.Minutes())
	if stopped != "" {
		fmt.Fprintf(w, "stopped         %s\n", stopped)
	}
}

func tally(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

// shortSession keeps the identity readable in a live log. The srv_id is long
// and only its head is needed to see that it changed.
func shortSession(s string) string {
	if i := strings.Index(s, "/"); i >= 0 && len(s) > i+9 {
		return s[:i+9] + "…"
	}
	return s
}

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
