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
	rungs, err := telegramRungs(cfg, s.MTProtoSessionStore(), only)
	if err != nil {
		return nil, err
	}
	return &telegram.Ladder{Rungs: rungs}, nil
}

// telegramRungs is the ladder's rungs, in the order they are tried.
//
// By default the Bot API, and behind it MTProto once the my.telegram.org pair
// is set. The settings screen asks for that pair exactly when the Bot API has
// failed, so a ladder without MTProto would store the pair and go on failing:
// the messages would wait for an api.telegram.org that this network cannot
// reach, while the route the person configured for it stood unused. Without
// the pair the rung is left out rather than added to fail, because «mtproto:
// не настроен» would then be part of every error about a Bot API that is
// simply down.
//
// `-rung bot-api` or `-rung mtproto` asks for one rung alone, which is how
// either is tried on its own.
func telegramRungs(cfg store.TelegramConfig, session telegram.SessionStore, only string) ([]telegram.Sender, error) {
	bot := telegram.NewBot(cfg.BotToken, nil)
	mt := telegram.NewMTProto(telegram.MTConfig{
		APIID: cfg.APIID, APIHash: cfg.APIHash, BotToken: cfg.BotToken, Session: session,
	})
	switch strings.ToLower(only) {
	case "":
		if cfg.APIID != 0 && cfg.APIHash != "" {
			return []telegram.Sender{bot, mt}, nil
		}
		return []telegram.Sender{bot}, nil
	case "bot-api":
		return []telegram.Sender{bot}, nil
	case "mtproto":
		return []telegram.Sender{mt}, nil
	default:
		return nil, fmt.Errorf("способ %q эта сборка не знает: bot-api или mtproto", only)
	}
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

// sendDue is one turn of the outbox worker, for the supervised loop.
//
// The ladder is rebuilt each turn on purpose. A token typed into Настройки has
// to take effect without a restart, and a ladder held for the life of the
// process would keep using the one that was there at boot — which is exactly
// how a person ends up believing their new token did not save.
func sendDue(ctx context.Context, s *store.Store) error {
	// Checked before the ladder is built, not after. With no token every due
	// message would be attempted, fail, and burn one of its twelve retries —
	// so a fresh install that has not configured Telegram yet would give up on
	// its first day's messages before anybody typed a token.
	cfg, err := s.TelegramConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.BotToken == "" && os.Getenv(tokenEnv) == "" {
		return nil
	}
	sender, err := buildLadder(ctx, s, "")
	if err != nil {
		return err
	}
	w := &notify.Worker{
		Store: s, Max: 100, Now: time.Now,
		Transport: notify.TransportFunc(func(ctx context.Context, m notify.Message) error {
			return sender.SendMessage(ctx, m.Address, m.Body)
		}),
	}
	_, err = w.Run(ctx)
	return err
}

// pollCallbacks is one turn of the inline-button poller.
//
// The handler does exactly what the panel's «Это не перепубликация» does — pin,
// split, replay — because it is the same correction reached one screen earlier,
// and two spellings of one act is how they drift apart.
func pollCallbacks(ctx context.Context, s *store.Store, logf func(string, ...any)) error {
	cfg, err := s.TelegramConfig(ctx)
	if err != nil {
		return err
	}
	token := cfg.BotToken
	if v := os.Getenv(tokenEnv); v != "" {
		token = v
	}
	if token == "" {
		// Nothing configured. Not an error: the moment somebody saves a token
		// the next turn polls, and reporting it would drive the supervisor's
		// backoff on every fresh install.
		return nil
	}

	p := &telegram.Poller{
		Bot:     telegram.NewBot(token, nil),
		Allowed: s.TelegramChats,
		Offset:  s.CallbackOffset,
		Save:    s.SetCallbackOffset,
		Log:     logf,
		Handle: func(ctx context.Context, successor, predecessor int64) (string, error) {
			if err := s.PinPair(ctx, successor, predecessor,
				time.Now().Unix()); err != nil {
				return "", err
			}
			n, err := replayAppearance(ctx, s, successor)
			if err != nil {
				// The pin and the split already happened and are right. The
				// same trade the panel makes: a correction is not rolled back
				// because a message could not be queued.
				return "Связь снята, но сообщение поставить в очередь не удалось.", nil
			}
			if n == 0 {
				return "Связь снята. Сообщения не будет: под правила это объявление не подходит.", nil
			}
			return "Связь снята, история разделена, сообщение отправится заново.", nil
		},
	}
	return p.Turn(ctx)
}
