// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// transportCmd is the command-line half of decision 3.
//
// Increments 3, 4 and 5 all collect before the panel that would ask exists, so
// the question has to be answerable here — and it writes the same one column
// the first screen will write later, rather than becoming a second source of
// truth that has to be reconciled.
func transportCmd(args []string) error {
	fs := flag.NewFlagSet("transport", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: avitomon transport show|set direct|blanktrail")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	switch fs.Arg(0) {
	case "show":
		m, err := s.TransportMode(ctx)
		if errors.Is(err, store.ErrNoTransportChosen) {
			fmt.Println("no transport mode has been chosen yet")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Println(m)
		return nil

	case "set":
		if fs.NArg() != 2 {
			return errors.New("usage: avitomon transport set direct|blanktrail")
		}
		if err := s.SetTransportMode(ctx, transport.Mode(fs.Arg(1))); err != nil {
			return err
		}
		fmt.Printf("transport mode is now %s\n", fs.Arg(1))
		return nil

	default:
		return fmt.Errorf("unknown transport subcommand %q", fs.Arg(0))
	}
}

func volumeCmd(args []string) error {
	fs := flag.NewFlagSet("volume", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	v, err := s.Volume(ctx)
	if err != nil {
		return err
	}
	fmt.Print(v.String())
	return nil
}
