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

	// Summarise turns everything one aggregating rule fired on in this pass
	// into a single message — «сорок объявлений подешевели» instead of forty
	// messages.
	//
	// Optional in the same sense Render is. An aggregating rule with no
	// Summarise falls back to rendering the first firing with a count in front
	// of it, which is a worse message than a real summary and a much better one
	// than silence.
	Summarise func(r Rule, firings []Firing) string

	// Now is the clock. Nil falls back to time.Now.
	Now func() time.Time

	// held is what the aggregating rules have fired on so far, by rule id.
	// Nothing reaches the queue from here until Flush.
	held map[int64][]Firing
	// order is the rule ids in the order they first fired, so that two passes
	// over the same data queue the same messages in the same order.
	order []int64
}

// Firing is one match that got through, kept for the summary.
type Firing struct {
	Rule    Rule
	Event   Event
	EventID int64
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
		if r.Aggregate {
			// Held, not queued. The rule said its firings should arrive as one
			// message, and the boundary that decides what "one message" covers
			// is the pass — see Flush.
			e.hold(r, ev, eventID)
			continue
		}
		n, err := e.queue(ctx, r, e.render(ev.Change), &eventID)
		if err != nil {
			return queued, err
		}
		queued += n
	}
	return queued, nil
}

func (e *Engine) hold(r Rule, ev Event, eventID int64) {
	if e.held == nil {
		e.held = make(map[int64][]Firing)
	}
	if _, ok := e.held[r.ID]; !ok {
		e.order = append(e.order, r.ID)
	}
	e.held[r.ID] = append(e.held[r.ID], Firing{Rule: r, Event: ev, EventID: eventID})
}

// Flush queues one message per aggregating rule that fired, and returns how
// many messages that came to.
//
// It must be called once at the end of a pass. One Engine is one pass, and that
// is what makes the aggregation boundary meaningful: «вместо сорока сообщений
// одно» needs somewhere to count the forty, and the pass is the only boundary
// that corresponds to anything a person would recognise.
//
// Calling it twice queues nothing the second time.
func (e *Engine) Flush(ctx context.Context) (int, error) {
	queued := 0
	for _, ruleID := range e.order {
		firings := e.held[ruleID]
		if len(firings) == 0 {
			continue
		}
		r := firings[0].Rule
		n, err := e.queue(ctx, r, e.summarise(r, firings), nil)
		if err != nil {
			return queued, err
		}
		queued += n
	}
	e.held, e.order = nil, nil
	return queued, nil
}

// queue puts one body in front of every enabled target this rule names.
//
// eventID is nil for a summary, and that is deliberate rather than lazy: a
// summary is the outcome of many decisions, and pointing the row at one of
// forty would make the other thirty-nine look as though nothing was sent about
// them. The rule_events rows all exist, all carry the rule id, and all sit in
// the same minute; the panel finds them that way.
func (e *Engine) queue(ctx context.Context, r Rule, body string, eventID *int64) (int, error) {
	targets, err := e.targetsFor(ctx, r)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, target := range targets {
		at := e.now().Unix()
		if err := e.Store.Enqueue(ctx, store.OutboxRow{
			TargetID: target, RuleEventID: eventID,
			CreatedAt: at, DueAt: at, Body: body,
		}); err != nil {
			return queued, fmt.Errorf("rules: queue message: %w", err)
		}
		queued++
	}
	return queued, nil
}

// summarise falls back to the first firing with a count in front of it.
//
// Worse than a real summary, and much better than silence — which is what a nil
// Summarise would otherwise mean for every aggregating rule.
func (e *Engine) summarise(r Rule, firings []Firing) string {
	if e.Summarise != nil {
		return e.Summarise(r, firings)
	}
	first := e.render(firings[0].Event.Change)
	if len(firings) == 1 {
		return first
	}
	return fmt.Sprintf("%s: изменений — %d\n\n%s", r.Name, len(firings), first)
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
