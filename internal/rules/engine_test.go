// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// engineOn builds an engine whose suppressor reads the real store, which is
// the arrangement production uses: the rate limit and the duplicate check are
// database questions, and a test that stubbed them would not exercise the
// queries they run.
func engineOn(t *testing.T, s *store.Store) *Engine {
	t.Helper()
	now := func() time.Time { return time.Unix(10_000, 0).UTC() }
	ctx := context.Background()
	return &Engine{
		Store: s, Now: now,
		Render: func(c store.ChangeRow) string { return string(c.Kind) },
		Suppressor: Suppressor{
			Now: now,
			LastFired: func(ruleID int64, p SubjectPair) (time.Time, bool) {
				at, ok, err := s.LastFired(ctx, ruleID, string(p.Kind), p.Key)
				if err != nil {
					t.Fatal(err)
				}
				return time.Unix(at, 0).UTC(), ok
			},
			SeenKey: func(ruleID int64, key string) bool {
				seen, err := s.SeenDedupKey(ctx, ruleID, key)
				if err != nil {
					t.Fatal(err)
				}
				return seen
			},
		},
	}
}

// persist writes the rules the way production has them: rule_events.rule_id is
// a foreign key onto rules(id), so a rule the engine is handed must exist. It
// also means these tests exercise Save and the round trip through the row,
// which nothing else does.
func persist(t *testing.T, s *store.Store, rs ...Rule) []Rule {
	t.Helper()
	out := make([]Rule, 0, len(rs))
	for _, r := range rs {
		id, err := Save(context.Background(), s, r)
		if err != nil {
			t.Fatal(err)
		}
		r.ID = id
		out = append(out, r)
	}
	return out
}

