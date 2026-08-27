// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// watchResumeCmd clears a halt, which is a person's act and never the
// program's.
//
// It re-tests nothing and promises nothing: if the wall is still there the next
// pass halts again with a fresh reason. That costs one request and buys a
// truthful answer, which is a better trade than a program deciding on its own
// that a block has lifted and resuming into it all night.
//
// Without this the most likely terminal state of a direct-mode install after a
// year — one captcha, one halted watch — would have no exit but editing the
// database by hand.
func watchResumeCmd(args []string) error {
	fs := flag.NewFlagSet("watch resume", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "файл базы")
	all := fs.Bool("all", false, "снять остановку со всех наблюдений")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	var ids []int64
	switch {
	case *all:
		if ids, err = s.HaltedWatches(ctx); err != nil {
			return err
		}
	case fs.NArg() == 1:
		id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("%q — не номер наблюдения", fs.Arg(0))
		}
		ids = []int64{id}
	default:
		return errors.New("usage: avitomon watch resume <id> | --all")
	}
	if len(ids) == 0 {
		fmt.Println("остановленных наблюдений нет")
		return nil
	}

	now := time.Now().Unix()
	resumed := 0
	for _, id := range ids {
		ok, err := s.ResumeWatch(ctx, id, now)
		if err != nil {
			return err
		}
		if ok {
			resumed++
			fmt.Printf("наблюдение %d возобновлено\n", id)
			continue
		}
		fmt.Printf("наблюдение %d не было остановлено\n", id)
	}
	if resumed > 0 {
		fmt.Println("Ничего не проверено: если стена на месте, " +
			"следующий проход снова остановится и скажет почему.")
	}
	return nil
}
