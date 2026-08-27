// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"time"

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
	store  *store.Store
	leaser transport.Leaser
	pacer  *transport.Pacer
}

// openCollector builds the transport once.
func openCollector(ctx context.Context, s *store.Store, opts leaserOpts) (*collector, error) {
	mode, err := s.TransportMode(ctx)
	if err != nil {
		return nil, err
	}
	// The warm-up root is a watch's origin, and a direct session mints its
	// identity by navigating to the host it is about to read. Every watch this
	// process reads is on Avito, so the first due watch's own URL is as good a
	// root as any — and when there is none yet, the site's front page is.
	root := store.Watch{URL: "https://m.avito.ru/", Surface: ""}
	if due, err := s.Watches(ctx); err == nil && len(due) > 0 {
		root = due[0]
	}
	leaser, err := openLeaser(ctx, s, mode, root, opts)
	if err != nil {
		return nil, err
	}
	pacer := transport.NewPacer(transport.PacerConfig{
		Floor:   transport.DefaultFloor,
		Jitter:  transport.DefaultJitter,
		Ceiling: transport.DefaultCeiling,
		Window:  transport.DefaultWindow,
	})
	pacer.SetAddresses(leaser.Caps().Addresses)
	return &collector{store: s, leaser: leaser, pacer: pacer}, nil
}

// Pass reads one watch.
//
// A Fetcher per pass and not per process, because the surface is the watch's
// and not the installation's. It costs nothing: a Fetcher holds no state of its
// own — only the two shared things and a config — so building one is three
// assignments.
func (c *collector) Pass(ctx context.Context, w store.Watch) error {
	f := transport.NewFetcher(c.leaser, c.pacer,
		transport.FetchConfig{Surface: w.Surface})
	return runOnce(ctx, c.store, f, w)
}

// Close releases the transport. Once, when the process stops.
func (c *collector) Close() error { return c.leaser.Close() }

// startCollecting runs the scheduler and the three background workers that
// already existed as one-shot calls, each under supervision.
//
// Four of the five the spec names. The fifth — the confirmation sweeper — is
// not here: it is the only one that spends requests on its own initiative, and
// a second consumer of the same budget needs its own priority question answered
// before it competes with the scheduler for the same addresses.
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
	supervise.Go(ctx, supervise.Config{
		Name: "уборка", Every: 6 * time.Hour, OnPanic: onPanic, Log: logf,
		Run: func(ctx context.Context) error {
			_, err := s.Reclaim(ctx, time.Now().Unix(), 0)
			return err
		},
	})
}
