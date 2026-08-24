// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func changesCmd(args []string) error {
	fs := flag.NewFlagSet("changes", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	watch := fs.Int64("watch", 1, "which watch")
	since := fs.Duration("since", 24*time.Hour, "how far back to read")
	limit := fs.Int("limit", 200, "how many to print")
	series := fs.Bool("series", false, "run the out-of-band series producer first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	if *series {
		n, err := s.ProduceSeries(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("series producer wrote %d changes\n\n", n)
	}

	if ratio, ok, err := s.Saturation(ctx, *watch, 3); err != nil {
		return err
	} else if ok {
		// Printed before the list, because it says how much to trust it: at a
		// saturation near one the window turned over completely between polls
		// and whatever went through in between was never seen.
		fmt.Printf("saturation %.0f%% of the window turns over between polls\n\n",
			ratio*100)
	}

	rows, err := s.Changes(ctx, *watch, time.Now().Add(-*since).Unix(), *limit)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no changes")
		return nil
	}
	for _, c := range rows {
		mark := ""
		if c.Backfill {
			mark = "  [backfill]"
		}
		move := ""
		if c.Was != "" || c.Now != "" {
			move = fmt.Sprintf("  %s → %s", dash(c.Was), dash(c.Now))
		}
		fmt.Printf("%s  %-20s %-11d conf %.1f  %-9s%s%s\n",
			time.Unix(c.TS, 0).Format("2006-01-02 15:04"),
			c.Kind, c.ItemID, c.Confidence, c.Producer, move, mark)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
