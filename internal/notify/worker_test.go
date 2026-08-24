// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
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

// sink records what it was asked to send and fails on demand.
type sink struct {
	sent []Message
	fail error
}

func (s *sink) Send(_ context.Context, m Message) error {
	if s.fail != nil {
		return s.fail
	}
	s.sent = append(s.sent, m)
	return nil
}

func queueOne(t *testing.T, s *store.Store, target int64, at int64, body string) {
	t.Helper()
	if err := s.Enqueue(context.Background(), store.OutboxRow{
		TargetID: target, CreatedAt: at, DueAt: at, Body: body,
	}); err != nil {
		t.Fatal(err)
	}
}

func target(t *testing.T, s *store.Store) int64 {
	t.Helper()
	id, err := s.SaveTarget(context.Background(), store.TargetRow{
		Name: "мне", Kind: "telegram", Address: "42", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func workerOn(s *store.Store, tr Transport, now int64) *Worker {
	return &Worker{
		Store: s, Transport: tr, Max: 100,
		Now: func() time.Time { return time.Unix(now, 0).UTC() },
	}
}

func TestASentRowIsNeverSentTwice(t *testing.T) {
	s := openStore(t)
	tgt := target(t, s)
	queueOne(t, s, tgt, 1000, "привет")
	tr := &sink{}

	if _, err := workerOn(s, tr, 2000).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := workerOn(s, tr, 3000).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 1 {
		t.Errorf("sent %d times, want 1", len(tr.sent))
	}
}

// TestARowThatIsNotDueYetIsLeftAlone.
func TestARowThatIsNotDueYetIsLeftAlone(t *testing.T) {
	s := openStore(t)
	tgt := target(t, s)
	queueOne(t, s, tgt, 5000, "потом")
	tr := &sink{}

	if _, err := workerOn(s, tr, 1000).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 0 {
		t.Error("a row due in the future was sent")
	}
}

// TestDueAtIsAnAbsoluteMomentAndSurvivesARestart.
//
// Storing "wait five minutes" instead of "not before 14:05" means a machine
// that reboots every four minutes retries for ever at the first backoff, and
// never reaches the point of giving up.
func TestDueAtIsAnAbsoluteMomentAndSurvivesARestart(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := target(t, s)
	queueOne(t, s, tgt, 1000, "привет")

	failing := &sink{fail: errors.New("telegram is down")}
	if _, err := workerOn(s, failing, 1000).Run(ctx); err != nil {
		t.Fatal(err)
	}

	var due, attempts int64
	if err := s.DB().QueryRow(
		`SELECT due_at, attempts FROM notify_outbox LIMIT 1`).Scan(&due, &attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if due <= 1000 {
		t.Fatalf("due_at = %d, want a moment after the failure — a relative "+
			"pause would reset on every restart", due)
	}

	// A "restart" is just another Worker over the same store. The row must
	// still be waiting.
	tr := &sink{}
	if _, err := workerOn(s, tr, 1001).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 0 {
		t.Error("the pause reset across a restart")
	}
}

// TestBackoffGrowsAndThenTheRowIsGivenUp.
func TestBackoffGrowsAndThenTheRowIsGivenUp(t *testing.T) {
	if Backoff(1) >= Backoff(2) || Backoff(2) >= Backoff(3) {
		t.Error("the backoff does not grow")
	}
	s := openStore(t)
	ctx := context.Background()
	tgt := target(t, s)
	queueOne(t, s, tgt, 0, "привет")

	failing := &sink{fail: errors.New("still down")}
	now := int64(1)
	for i := 0; i < 20; i++ {
		if _, err := workerOn(s, failing, now).Run(ctx); err != nil {
			t.Fatal(err)
		}
		now += 86400 // a day, so every backoff has certainly elapsed
	}
	var state string
	var lastErr string
	if err := s.DB().QueryRow(
		`SELECT state, last_error FROM notify_outbox LIMIT 1`).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Errorf("state = %q after twenty failures, want failed — a row that "+
			"retries for ever is a queue that never drains", state)
	}
	if lastErr == "" {
		t.Error("no last_error recorded; the panel shows it")
	}
}

// TestARowWhoseRuleEventWasDeletedStillSends.
//
// rule_event_id is nullable and cleared rather than cascaded, so a message
// delayed by an outage survives its rule being edited. The alternative loses
// precisely the notifications that were already late.
func TestARowWhoseRuleEventWasDeletedStillSends(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := target(t, s)

	// rule_events.rule_id is a foreign key, so the rule has to exist before a
	// decision about it can. That is the constraint this test then deletes
	// THROUGH — the point being that the outbox row survives it.
	ruleID, err := s.SaveRule(ctx, store.RuleRow{
		Name: "новое", EventKind: "listing-appeared", ScopeKind: "watch",
		ScopeID: 1, Targets: "[1]", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: ruleID, FiredAt: 1000, Kind: "listing-appeared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, store.OutboxRow{
		TargetID: tgt, RuleEventID: &id, CreatedAt: 1000, DueAt: 1000,
		Body: "привет",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DELETE FROM rule_events WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}

	tr := &sink{}
	if _, err := workerOn(s, tr, 2000).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 1 {
		t.Error("a message whose rule event was deleted was lost; that is " +
			"precisely the notification that was already late")
	}
}

// TestADisabledTargetIsNotSentTo, and the row is not consumed either: turning a
// target back on must not have silently eaten the queue.
func TestADisabledTargetIsNotSentTo(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, err := s.SaveTarget(ctx, store.TargetRow{
		Name: "выключено", Kind: "telegram", Address: "42", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	queueOne(t, s, id, 1000, "привет")

	tr := &sink{}
	if _, err := workerOn(s, tr, 2000).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 0 {
		t.Error("sent to a disabled target")
	}
	var state string
	if err := s.DB().QueryRow(`SELECT state FROM notify_outbox LIMIT 1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Errorf("state = %q, want pending; a disabled target must not consume "+
			"the queue", state)
	}
}

// picky fails for one address and succeeds for the rest.
type picky struct {
	bad  string
	sent []Message
}

func (p *picky) Send(_ context.Context, m Message) error {
	if m.Address == p.bad {
		return errors.New("этот адрес недоступен")
	}
	p.sent = append(p.sent, m)
	return nil
}

func targetAt(t *testing.T, s *store.Store, address string, enabled bool) int64 {
	t.Helper()
	id, err := s.SaveTarget(context.Background(), store.TargetRow{
		Name: address, Kind: "telegram", Address: address, Enabled: enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestOneUnreachableTargetDoesNotHoldUpTheOthers.
//
// A transport error is a fact about that message rather than about the queue.
// Stopping the drain on the first failure would let one wrong chat id silence
// every other person the install notifies.
func TestOneUnreachableTargetDoesNotHoldUpTheOthers(t *testing.T) {
	s := openStore(t)
	first := targetAt(t, s, "сломанный", true)
	second := targetAt(t, s, "рабочий", true)
	queueOne(t, s, first, 1000, "раз")
	queueOne(t, s, second, 1000, "два")

	tr := &picky{bad: "сломанный"}
	st, err := workerOn(s, tr, 2000).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 1 || tr.sent[0].Address != "рабочий" {
		t.Errorf("delivered %+v; the working target was skipped because the "+
			"broken one came first", tr.sent)
	}
	if st.Sent != 1 || st.Failed != 1 {
		t.Errorf("Stats = %+v, want one sent and one failed", st)
	}
}

// TestFailedAndGaveUpAreSeparateNumbers.
//
// They mean different things to whoever reads them: Failed is "Telegram was
// down and we will try again", GaveUp is "this message is never going to
// arrive". One count would let the second hide inside the first.
func TestFailedAndGaveUpAreSeparateNumbers(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := target(t, s)
	queueOne(t, s, tgt, 0, "привет")
	failing := &sink{fail: errors.New("недоступно")}

	now := int64(1)
	var last Stats
	for i := 0; i < 20; i++ {
		st, err := workerOn(s, failing, now).Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if st.GaveUp > 0 {
			last = st
			break
		}
		if st.Failed != 1 {
			t.Fatalf("attempt %d reported %+v, want one retry", i+1, st)
		}
		now += 86400
	}
	if last.GaveUp != 1 {
		t.Fatal("the row was never given up on")
	}
	if last.Failed != 0 {
		t.Errorf("the giving-up drain also counted %d retries; a message that "+
			"is never arriving must not hide inside the retry number", last.Failed)
	}
}

// TestMaxBoundsOneDrain. A queue that built up during a long outage must not
// turn the first tick after it into a thousand requests.
func TestMaxBoundsOneDrain(t *testing.T) {
	s := openStore(t)
	tgt := target(t, s)
	for i := 0; i < 7; i++ {
		queueOne(t, s, tgt, 1000, "привет")
	}
	tr := &sink{}
	w := workerOn(s, tr, 2000)
	w.Max = 3

	st, err := w.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Sent != 3 {
		t.Errorf("sent %d in one drain, want the bound of 3", st.Sent)
	}
	if _, err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 6 {
		t.Errorf("two drains sent %d, want 6 — the rest are still queued", len(tr.sent))
	}
}

// TestARecoveredRowDoesNotKeepShowingItsOldError. The panel reads last_error,
// and a message that went out carrying the text of the failure before it would
// show that failure for ever.
func TestARecoveredRowDoesNotKeepShowingItsOldError(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	tgt := target(t, s)
	queueOne(t, s, tgt, 1000, "привет")

	if _, err := workerOn(s, &sink{fail: errors.New("недоступно")}, 1000).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := workerOn(s, &sink{}, 100_000).Run(ctx); err != nil {
		t.Fatal(err)
	}
	var state, lastErr string
	if err := s.DB().QueryRow(
		`SELECT state, last_error FROM notify_outbox LIMIT 1`).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if state != "sent" {
		t.Fatalf("state = %q after the retry succeeded", state)
	}
	if lastErr != "" {
		t.Errorf("last_error = %q on a row that was delivered", lastErr)
	}
}

// TestTheOldestDueMessageGoesFirst. Two people told about the same market get
// told in the order the changes happened, not in whatever order SQLite felt
// like returning.
func TestTheOldestDueMessageGoesFirst(t *testing.T) {
	s := openStore(t)
	tgt := target(t, s)
	queueOne(t, s, tgt, 3000, "третий")
	queueOne(t, s, tgt, 1000, "первый")
	queueOne(t, s, tgt, 2000, "второй")

	tr := &sink{}
	if _, err := workerOn(s, tr, 9000).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, m := range tr.sent {
		got = append(got, m.Body)
	}
	want := []string{"первый", "второй", "третий"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestTheAddressComesFromTheTarget. The worker never learns what a target row
// is; the join is what carries the address, and a transport with the wrong one
// delivers somebody else's alerts to a stranger.
func TestTheAddressComesFromTheTarget(t *testing.T) {
	s := openStore(t)
	tgt := targetAt(t, s, "-1001234567890", true)
	queueOne(t, s, tgt, 1000, "привет")

	tr := &sink{}
	if _, err := workerOn(s, tr, 2000).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tr.sent) != 1 {
		t.Fatal("nothing was sent")
	}
	if tr.sent[0].Address != "-1001234567890" {
		t.Errorf("Address = %q", tr.sent[0].Address)
	}
	if tr.sent[0].Kind != "telegram" {
		t.Errorf("Kind = %q; the ladder reads it to pick a transport", tr.sent[0].Kind)
	}
}
