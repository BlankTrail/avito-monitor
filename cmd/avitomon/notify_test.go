// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

func names(t *testing.T, cfg store.TelegramConfig, only string) []string {
	t.Helper()
	rungs, err := telegramRungs(cfg, nil, only)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rungs {
		out = append(out, r.Name())
	}
	return out
}

// TestTheBackgroundLadderFallsBackToMTProto.
//
// The settings screen asked for an api_id and an api_hash when the Bot API
// failed, and the messages still went only through the Bot API: the pair was
// stored and never used.
func TestTheBackgroundLadderFallsBackToMTProto(t *testing.T) {
	got := names(t, store.TelegramConfig{BotToken: "1:A", APIID: 7, APIHash: "h"}, "")
	if len(got) != 2 || got[0] != "bot api" || got[1] != "mtproto" {
		t.Errorf("rungs = %v, want [bot api mtproto]", got)
	}
}

func TestWithoutThePairTheLadderIsTheBotApiAlone(t *testing.T) {
	// A rung that can only answer «не настроен» would put that into every
	// error about a Bot API that is simply down.
	for _, cfg := range []store.TelegramConfig{
		{BotToken: "1:A"}, {BotToken: "1:A", APIID: 7}, {BotToken: "1:A", APIHash: "h"},
	} {
		if got := names(t, cfg, ""); len(got) != 1 || got[0] != "bot api" {
			t.Errorf("%+v: rungs = %v, want [bot api]", cfg, got)
		}
	}
}

func TestOneRungCanStillBeAskedForByName(t *testing.T) {
	cfg := store.TelegramConfig{BotToken: "1:A", APIID: 7, APIHash: "h"}
	if got := names(t, cfg, "mtproto"); len(got) != 1 || got[0] != "mtproto" {
		t.Errorf("rungs = %v, want [mtproto]", got)
	}
	if _, err := telegramRungs(cfg, nil, "pigeon"); err == nil {
		t.Error("an unknown rung was accepted")
	}
}
