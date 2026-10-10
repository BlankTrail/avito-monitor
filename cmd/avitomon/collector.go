// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/collect"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/supervise"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// collector owns the transport for the life of the process.
//
// One leaser and one pacer, built once and shared by every watch. This is the
// single load-bearing line of the whole increment.
//
// `avitomon run --once` builds both per invocation for one watch, which is
// correct for one watch and wrong for a scheduler: a pacer per watch would give
// each its own token bucket, and the ceiling internal/web/budget.go computes —
// «N addresses is N times the ceiling, and one address is one ceiling however
// many ports are open» — would be a number the product itself disproves on the
// first tick. The budget screen would keep promising a rate the collector had
// already exceeded, and the first anybody would know of it is a ban.
type collector struct {
	store *store.Store
	pacer *transport.Pacer
	opts  leaserOpts

	// One leaser per surface, opened when a watch on that surface first needs
	// it. Found on 11 October 2026: the process held ONE leaser whose profile
	// came from whichever watch was first — a mobile one — so a desktop watch
	// on PlayStation 5 was served through iOS ports, received the mobile page
	// and stored thirty listings without sellers as a desktop reading. Item
	// pages for the confirmation sweep, always desktop, went the same way.
	// The surface is a port-profile decision (bt.Spec), not a header one, so it
	// cannot be decided per request on a shared pool.
	mu      sync.Mutex
	leasers map[avito.Surface]transport.Leaser
}

// openCollector opens the transport for the first watch's surface, which also
// proves the configuration works; the other surface opens when first needed.
func openCollector(ctx context.Context, s *store.Store, opts leaserOpts) (*collector, error) {
	if _, err := s.TransportMode(ctx); err != nil {
		return nil, err
	}
	surface := avito.SurfaceMobile
	if ws, err := s.Watches(ctx); err == nil && len(ws) > 0 && ws[0].Surface != "" {
		surface = ws[0].Surface
	}
	c := &collector{
		store: s, opts: opts,
		leasers: map[avito.Surface]transport.Leaser{},
		pacer: transport.NewPacer(transport.PacerConfig{
			Floor:   transport.DefaultFloor,
			Jitter:  transport.DefaultJitter,
			Ceiling: transport.DefaultCeiling,
			Window:  transport.DefaultWindow,
		}),
	}
	if _, err := c.leaserFor(ctx, surface); err != nil {
		return nil, err
	}
	return c, nil
}

