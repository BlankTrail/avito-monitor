// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// enrichRoot is the origin a direct session warms up against.
//
// The site's own front page and not a listing's: the warm-up mints an identity
// by navigating to the host it is about to read, and every page this command
// reads is on www.avito.ru whatever the listing.
const enrichRoot = "https://www.avito.ru/"

// enrichCmd fills the seller for listings collected from a surface that does
// not carry one.
//
// A separate command and not a step inside `run`, on purpose. A pass is the
// product working and this is the product improving; sharing a command would
// let a slow enrichment delay a scheduled pass, and the request improvement
// spent would be the one collection needed.
//
// The listings it fixes came from the mobile surface, which carries no seller,
// no category, no location and no coordinates — measured at 0 of 30. Desktop is
// what a new watch is now offered, so this queue shrinks over time rather than
// growing; it exists for the mobile watches somebody deliberately kept and for
// everything collected before the default changed.
func enrichCmd(args []string) error {
	fs := flag.NewFlagSet("enrich", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	limit := fs.Int("limit", 20, "how many listings to fill in one run")
	key := fs.String("key", "", "BlankTrail API key (or "+keyEnv+", or the settings row)")
	control := fs.String("control", "", "BlankTrail control API (default: the settings row)")
	proxies := fs.String("proxies", "", "file of egress proxies, one per line")
	statePath := fs.String("state", "", "direct: where identities are persisted "+
		"(default: beside the database)")
	timeout := fs.Duration("timeout", 10*time.Minute, "the whole run's budget")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// The queue first, before anything is opened. Enrichment with nothing to do
	// must not mint an identity, open a port or spend a warm-up request — this
	// command is meant to be safe to run on a timer that mostly finds nothing.
	candidates, err := s.ListingsNeedingSeller(ctx, *limit)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		fmt.Println("дополнять нечего: у всех объявлений есть продавец")
		return nil
	}

	mode, err := s.TransportMode(ctx)
	if err != nil {
		return err
	}
	leaser, err := openLeaser(ctx, s, mode, store.Watch{
		URL: enrichRoot, Surface: avito.SurfaceDesktop,
	}, leaserOpts{
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

	// The desktop surface, always. The whole reason a listing is in this queue
	// is that the surface it was collected from carries no seller, and asking
	// that surface again would spend a request to learn nothing.
	f := transport.NewFetcher(leaser, pacer, transport.FetchConfig{
		Surface: avito.SurfaceDesktop,
	})

	done, empty, failed := 0, 0, 0
	for _, c := range candidates {
		// Before the fetch, not after. A row that cannot be written is a
		// request spent for nothing, and the request is the scarce thing.
		if err := s.CheckSpace(ctx); err != nil {
			return err
		}
		res, ferr := f.Fetch(ctx, "https://www.avito.ru"+c.URLPath)
		if ferr != nil {
			failed++
			fmt.Printf("объявление %d: %v\n", c.ItemID, ferr)
			// A halt stops the whole run. Enrichment is the lowest-priority
			// traffic there is, and spending further requests into a wall
			// would burn the address collection needs.
			var halt *transport.HaltError
			if errors.As(ferr, &halt) {
				fmt.Printf("остановлено: %s\n", halt.Verdict.Wall.Marker)
				break
			}
			continue
		}

		var found *avito.Listing
		for i := range res.Page.Listings {
			if res.Page.Listings[i].ItemID == c.ItemID {
				found = &res.Page.Listings[i]
				break
			}
		}
		if found == nil || found.SellerKey == "" {
			// The row stays in the queue. A later attempt may read a page that
			// does carry a seller, and the per-run limit is what stops this
			// becoming a spin.
			empty++
			continue
		}
		if err := s.ApplyEnrichment(ctx, c.ItemID, *found, time.Now().Unix()); err != nil {
			return err
		}
		done++
	}

	waiting, keyed, total, err := s.EnrichmentQueue(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("дополнено %d, страница без продавца %d, не открылось %d\n",
		done, empty, failed)
	fmt.Printf("осталось %d из %d; продавец известен у %d\n", waiting, total, keyed)
	return nil
}