func addTarget(t *testing.T, s *store.Store) int64 {
	t.Helper()
	id, err := s.SaveTarget(context.Background(), store.TargetRow{
		Name: "мне", Kind: "telegram", Address: "42", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ruleEvents(t *testing.T, s *store.Store) []store.RuleEventRow {
	t.Helper()
	rows, err := s.RuleEvents(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func outboxCount(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM notify_outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestApplyQueuesOneMessagePerEnabledTarget.
func TestApplyQueuesOneMessagePerEnabledTarget(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, b := addTarget(t, s), addTarget(t, s)

	r := priceRule()
	r.Targets = []int64{a, b}

	queued, err := engineOn(t, s).Apply(ctx, persist(t, s, r), priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 2 {
		t.Errorf("queued %d, want one per target", queued)
	}
	if n := outboxCount(t, s); n != 2 {
		t.Errorf("notify_outbox has %d rows, want 2", n)
	}
}

// TestApplyWritesARuleEventForASuppressedFiringToo.
//
// «Почему мне не сказали» is the question rule_events.suppressed_by exists to
// answer, and a monitor that silently drops notifications cannot be debugged by
// the person who stopped receiving them.
func TestApplyWritesARuleEventForASuppressedFiringToo(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := addTarget(t, s)

	r := priceRule()
	r.Targets = []int64{tgt}
	r.ThresholdMinor = 100_000_000 // nothing clears this

	queued, err := engineOn(t, s).Apply(ctx, persist(t, s, r), priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("queued %d despite the threshold", queued)
	}
	rows := ruleEvents(t, s)
	if len(rows) != 1 {
		t.Fatalf("rule_events has %d rows, want 1 — a suppression is a fact "+
			"worth recording", len(rows))
	}
	if rows[0].SuppressedBy != string(ReasonThreshold) {
		t.Errorf("suppressed_by = %q, want %q", rows[0].SuppressedBy, ReasonThreshold)
	}
	if rows[0].DedupKey == "" {
		t.Error("the suppressed row carries no dedup key; the next decision " +
			"compares against it")
	}
}

// TestTheSameChangeAppliedTwiceQueuesOnce, which is what a restart mid-pass
// looks like.
func TestTheSameChangeAppliedTwiceQueuesOnce(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	r := priceRule()
	r.Targets = []int64{addTarget(t, s)}
	e := engineOn(t, s)
	saved := persist(t, s, r)

	if _, err := e.Apply(ctx, saved, priceEvent()); err != nil {
		t.Fatal(err)
	}
	queued, err := e.Apply(ctx, saved, priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("the same change queued %d messages the second time", queued)
	}
	if n := outboxCount(t, s); n != 1 {
		t.Errorf("notify_outbox has %d rows, want 1", n)
	}
	// And the second attempt is recorded as a duplicate rather than as nothing.
	rows := ruleEvents(t, s)
	if len(rows) != 2 || rows[1].SuppressedBy != string(ReasonDuplicate) {
		t.Errorf("the second attempt left %d rows, last suppressed_by %q",
			len(rows), rows[len(rows)-1].SuppressedBy)
	}
}

// TestTheDedupKeyIsScopedToTheRule. Two people watching the same market with
// two rules both get told; one rule's memory must not silence another's.
func TestTheDedupKeyIsScopedToTheRule(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := addTarget(t, s)

	first := priceRule()
	first.ID, first.Targets = 1, []int64{tgt}
	second := priceRule()
	second.ID, second.Name, second.Targets = 2, "тоже цена", []int64{tgt}

	queued, err := engineOn(t, s).Apply(ctx, persist(t, s, first, second), priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 2 {
		t.Errorf("queued %d, want one per rule", queued)
	}
}

// TestARuleThatDoesNotMatchLeavesNoTrace.
//
// A rule of the wrong kind is not a suppression — it was never about this
// change. Writing a rule_events row for it would fill the table with the
// cartesian product of every rule and every change, and bury the rows that
// answer «почему мне не сказали».
func TestARuleThatDoesNotMatchLeavesNoTrace(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	r := priceRule()
	r.Kind, r.Targets = track.KindListingVanished, []int64{addTarget(t, s)}

	if _, err := engineOn(t, s).Apply(ctx, persist(t, s, r), priceEvent()); err != nil {
		t.Fatal(err)
	}
	if rows := ruleEvents(t, s); len(rows) != 0 {
		t.Errorf("a rule of another kind left %d rows", len(rows))
	}
}

// TestARuleEventOutlivesTheChangeItRestsOn.
//
// change_id is nullable with no foreign key, for the same reason
// changes.pass_id is: retention removes old changes, and a firing must outlive
// the change it rests on rather than be cascaded away with it.
func TestARuleEventOutlivesTheChangeItRestsOn(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	r := priceRule()
	r.Targets = []int64{addTarget(t, s)}

	ev := priceEvent()
	ev.Change.ID = 999 // a change row that will not exist
	if _, err := engineOn(t, s).Apply(ctx, persist(t, s, r), ev); err != nil {
		t.Fatalf("a firing about a change we cannot resolve failed: %v", err)
	}
	if len(ruleEvents(t, s)) != 1 {
		t.Error("the firing was lost")
	}
}

// TestASwitchedOffTargetIsSilencedEverywhereAtOnce.
//
// The rule's own list is intersected with the targets still enabled, so that
// turning one off does not require an edit to every rule that names it — which
// is what somebody does when they go on holiday, and they will not remember to
// undo nine edits when they come back.
func TestASwitchedOffTargetIsSilencedEverywhereAtOnce(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	on, off := addTarget(t, s), addTarget(t, s)
	if _, err := s.SaveTarget(ctx, store.TargetRow{
		ID: off, Name: "в отпуске", Kind: "telegram", Address: "43", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	r := priceRule()
	r.Targets = []int64{on, off}

	queued, err := engineOn(t, s).Apply(ctx, persist(t, s, r), priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Errorf("queued %d, want one — the second target is switched off", queued)
	}
}

// TestARuleWithNobodyLeftToTellStillRecordsAFiring.
//
// Nothing about the change stopped it, so suppressed_by stays empty. Writing a
// reason there would put a reason in the column that is not the reason, and the
// person reading «почему мне не сказали» would be told the wrong thing.
func TestARuleWithNobodyLeftToTellStillRecordsAFiring(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	off, err := s.SaveTarget(ctx, store.TargetRow{
		Name: "выключен", Kind: "telegram", Address: "44", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := priceRule()
	r.Targets = []int64{off}

	queued, err := engineOn(t, s).Apply(ctx, persist(t, s, r), priceEvent())
	if err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Errorf("queued %d with nobody enabled", queued)
	}
	rows := ruleEvents(t, s)
	if len(rows) != 1 {
		t.Fatalf("rule_events has %d rows, want 1", len(rows))
	}
	if rows[0].SuppressedBy != "" {
		t.Errorf("suppressed_by = %q; nothing about the change stopped it",
			rows[0].SuppressedBy)
	}
}

// TestTheTwoHistoryQueriesHaveOppositeRules.
//
// This is the pair the whole suppression rests on, and getting either backwards
// is a monitor that is wrong in a way nobody notices for a week.
//
// LastFired EXCLUDES suppressed rows: a rate limit counts messages somebody
// received, and counting refusals would let one quiet-hours suppression start
// the clock as if a message had gone out — the rule would then be silent for an
// hour about a change it never reported.
//
// SeenDedupKey INCLUDES them: a change refused once for any reason is still the
// same change the second time, and re-deciding it would make a rule that was
// quiet at midnight noisy at one o'clock about a price that moved once.
func TestTheTwoHistoryQueriesHaveOppositeRules(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	r := persist(t, s, priceRule())[0]

	if _, err := s.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: r.ID, FiredAt: 9_000, Kind: string(track.KindPriceChanged),
		SubjectKind: string(SubjectThread), SubjectKey: 5000,
		SuppressedBy: string(ReasonQuietHours), DedupKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.LastFired(ctx, r.ID, string(SubjectThread), 5000); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("LastFired counted a suppressed row; one quiet-hours refusal " +
			"would start the rate limit as if a message had gone out")
	}

	seen, err := s.SeenDedupKey(ctx, r.ID, "k")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Error("SeenDedupKey ignored a suppressed row; the same change would " +
			"be decided again an hour later")
	}

	// And a row that was actually sent does start the clock.
	if _, err := s.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: r.ID, FiredAt: 9_500, Kind: string(track.KindPriceChanged),
		SubjectKind: string(SubjectThread), SubjectKey: 5000, DedupKey: "k2",
	}); err != nil {
		t.Fatal(err)
	}
	at, ok, err := s.LastFired(ctx, r.ID, string(SubjectThread), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || at != 9_500 {
		t.Errorf("LastFired = %d, %v; want 9500, true", at, ok)
	}
}

// TestARuleSurvivesTheRoundTrip. The condition tree, the filter and the target
// list go through JSON; a rule that came back different would fire on something
// other than what its owner wrote.
func TestARuleSurvivesTheRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := addTarget(t, s)

	want := Rule{
		Name: "дешевле на десять", Kind: track.KindPriceChanged,
		Condition: Node{Op: OpAnd, Nodes: []Node{
			{Op: OpCompare, Field: FieldPricePct, Cmp: CmpLess, Value: -10},
			{Op: OpCompare, Field: FieldConfidence, Cmp: CmpGreaterOrEq, Value: 1},
		}},
		Scope: Scope{Kind: ScopeFilter, Filter: Filter{
			Kinds:         []track.Kind{track.KindPriceChanged},
			PriceMinMinor: 1_000_000, PriceMaxMinor: 9_000_000,
			IncludeUnpriced: true,
		}},
		Urgent: true, ThresholdPct: 5, ThresholdMinor: 100_000,
		MinInterval: 90 * time.Minute, Aggregate: true,
		Targets: []int64{tgt}, Enabled: true, FromDefaults: true,
	}
	id, err := Save(ctx, s, want)
	if err != nil {
		t.Fatal(err)
	}

	all, err := All(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("read back %d rules, want 1", len(all))
	}
	got := all[0]
	want.ID = id
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the rule:\n got %+v\nwant %+v", got, want)
	}
}

// TestSavingARuleThatCanNeverMatchIsRefused. A rule that cannot fire sits in
// the list looking healthy, and its owner concludes the thing they are watching
// never changes.
func TestSavingARuleThatCanNeverMatchIsRefused(t *testing.T) {
	s := openStore(t)
	r := priceRule()
	r.Targets = nil
	if _, err := Save(context.Background(), s, r); err == nil {
		t.Error("a rule with no targets was stored")
	}
}

// TestEditingARuleKeepsItsHistory. rule_events rows reference the id; a save
// that inserted a new row would orphan them, and the rate limit that reads that
// history would start over — so changing a threshold would let a rule speak
// again immediately.
func TestEditingARuleKeepsItsHistory(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	r := persist(t, s, priceRule())[0]

	if _, err := engineOn(t, s).Apply(ctx, []Rule{r}, priceEvent()); err != nil {
		t.Fatal(err)
	}
	r.Name, r.ThresholdPct = "переименовано", 3
	id, err := Save(ctx, s, r)
	if err != nil {
		t.Fatal(err)
	}
	if id != r.ID {
		t.Fatalf("the edit took a new id %d, orphaning %d's history", id, r.ID)
	}
	if _, ok, err := s.LastFired(ctx, r.ID, string(SubjectThread), 5000); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("the firing was lost when the rule was edited")
	}
	all, err := All(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "переименовано" {
		t.Errorf("read back %d rules, first named %q", len(all), all[0].Name)
	}
}
