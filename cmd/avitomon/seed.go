// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// seedRules creates the default rules for a new watch, and the global health
// rules if nothing has created them yet.
//
// Nothing is created when there is nobody to tell. A rule with no target cannot
// pass Validate — deliberately, because a rule that cannot fire is worse than
// no rule: its owner believes they are covered. So the seeding waits for a
// target rather than storing nine rules that are all broken in the same way.
func seedRules(ctx context.Context, s *store.Store, watchID int64) ([]string, error) {
	targets, err := s.EnabledTargets(ctx)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, nil
	}

	existing, err := rules.All(ctx, s)
	if err != nil {
		return nil, err
	}
	made := rules.DefaultsForWatch(watchID, targets)
	// The five health rules are about the install rather than about a watch, so
	// they are created once. Keyed on "does a default health rule already
	// exist" rather than on the rules table being empty: somebody who deletes a
	// health rule they did not want must not get it back with their next watch.
	if !anyGlobalDefault(existing) {
		made = append(made, rules.GlobalDefaults(targets)...)
	}

	names := make([]string, 0, len(made))
	for _, r := range made {
		if _, err := rules.Save(ctx, s, r); err != nil {
			return nil, fmt.Errorf("правило %q: %w", r.Name, err)
		}
		names = append(names, r.Name)
	}
	return names, nil
}

func anyGlobalDefault(existing []rules.Rule) bool {
	for _, r := range existing {
		if r.FromDefaults && r.Scope.Kind == rules.ScopeFilter {
			return true
		}
	}
	return false
}
