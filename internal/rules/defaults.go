// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"time"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// healthQuietFor is how often a health rule may say the same thing. Six hours,
// because an install that has been flagged should say so once rather than
// either never or continuously.
const healthQuietFor = 6 * time.Hour

// DefaultsForWatch is the four rules a new watch gets.
//
// Per watch and not one shared global copy, for two reasons that pull the same
// way: a rule scoped to a watch that does not exist yet cannot be written in
// advance, and a shared rule would make deleting one watch silence another.
//
// Every one is editable, disableable and deletable like any rule a person
// wrote. FromDefaults is what lets the panel say which came from here, so that
// deleting one is an informed act rather than a surprise later.
func DefaultsForWatch(watchID int64, targets []int64) []Rule {
	scope := Scope{Kind: ScopeWatch, ID: watchID}
	return []Rule{
		{
			Name: "Новое объявление", Kind: track.KindListingAppeared,
			// No threshold and no rate limit, and neither is an oversight. The
			// dedup key for this kind carries no numbers, so it fires at most
			// once per listing ever; a floor and a limit would both be dead
			// configuration a person still has to read and reason about.
			//
			// Aggregated: a busy query brings twenty in one pass, and twenty
			// messages a pass is how a monitor gets muted. Found on 11 October
			// 2026 adding rentals and car dealers to a real installation. One
			// arrival in a pass still goes out as its own message — see Flush.
			Aggregate: true,
			Condition: Node{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Изменилась цена", Kind: track.KindPriceChanged,
			// Keyed on the thread by DedupKey, so a repost at the same price is
			// not news, and floored here so a rounding move is not either. Both
			// numbers, because a percentage alone lets a cheap listing shout
			// and a minor-unit floor alone lets an expensive one whisper.
			ThresholdPct: 1, ThresholdMinor: 10_000,
			Condition: Node{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Объявление пропало", Kind: track.KindListingVanished,
			// Enabled per decision 5. The message is prefixed «предположительно»
			// — see internal/telegram/message.go — which is what makes an
			// enabled-by-default removal alert honest rather than sloppy.
			// Aggregated for the same reason as arrivals.
			Aggregate: true,
			Condition: Node{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Объявление закрыто", Kind: track.KindListingClosed,
			// The confirmation of the third, carrying confidence 1.0 because an
			// item page stated it rather than a window implying it.
			Condition: Node{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
	}
}

// GlobalDefaults is the five health rules, created on first run.
//
// They are scoped to a filter rather than to a watch because they are about the
// install: "my collector has been flagged" is not a fact about one query, and
// scoping it to a watch would mean either five rules per watch or a person
// wondering why only one watch reported the outage.
func GlobalDefaults(targets []int64) []Rule {
	health := []struct {
		name string
		kind track.Kind
	}{
		{"Транспорт заблокирован", track.KindTransportBlocked},
		{"Извлечение деградировало", track.KindExtractionFellBack},
		{"Разметка изменилась", track.KindCollectorShapeUnknown},
		{"Сбор молчит", track.KindCollectorSilent},
		{"Воркер упал", track.KindWorkerPanicked},
	}
	out := make([]Rule, 0, len(health))
	for _, h := range health {
		out = append(out, Rule{
			Name: h.name, Kind: h.kind,
			// The zero Node is an empty And, which is true: this rule fires on
			// every change of its kind, which is what a health alert is.
			Condition: Node{},
			// An empty filter, which covers everything. The plan set
			// IncludeUnpriced here on the grounds that a transport change has no
			// price; it is not needed, because that flag only decides what a
			// price BAND does with a change it cannot price, and there is no
			// band. Setting it would be a flag that reads as load-bearing and
			// is not.
			Scope:       Scope{Kind: ScopeFilter, Filter: Filter{}},
			MinInterval: healthQuietFor,
			Targets:     targets, Enabled: true, FromDefaults: true,
		})
	}
	return out
}
