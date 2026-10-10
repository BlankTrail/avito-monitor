// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fortyChanges is what a busy pass on a broad watch looks like.
func fortyChanges() []Event {
	out := make([]Event, 0, 40)
	for i := 0; i < 40; i++ {
		ev := priceEvent()
		ev.Change.ItemID = 7800000000 + int64(i)
		ev.Change.ThreadID = 5000 + int64(i)
		ev.Change.Now = fmt.Sprintf("%d", 3500000-i*1000)
		out = append(out, ev)
	}
	return out
}

func applyAll(t *testing.T, e *Engine, rs []Rule, evs []Event) int {
	t.Helper()
	ctx := context.Background()
	total := 0
	for _, ev := range evs {
		n, err := e.Apply(ctx, rs, ev)
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	n, err := e.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return total + n
}

// TestFortyChangesBecomeOneMessage.
//
// Spec section 6.3's «вместо сорока сообщений одно». A monitor that sends forty
// messages in one pass is switched off on the second day, and the aggregating
// flag is the setting that says so — until now it was stored, read back, and
// acted on by nothing.
func TestFortyChangesBecomeOneMessage(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	if got := applyAll(t, engineOn(t, s), saved, fortyChanges()); got != 1 {
		t.Errorf("queued %d messages for forty changes, want 1", got)
	}
	if n := outboxCount(t, s); n != 1 {
		t.Errorf("notify_outbox has %d rows, want 1", n)
	}
}

// TestAggregationChangesTheMessagesAndNotTheRecord.
//
// Every one of the forty is still a decision, and rule_events is what answers
// «почему мне не сказали» — or in this case «почему мне сказали». Folding forty
// decisions into one row would lose exactly the detail the summary omits.
func TestAggregationChangesTheMessagesAndNotTheRecord(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	applyAll(t, engineOn(t, s), saved, fortyChanges())
	if rows := ruleEvents(t, s); len(rows) != 40 {
		t.Errorf("rule_events has %d rows, want 40", len(rows))
	}
}

// TestANonAggregatingRuleStillSpeaksPerChange, so switching the flag on is a
// choice a person makes rather than the only behaviour there is.
func TestANonAggregatingRuleStillSpeaksPerChange(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Targets = []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	evs := fortyChanges()[:3]
	if got := applyAll(t, engineOn(t, s), saved, evs); got != 3 {
		t.Errorf("queued %d for three changes, want 3", got)
	}
}

// TestTwoAggregatingRulesGetOneMessageEach. They are two subscriptions, and
// merging them would tell one person about the other's rule.
func TestTwoAggregatingRulesGetOneMessageEach(t *testing.T) {
	s := openStore(t)
	tgt := addTarget(t, s)

	first := priceRule()
	first.Aggregate, first.Targets = true, []int64{tgt}
	second := priceRule()
	// A fresh id, or the two would be one rule saved twice — and the holding is
	// keyed on the rule id, so they would share a summary.
	second.ID = 0
	second.Name, second.Aggregate, second.Targets = "тоже цена", true, []int64{tgt}
	saved := persist(t, s, first, second)

	if got := applyAll(t, engineOn(t, s), saved, fortyChanges()); got != 2 {
		t.Errorf("queued %d, want one per rule", got)
	}
}

// TestFlushIsSafeToCallWithNothingHeld, which is every quiet pass.
func TestFlushIsSafeToCallWithNothingHeld(t *testing.T) {
	s := openStore(t)
	e := engineOn(t, s)
	for i := 0; i < 2; i++ {
		n, err := e.Flush(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("Flush queued %d with nothing held", n)
		}
	}
}

// TestFlushingTwiceDoesNotSendTwice. A caller that flushes defensively at the
// end of a pass and again on shutdown must not double every summary.
func TestFlushingTwiceDoesNotSendTwice(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	e := engineOn(t, s)
	for _, ev := range fortyChanges() {
		if _, err := e.Apply(ctx, saved, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := e.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the second Flush queued %d", n)
	}
	if got := outboxCount(t, s); got != 1 {
		t.Errorf("notify_outbox has %d rows, want 1", got)
	}
}

// TestASuppressedChangeIsNotAggregatedEither. Aggregation happens after the
// suppression, so a seeding pass produces one summary of nothing rather than a
// summary of thirty listings nobody wanted to hear about.
func TestASuppressedChangeIsNotAggregatedEither(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	evs := fortyChanges()
	for i := range evs {
		evs[i].Change.Backfill = true
	}
	if got := applyAll(t, engineOn(t, s), saved, evs); got != 0 {
		t.Errorf("queued %d for a seeding pass, want 0", got)
	}
}

// TestASummaryPointsAtNoSingleDecision.
//
// It is the outcome of forty of them, and pointing the row at one would make
// the other thirty-nine look as though nothing was sent about them.
func TestASummaryPointsAtNoSingleDecision(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)
	applyAll(t, engineOn(t, s), saved, fortyChanges())

	var ruleEventID *int64
	if err := s.DB().QueryRow(
		`SELECT rule_event_id FROM notify_outbox LIMIT 1`).Scan(&ruleEventID); err != nil {
		t.Fatal(err)
	}
	if ruleEventID != nil {
		t.Errorf("the summary points at decision %d of forty", *ruleEventID)
	}
}

// TestTheFallbackSummaryIsWorseThanASummaryAndBetterThanSilence.
//
// An Engine with no Summarise is a caller's oversight. Sending nothing for
// every aggregating rule would be the wrong way to report it.
func TestTheFallbackSummaryIsWorseThanASummaryAndBetterThanSilence(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	e := engineOn(t, s) // no Summarise
	applyAll(t, e, saved, fortyChanges())

	var body string
	if err := s.DB().QueryRow(`SELECT body FROM notify_outbox LIMIT 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body == "" {
		t.Fatal("the summary is empty")
	}
	if !strings.Contains(body, "40") {
		t.Errorf("body = %q; it does not say how many changes it covers", body)
	}
}

// TestTheSummaryIsWhatTheCallerWrote, because the wording is not this package's
// business — the same reason Render is a function rather than an import.
func TestTheSummaryIsWhatTheCallerWrote(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	e := engineOn(t, s)
	e.Summarise = func(r Rule, firings []Firing) string {
		return fmt.Sprintf("%s: %d штук", r.Name, len(firings))
	}
	applyAll(t, e, saved, fortyChanges())

	var body string
	if err := s.DB().QueryRow(`SELECT body FROM notify_outbox LIMIT 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "цена упала: 40 штук" {
		t.Errorf("body = %q", body)
	}
}

// TestOneEngineIsOnePass. A second Engine over the same store starts with
// nothing held, which is what makes the pass the aggregation boundary rather
// than the lifetime of the process.
func TestOneEngineIsOnePass(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)

	all := fortyChanges()
	applyAll(t, engineOn(t, s), saved, all[:20])
	// A second Engine, and twenty changes it has not seen — so the
	// deduplication does not swallow them and what is left to observe is
	// whether the second pass summarises on its own.
	applyAll(t, engineOn(t, s), saved, all[20:])

	if n := outboxCount(t, s); n != 2 {
		t.Errorf("two passes produced %d summaries, want one each", n)
	}
}

// TestASingleFiringIsAMessageNotASummaryOfOne.
//
// Arrivals and vanishings aggregate by default since 11 October 2026, and a
// quiet watch's one arrival must not lose its own wording and its decision for
// a «summary» of one line.
func TestASingleFiringIsAMessageNotASummaryOfOne(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Aggregate, r.Targets = true, []int64{addTarget(t, s)}
	saved := persist(t, s, r)
	if got := applyAll(t, engineOn(t, s), saved, fortyChanges()[:1]); got != 1 {
		t.Fatalf("queued %d", got)
	}
	var ruleEventID *int64
	if err := s.DB().QueryRow(`SELECT rule_event_id FROM notify_outbox LIMIT 1`).Scan(&ruleEventID); err != nil {
		t.Fatal(err)
	}
	if ruleEventID == nil {
		t.Error("one firing went out as a summary pointing at nothing")
	}
}
