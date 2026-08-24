// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/notify"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/telegram"
)

// tokenEnv is where the Telegram bot token comes from when it is not in the
// database. Like the BlankTrail key, it is never written to a file this program
// creates, never logged, and never included in an error.
const tokenEnv = "AVITOMON_TELEGRAM_TOKEN"

func notifyCmd(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "файл базы")
	test := fs.Bool("test", false, "отправить одно сообщение и проверить путь")
	chat := fs.String("chat", "", "-test: в какой чат")
	rung := fs.String("rung", "", "-test: только этот способ (bot-api|mtproto)")
	max := fs.Int("max", 100, "сколько сообщений разослать за один проход")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	sender, err := buildLadder(ctx, s, *rung)
	if err != nil {
		return err
	}

	if *test {
		if *chat == "" {
			return errors.New("для -test нужен -chat")
		}
		if err := sender.SendMessage(ctx, *chat,
			"Avito Monitor: проверка связи."); err != nil {
			return err
		}
		fmt.Printf("отправлено через %s\n", sender.Current())
		return nil
	}

	w := &notify.Worker{
		Store: s, Max: *max, Now: time.Now,
		Transport: notify.TransportFunc(func(ctx context.Context, m notify.Message) error {
			return sender.SendMessage(ctx, m.Address, m.Body)
		}),
	}
	st, err := w.Run(ctx)
	if err != nil {
		return err
	}
	path := sender.Current()
	if path == "" {
		path = "ничего не отправлялось"
	}
	fmt.Printf("%s (путь: %s)\n", st, path)
	return nil
}

// buildLadder assembles the delivery ladder from what is configured.
//
// MTProto is deliberately absent from the default ladder. Its live exchange has
// not been verified against real Telegram — see docs/measurements/ — and a
// delivery path nobody has seen work is worse than an absent one, because the
// settings screen would offer it and somebody would rely on it. `-rung mtproto`
// is how it gets tried, which is the verification itself.
func buildLadder(ctx context.Context, s *store.Store, only string) (*telegram.Ladder, error) {
	cfg, err := s.TelegramConfig(ctx)
	if err != nil {
		return nil, err
	}
	// The environment overrides the database, so a token can be tried without
	// first writing it anywhere.
	if v := os.Getenv(tokenEnv); v != "" {
		cfg.BotToken = v
	}

	var rungs []telegram.Sender
	switch strings.ToLower(only) {
	case "", "bot-api":
		rungs = append(rungs, telegram.NewBot(cfg.BotToken, nil))
	case "mtproto":
		rungs = append(rungs, telegram.NewMTProto(telegram.MTConfig{
			APIID: cfg.APIID, APIHash: cfg.APIHash, BotToken: cfg.BotToken,
			Session: s.MTProtoSessionStore(),
		}))
	default:
		return nil, fmt.Errorf("способ %q эта сборка не знает: bot-api или mtproto", only)
	}
	return &telegram.Ladder{Rungs: rungs}, nil
}

// targetCmd manages who gets told.
func targetCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("avitomon target list|add")
	}
	fs := flag.NewFlagSet("target", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "файл базы")
	name := fs.String("name", "", "add: как называется")
	kind := fs.String("kind", "telegram", "add: какого рода")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	switch args[0] {
	case "list":
		targets, err := s.Targets(ctx)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("получателей нет: avitomon target add <chat id>")
			return nil
		}
		for _, t := range targets {
			state := "включён"
			if !t.Enabled {
				state = "выключен"
			}
			fmt.Printf("%d  %s  %s  %s  (%s)\n", t.ID, t.Kind, t.Address, t.Name, state)
		}
		return nil

	case "add":
		if fs.NArg() == 0 {
			return errors.New("avitomon target add <chat id>")
		}
		address := fs.Arg(0)
		label := *name
		if label == "" {
			label = address
		}
		id, err := s.SaveTarget(ctx, store.TargetRow{
			Name: label, Kind: *kind, Address: address, Enabled: true,
		})
		if err != nil {
			return err
		}
		fmt.Printf("получатель %d добавлен: %s\n", id, address)
		return nil
	}
	return fmt.Errorf("avitomon target: не знаю команду %q", args[0])
}
