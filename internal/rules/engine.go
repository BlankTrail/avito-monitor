// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Engine applies every rule to one change.
//
// Lifted in shape from WB_GO's Apply. The one thing it does that WB_GO's does
// not have to is treat a change it cannot resolve as still worth recording —
// see record.
type Engine struct {
	Store      *store.Store
	Suppressor Suppressor

	// Render turns a change into the body a person reads. A function rather
	// than an import so that internal/rules never learns what Telegram is: the
	// engine's job ends at "this should be sent".
	Render func(store.ChangeRow) string

	// Now is the clock. Nil falls back to time.Now.
	Now func() time.Time
}

// Apply returns how many messages were queued.
//
// A rule that does not match leaves no trace at all, and that is not the same
// as a suppression. A rule of the wrong kind was never about this change;
// writing a row for it would fill rule_events with the cartesian product of
// every rule and every change, and bury the rows that answer «почему мне не
// сказали».
func (e *Engine) Apply(ctx context.Context, all []Rule, ev Event) (int, error) {
	queued := 0
	for _, r := range all {
		if !r.Matches(ev) {
			continue
		}
		d := e.Suppressor.Decide(r, ev)
		eventID, err := e.record(ctx, r, ev, d)
		if err != nil {
			return queued, err
		}
		if !d.Send {
			continue
		}
		targets, err := e.targetsFor(ctx, r)
		if err != nil {
			return queued, err
		}
		body := e.render(ev.Change)
		for _, target := range targets {
			at := e.now().Unix()
			if err := e.Store.Enqueue(ctx, store.OutboxRow{
				TargetID: target, RuleEventID: &eventID,
				CreatedAt: at, DueAt: at, Body: body,
			}); err != nil {
				return queued, fmt.Errorf("rules: queue message: %w", err)
			}
			queued++
		}
	}
	return queued, nil
}

// targetsFor is the rule's own list intersected with the ones still switched
// on, so that disabling a target silences it everywhere at once rather than
// requiring an edit to every rule that names it.
//
// A rule whose targets have all been switched off still records its decision as
// a firing rather than as a suppression: nothing about the change stopped it,
// and calling that a suppression would put a reason in the column that is not
// the reason.
func (e *Engine) targetsFor(ctx context.Context, r Rule) ([]int64, error) {
	enabled, err := e.Store.EnabledTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("rules: targets: %w", err)
	}
	on := make(map[int64]bool, len(enabled))
	for _, id := range enabled {
		on[id] = true
	}
	out := make([]int64, 0, len(r.Targets))
	for _, id := range r.Targets {
		if on[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (e *Engine) record(ctx context.Context, r Rule, ev Event, d Decision) (int64, error) {
	subject := Subject(ev)

	// change_id is left null when the change has no row of its own yet, rather
	// than the firing being dropped. Retention removes old changes and a firing
	// must outlive the change it rests on; a decision that could only be
	// recorded alongside a surviving change would be lost exactly when somebody
	// went looking for why they were told something last month.
	var changeID *int64
	if ev.Change.ID != 0 {
		id := ev.Change.ID
		changeID = &id
	}

	id, err := e.Store.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: r.ID, ChangeID: changeID, FiredAt: e.now().Unix(),
		Kind:        string(ev.Change.Kind),
		SubjectKind: string(subject.Kind), SubjectKey: subject.Key,
		Field:        ev.Change.Field,
		SuppressedBy: string(d.Reason), DedupKey: d.DedupKey,
	})
	if err != nil {
		return 0, fmt.Errorf("rules: record decision: %w", err)
	}
	return id, nil
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// render falls back to the kind rather than to an empty body. A message with
// nothing in it is worse than an ugly one: the person cannot tell whether the
// monitor is broken or the listing is.
func (e *Engine) render(c store.ChangeRow) string {
	if e.Render == nil {
		return string(c.Kind)
	}
	return e.Render(c)
}
