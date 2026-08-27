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
)

// watchCmd adds one watch.
//
// The URL is stored verbatim. Parsing it into query, category_id and the rest
// is avito.SearchURL in increment 4, and the columns for it are already in the
// schema; inventing half of that parser here would be two parsers to keep in
// step with each other.
func watchCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: avitomon watch add|resume")
	}
	if args[0] == "resume" {
		return watchResumeCmd(args[1:])
	}
	if args[0] != "add" {
		return errors.New("usage: avitomon watch add [flags] <url> | " +
			"avitomon watch resume <id>|--all")
	}
	fs := flag.NewFlagSet("watch add", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	name := fs.String("name", "", "what to call it")
	surfaceFlag := fs.String("surface", "mobile", "mobile|desktop")
	interval := fs.Int("interval", 300, "seconds between passes (floor 120)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("exactly one URL is required")
	}
	// The link decides the kind, and a shop decides its own surface and page
	// count — the same rule the panel follows, applied here so the two paths
	// cannot produce different rows for the same URL. A private profile is
	// refused by name; anything unparseable is refused before a database is
	// even opened.
	parsed, err := avito.ParseSearch(fs.Arg(0))
	if err != nil {
		return err
	}
	surface, pages := avito.Surface(*surfaceFlag), 1
	if parsed.Kind == avito.KindSeller {
		surface = avito.SurfaceDesktop
	}

	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now().Unix()
	id, err := s.AddWatch(ctx, store.Watch{
		Name: *name, Kind: string(parsed.Kind), URL: fs.Arg(0),
		Surface: surface, MaxPages: pages,
		IntervalSec: *interval, AbsenceConfirmations: 2,
		AbsenceMinSpanSec: 1200,
		// Due immediately. A watch somebody has just added and that does
		// nothing for five minutes reads as broken.
		NextDueAt: now,
		Enabled:   true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	made, err := seedRules(ctx, s, id)
	if err != nil {
		return err
	}
	// Printed, because a rule a person did not know they had is a rule they
	// cannot turn off.
	fmt.Printf("наблюдение %d добавлено, правил по умолчанию: %d\n", id, len(made))
	for _, name := range made {
		fmt.Printf("  %s\n", name)
	}
	if len(made) == 0 {
		fmt.Println("  получателей нет, поэтому и правил нет: " +
			"avitomon target add <chat id>, затем avitomon watch add ещё раз")
	}
	return nil
}
