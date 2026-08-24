// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// This file is the only place a Rule and a stored row know about each other.
// The dependency points one way, and the side allowed to see both is this one:
// internal/store never learns what a condition tree is, and everything above
// works in Rules rather than in rows of JSON.

// Save writes a rule and returns its id.
//
// Validate runs first and its failure is returned rather than stored. A rule
// that cannot match anything is a scheduled disappointment: it sits in the list
// looking healthy, and its owner concludes the thing they are watching never
// changes.
func Save(ctx context.Context, s *store.Store, r Rule) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	condition, err := json.Marshal(r.Condition)
	if err != nil {
		return 0, fmt.Errorf("rules: save: condition: %w", err)
	}
	filter, err := json.Marshal(r.Scope.Filter)
	if err != nil {
		return 0, fmt.Errorf("rules: save: scope filter: %w", err)
	}
	targets, err := json.Marshal(r.Targets)
	if err != nil {
		return 0, fmt.Errorf("rules: save: targets: %w", err)
	}
	return s.SaveRule(ctx, store.RuleRow{
		ID:          r.ID,
		Name:        r.Name,
		EventKind:   string(r.Kind),
		Condition:   string(condition),
		ScopeKind:   string(r.Scope.Kind),
		ScopeID:     r.Scope.ID,
		ScopeFilter: string(filter),

		Urgent:            r.Urgent,
		ThresholdPct:      r.ThresholdPct,
		ThresholdMinor:    r.ThresholdMinor,
		ThresholdCurrency: "RUB",
		MinIntervalSec:    int(r.MinInterval / time.Second),
		Aggregate:         r.Aggregate,

		Targets:      string(targets),
		Enabled:      r.Enabled,
		FromDefaults: r.FromDefaults,
	})
}

// All reads every rule back.
//
// A rule the current build cannot understand is returned rather than skipped,
// with whatever it carries. Validate is what says it is unusable and the screen
// shows that; dropping it here would make a rule written by a newer release
// vanish from the list, and the person looking for it would write it again.
//
// Only a row whose JSON will not parse is an error, because that is not a rule
// this build disagrees with — it is not a rule.
func All(ctx context.Context, s *store.Store) ([]Rule, error) {
	rows, err := s.Rules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(rows))
	for _, row := range rows {
		r, err := fromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func fromRow(row store.RuleRow) (Rule, error) {
	r := Rule{
		ID:             row.ID,
		Name:           row.Name,
		Kind:           track.Kind(row.EventKind),
		Scope:          Scope{Kind: ScopeKind(row.ScopeKind), ID: row.ScopeID},
		Urgent:         row.Urgent,
		ThresholdPct:   row.ThresholdPct,
		ThresholdMinor: row.ThresholdMinor,
		MinInterval:    time.Duration(row.MinIntervalSec) * time.Second,
		Aggregate:      row.Aggregate,
		Enabled:        row.Enabled,
		FromDefaults:   row.FromDefaults,
	}
	if err := json.Unmarshal([]byte(row.Condition), &r.Condition); err != nil {
		return Rule{}, fmt.Errorf("rules: rule %d: condition: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.ScopeFilter), &r.Scope.Filter); err != nil {
		return Rule{}, fmt.Errorf("rules: rule %d: scope filter: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.Targets), &r.Targets); err != nil {
		return Rule{}, fmt.Errorf("rules: rule %d: targets: %w", row.ID, err)
	}
	return r, nil
}