// leaserFor is the surface's leaser, opened on first use. The pacer is shared:
// both surfaces leave through the same addresses, and the per-address ceiling
// is the site's, whichever profile asks.
func (c *collector) leaserFor(ctx context.Context, surface avito.Surface) (transport.Leaser, error) {
	if surface == "" {
		surface = avito.SurfaceMobile
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.leasers[surface]; ok {
		return l, nil
	}
	mode, err := c.store.TransportMode(ctx)
	if err != nil {
		return nil, err
	}
	// The warm-up root is the surface's own host: a direct session mints its
	// identity by navigating to the host it is about to read.
	root := store.Watch{URL: "https://m.avito.ru/", Surface: surface}
	if surface == avito.SurfaceDesktop {
		root.URL = "https://www.avito.ru/"
	}
	l, err := openLeaser(ctx, c.store, mode, root, c.opts)
	if err != nil {
		return nil, err
	}
	c.leasers[surface] = l
	if n := l.Caps().Addresses; n > 0 {
		c.pacer.SetAddresses(n)
	}
	return l, nil
}

// Pass reads one watch.
//
// A Fetcher per pass and not per process, because the surface is the watch's
// and not the installation's. It costs nothing: a Fetcher holds no state of its
// own — only the two shared things and a config — so building one is three
// assignments.
func (c *collector) Pass(ctx context.Context, w store.Watch) error {
	l, err := c.leaserFor(ctx, w.Surface)
	if err != nil {
		return err
	}
	f := transport.NewFetcher(l, c.pacer,
		transport.FetchConfig{Surface: w.Surface})
	return runOnce(ctx, c.store, f, w)
}

// Confirm reads one vanished listing's own page.
//
// The desktop surface, always: item pages are the one thing that surface is
// wired for and their data-marker set is the one that was measured. ok reports
// whether the page yielded a listing at all — which is not the same as the
// listing being alive, and the store treats the two differently.
func (c *collector) Confirm(ctx context.Context, x store.Confirmable) (avito.Listing, bool, error) {
	l, err := c.leaserFor(ctx, avito.SurfaceDesktop)
	if err != nil {
		return avito.Listing{}, false, err
	}
	f := transport.NewFetcher(l, c.pacer,
		transport.FetchConfig{Surface: avito.SurfaceDesktop})
	res, err := f.Fetch(ctx, "https://www.avito.ru"+x.URLPath)
	if err != nil {
		return avito.Listing{}, false, err
	}
	for _, l := range res.Page.Listings {
		if l.ItemID == x.ItemID {
			return l, true, nil
		}
	}
	// The page opened and did not contain this listing. Not evidence of
	// anything: the surface may have changed, or the fetch may have been
	// redirected somewhere else entirely, and either way it is a page we did
	// not read rather than a listing we did.
	return avito.Listing{}, false, nil
}

// Close releases the transport. Once, when the process stops.
func (c *collector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	for _, l := range c.leasers {
		if err := l.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// startCollecting runs the scheduler and the three background workers that
// already existed as one-shot calls, each under supervision.
//
// All five the spec names.
//
// The confirmation sweeper is the one that spends requests on its own
// initiative, and its priority against the scheduler is settled by its own
// hourly cap rather than by anything here: five requests an hour, a constant
// and not a setting. It shares the same pacer as everything else, so those five
// queue behind collection like any other request rather than jumping it.
func startCollecting(ctx context.Context, s *store.Store, c *collector) {
	onPanic := func(name, stack string) {
		// A panic is a row. The whole reason internal/supervise exists is that
		// a crash in a background goroutine is otherwise an explanation in a
		// file nobody has open.
		if err := s.RecordWorkerPanic(ctx, name, stack, time.Now().Unix()); err != nil {
			fmt.Println("supervise: не удалось записать падение:", err)
		}
	}
	logf := func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}

	sch := &collect.Scheduler{Store: s, Passer: c, Now: time.Now, Log: logf}
	supervise.Go(ctx, supervise.Config{
		Name: "сборщик", Every: 15 * time.Second, Run: sch.Turn,
		OnPanic: onPanic, Log: logf,
	})
	supervise.Go(ctx, supervise.Config{
		Name: "почтальон", Every: 10 * time.Second, OnPanic: onPanic, Log: logf,
		Run: func(ctx context.Context) error { return sendDue(ctx, s) },
	})
	supervise.Go(ctx, supervise.Config{
		Name: "серии", Every: time.Minute, OnPanic: onPanic, Log: logf,
		Run: func(ctx context.Context) error {
			_, err := s.ProduceSeries(ctx)
			return err
		},
	})
	sweeper := &collect.Sweeper{
		Store: s, Confirmer: c, Now: time.Now, Log: logf,
	}
	supervise.Go(ctx, supervise.Config{
		// Every ten minutes, not every hour. The cap is enforced inside the
		// sweeper against a rolling hour, so a shorter interval spends the same
		// five requests — it just spreads them out and picks up a listing that
		// vanished five minutes ago while the site still has its page.
		Name: "подтверждение", Every: 10 * time.Minute,
		OnPanic: onPanic, Log: logf, Run: sweeper.Turn,
	})
	// The sixth goroutine, and the only inbound one. The spec names five
	// because when it was written nothing reached into this program from
	// outside; «Это не перепубликация» on the message itself is what added a
	// direction, and it is supervised like the rest.
	supervise.Go(ctx, supervise.Config{
		// Twice a minute. getUpdates long-polls for twenty-five seconds and
		// returns the moment anything arrives, so this interval is the gap
		// between polls and not the delay before a press is noticed.
		Name: "нажатия", Every: 30 * time.Second, OnPanic: onPanic, Log: logf,
		Run: func(ctx context.Context) error { return pollCallbacks(ctx, s, logf) },
	})
	supervise.Go(ctx, supervise.Config{
		Name: "уборка", Every: 6 * time.Hour, OnPanic: onPanic, Log: logf,
		Run: func(ctx context.Context) error {
			_, err := s.Reclaim(ctx, time.Now().Unix(), 0)
			return err
		},
	})
}
