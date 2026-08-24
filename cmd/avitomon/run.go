// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
	"github.com/BlankTrail/avito-monitor/internal/transport/bt"
	"github.com/BlankTrail/avito-monitor/internal/transport/direct"
)

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	once := fs.Bool("once", false, "take one due watch, fetch it, write the pass, stop")
	dbPath := fs.String("db", "avito.db", "database file")
	key := fs.String("key", "", "BlankTrail API key (or "+keyEnv+", or the settings row)")
	control := fs.String("control", "", "BlankTrail control API (default: the settings row)")
	proxies := fs.String("proxies", "", "file of egress proxies, one per line")
	statePath := fs.String("state", "", "direct: where identities are persisted "+
		"(default: beside the database)")
	timeout := fs.Duration("timeout", 5*time.Minute, "a refused fetch can spend minutes on its remedy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*once {
		return errors.New("only --once exists today; the scheduler is increment 4")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// Decision 3: no default. Until somebody has answered, collection refuses
	// to start rather than choosing on their behalf whether their traffic goes
	// through a paid proxy.
	mode, err := s.TransportMode(ctx)
	if errors.Is(err, store.ErrNoTransportChosen) {
		fmt.Fprintln(os.Stderr,
			"No transport has been chosen. This decides whether your requests go\n"+
				"out from this machine directly or through BlankTrail, which is a\n"+
				"paid service, and it is not a decision this program makes for you.\n\n"+
				"  avitomon transport set direct      — free, and stops at a captcha\n"+
				"  avitomon transport set blanktrail  — passes captchas, 299 RUB/month")
		os.Exit(2)
	}
	if err != nil {
		return err
	}

	due, err := s.DueWatches(ctx, time.Now().Unix(), 1)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		fmt.Println("nothing is due")
		return nil
	}
	w := due[0]

	// The warm-up root is the watch's own origin: a direct session mints its
	// identity by navigating to the host it is about to read, and navigating to
	// a different one would earn a clearance for the wrong place.
	leaser, err := openLeaser(ctx, s, mode, w, leaserOpts{
		control: *control, key: *key, proxyFile: *proxies, statePath: *statePath,
	})
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
	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{Surface: w.Surface})

	return runOnce(ctx, s, f, w)
}

// runOnce is one watch, one fetch, one pass.
func runOnce(ctx context.Context, s *store.Store, f *transport.Fetcher, w store.Watch) error {
	// Before the fetch, not after. A pass that cannot be written is a request
	// spent for nothing, and the request is the scarce thing here.
	if err := s.CheckSpace(ctx); err != nil {
		return err
	}

	started := time.Now().Unix()
	res, ferr := f.Fetch(ctx, w.URL)
	finished := time.Now().Unix()

	p := store.Pass{
		WatchID: w.ID, StartedAt: started, FinishedAt: finished,
		Page: 1, Surface: w.Surface,
		Class: string(res.Verdict.Class), HTTPStatus: res.Status,
		Path: string(res.Page.Report.Path), Mode: string(f.Mode()),
		Session: res.Session, Egress: res.Egress,
		ElapsedMS: (finished - started) * 1000,
		Bytes:     int64(len(res.Body)),
		// evidential gets its real admissibility tests in increment 4, where
		// the lifecycle that depends on them lives. Here it is the honest
		// floor: a class that answered.
		Evidential: res.Verdict.Class == transport.ClassOK ||
			res.Verdict.Class == transport.ClassEmpty,
		TotalCount: res.Page.TotalCount,
	}
	if ferr != nil {
		p.Error = ferr.Error()
	}
	saved, err := s.SavePass(ctx, p, res.Page.Listings)
	if err != nil {
		return err
	}
	fmt.Printf("watch %d  pass %d  class=%s  listings=%d  arrivals=%d  snapshots=%d\n",
		w.ID, saved.PassID, p.Class, len(res.Page.Listings),
		saved.Arrivals, saved.Snapshots)

	var halt *transport.HaltError
	if errors.As(ferr, &halt) {
		// The watch stays halted, and this program never clears it.
		if err := s.MarkHalted(ctx, w.ID, halt.Verdict.Wall.Marker, finished); err != nil {
			return err
		}
		return ferr
	}
	// The schedule advances on any completed pass, whatever its class. A watch
	// that stops being scheduled because it failed once is a watch that never
	// recovers.
	return s.SetNextDue(ctx, w.ID, started+int64(w.IntervalSec))
}

// leaserOpts are the command-line overrides, kept in one struct so adding the
// next one does not grow a parameter list nobody can read.
type leaserOpts struct {
	control   string
	key       string
	proxyFile string
	statePath string
}

// openLeaser builds the transport the settings row chose.
//
// The BlankTrail key comes from the flag, then the environment, then the
// settings column — in that order, so a person running one command by hand does
// not have to write theirs into the database first.
func openLeaser(ctx context.Context, s *store.Store, mode transport.Mode,
	w store.Watch, o leaserOpts,
) (transport.Leaser, error) {
	var upstreams []blanktrail.Upstream
	if o.proxyFile != "" {
		var err error
		if upstreams, err = readProxies(ctx, o.proxyFile); err != nil {
			return nil, err
		}
	}

	switch mode {
	case transport.ModeDirect:
		// The identity is persisted by default, and that is not a convenience.
		//
		// Measured 2026-08-24: Avito's result set depends on srv_id. Two passes
		// of the same date-sorted URL minutes apart, on two fresh identities,
		// returned thirty listings each with ZERO overlap; the same two passes
		// on one persisted identity overlapped thirty of thirty. A monitor that
		// mints a new identity per run therefore sees a complete turnover every
		// tick — thirty arrivals and thirty disappearances, for ever — and
		// every alert it sends is false.
		state := o.statePath
		if state == "" {
			state = s.Path() + ".identities.json"
		}
		return direct.Open(direct.Config{
			Surface: w.Surface, Sessions: 1,
			Root:      originOf(w.URL),
			StatePath: state,
			Upstreams: upstreamURLs(upstreams),
		})

	case transport.ModeBlankTrail:
		cfg, err := s.BlankTrailConfig(ctx)
		if err != nil {
			return nil, err
		}
		if o.key != "" {
			cfg.APIKey = o.key
		} else if env := os.Getenv(keyEnv); env != "" {
			cfg.APIKey = env
		}
		if o.control != "" {
			cfg.ControlURL = o.control
		}
		if cfg.ControlURL == "" {
			cfg.ControlURL = "http://127.0.0.1:8891"
		}
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("blanktrail mode needs a key: -key, %s, "+
				"or the settings row", keyEnv)
		}
		if cfg.Ports <= 0 {
			cfg.Ports = 2
		}
		var channels []blanktrail.Channel
		if len(upstreams) > 0 {
			channels = []blanktrail.Channel{
				blanktrail.NewListChannel("pool", blanktrail.NewStaticRotor(upstreams)),
			}
		}
		return bt.Open(ctx, bt.Config{
			ControlURL: cfg.ControlURL, APIKey: cfg.APIKey,
			Ports: cfg.Ports, Surface: w.Surface,
			Channels: channels, Addresses: len(upstreams),
			Insecure: true,
		})

	default:
		return nil, fmt.Errorf("unknown transport mode %q", mode)
	}
}
