// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// This file is the four notification tables: what a rule is, what every
// decision about it recorded, who is to be told, and what is waiting to go.
//
// Two of the reads have rules that are exact opposites of each other, and both
// are load-bearing — see LastFired and SeenDedupKey.

// RuleRow is one rule as the database holds it.
//
// Condition, ScopeFilter and Targets are JSON in TEXT columns. That is the one
// place this schema stores structure as a blob, and it is deliberate: a
// condition is a tree whose shape the rule editor owns, and normalising it into
// rows would buy a join nobody runs and cost a migration every time the editor
// gains an operator.
type RuleRow struct {
	ID             int64
	Name           string
	EventKind      string
	Condition      string // JSON
	ScopeKind      string
	ScopeID        int64
	ScopeFilter    string // JSON
	Urgent         bool
	ThresholdPct   int
	ThresholdMinor int64
	// ThresholdCurrency is what the money floor is stated in. Roubles is the
	// only currency this source quotes; storing it rather than assuming it is
	// what keeps the column honest if that ever stops being true.
	ThresholdCurrency string
	MinIntervalSec    int
	Aggregate         bool
	Targets           string // JSON array of target ids
	Enabled           bool
	FromDefaults      bool
}

// RuleEventRow is one decision — a firing or a refusal.
//
// SubjectKey is an int64 here and TEXT in the column. The column is TEXT so a
// later subject kind need not be a number; the field is an int64 because every
// subject this build has is one, and handing callers a string to format would
// put the same strconv call at every call site instead of at this one.
type RuleEventRow struct {
	ID     int64
	RuleID int64
	// ChangeID is nullable with no foreign key, for the same reason
	// changes.pass_id is: retention removes old changes, and a firing must
	// outlive the change it rests on rather than be cascaded away with it.
	ChangeID     *int64
	FiredAt      int64
	Kind         string
	SubjectKind  string
	SubjectKey   int64
	Field        string
	SuppressedBy string
	DedupKey     string
}

// OutboxRow is one message waiting to go to one target.
type OutboxRow struct {
	ID          int64
	TargetID    int64
	RuleEventID *int64
	CreatedAt   int64
	DueAt       int64
	Body        string
	Attachment  string
}

// TargetRow is somebody to tell.
type TargetRow struct {
	ID      int64
	Name    string
	Kind    string
	Address string
	Enabled bool
}

// SaveRule inserts or updates, keyed on the id.
//
// An update rather than a delete and an insert, because rule_events rows
// reference the id: editing a rule's threshold would otherwise orphan its whole
// history, and the rate limit that reads that history would start over.
func (s *Store) SaveRule(ctx context.Context, r RuleRow) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now().UTC().Unix()

	if r.ID == 0 {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO rules (`+ruleWritable+`, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append(r.writeArgs(), now, now)...)
		if err != nil {
			return 0, fmt.Errorf("store: save rule: %w", err)
		}
		return res.LastInsertId()
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rules (id, `+ruleWritable+`, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name, event_kind = excluded.event_kind,
		   condition = excluded.condition, scope_kind = excluded.scope_kind,
		   scope_id = excluded.scope_id, scope_filter = excluded.scope_filter,
		   urgent = excluded.urgent, threshold_pct = excluded.threshold_pct,
		   threshold_minor = excluded.threshold_minor,
		   threshold_currency = excluded.threshold_currency,
		   min_interval_sec = excluded.min_interval_sec,
		   aggregate = excluded.aggregate, targets = excluded.targets,
		   enabled = excluded.enabled, from_defaults = excluded.from_defaults,
		   updated_at = excluded.updated_at`,
		append([]any{r.ID}, append(r.writeArgs(), now, now)...)...)
	if err != nil {
		return 0, fmt.Errorf("store: save rule %d: %w", r.ID, err)
	}
	return r.ID, nil
}

// ruleWritable is every column a caller sets, in one place so the column list
// and the argument list cannot drift apart — which is exactly what a
// hand-counted row of question marks does.
const ruleWritable = `name, event_kind, condition, scope_kind, scope_id,
	scope_filter, urgent, threshold_pct, threshold_minor, threshold_currency,
	min_interval_sec, aggregate, targets, enabled, from_defaults`

func (r RuleRow) writeArgs() []any {
	return []any{
		r.Name, r.EventKind, jsonOr(r.Condition, "{}"), r.ScopeKind, r.ScopeID,
		jsonOr(r.ScopeFilter, "{}"), r.Urgent, r.ThresholdPct, r.ThresholdMinor,
		r.ThresholdCurrency, r.MinIntervalSec, r.Aggregate,
		jsonOr(r.Targets, "[]"), r.Enabled, r.FromDefaults,
	}
}

// jsonOr keeps an empty Go string out of a column the readers unmarshal. An
// empty string is not valid JSON, and a rule saved with one would fail to load
// rather than load with no condition.
func jsonOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

const ruleColumns = `id, ` + ruleWritable

// Rules reads every rule, ordered by id so two runs list them the same way.
func (s *Store) Rules(ctx context.Context) ([]RuleRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ruleColumns+` FROM rules ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: rules: %w", err)
	}
	defer rows.Close()

	var out []RuleRow
	for rows.Next() {
		var r RuleRow
		if err := rows.Scan(&r.ID, &r.Name, &r.EventKind, &r.Condition,
			&r.ScopeKind, &r.ScopeID, &r.ScopeFilter, &r.Urgent, &r.ThresholdPct,
			&r.ThresholdMinor, &r.ThresholdCurrency, &r.MinIntervalSec, &r.Aggregate,
			&r.Targets, &r.Enabled, &r.FromDefaults); err != nil {
			return nil, fmt.Errorf("store: rules: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordRuleEvent writes one decision and returns its id.
func (s *Store) RecordRuleEvent(ctx context.Context, e RuleEventRow) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO rule_events
		  (rule_id, change_id, fired_at, kind, subject_kind, subject_key,
		   field, suppressed_by, dedup_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.RuleID, e.ChangeID, e.FiredAt, e.Kind, e.SubjectKind,
		strconv.FormatInt(e.SubjectKey, 10), e.Field, e.SuppressedBy, e.DedupKey)
	if err != nil {
		return 0, fmt.Errorf("store: record rule event: %w", err)
	}
	return res.LastInsertId()
}

const ruleEventColumns = `id, rule_id, change_id, fired_at, kind, subject_kind,
	subject_key, field, suppressed_by, dedup_key`

// RuleEvents reads the newest decisions, oldest first within the page, which is
// the order the panel reads them in.
func (s *Store) RuleEvents(ctx context.Context, limit int) ([]RuleEventRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ruleEventColumns+` FROM rule_events ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: rule events: %w", err)
	}
	defer rows.Close()

	var out []RuleEventRow
	for rows.Next() {
		var e RuleEventRow
		var key string
		if err := rows.Scan(&e.ID, &e.RuleID, &e.ChangeID, &e.FiredAt, &e.Kind,
			&e.SubjectKind, &key, &e.Field, &e.SuppressedBy, &e.DedupKey); err != nil {
			return nil, fmt.Errorf("store: rule events: %w", err)
		}
		// A key this build did not write is left as zero rather than failing the
		// read: the panel showing one row with a blank subject beats it showing
		// nothing at all.
		e.SubjectKey, _ = strconv.ParseInt(key, 10, 64)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastFired is the newest firing of this rule about this subject that was
// actually sent.
//
// Suppressed rows are excluded deliberately. A rate limit counts messages
// somebody received, and counting refusals would let a rule silence itself: one
// quiet-hours suppression would start the clock as if a message had gone out.
func (s *Store) LastFired(ctx context.Context, ruleID int64, kind string, key int64) (int64, bool, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `
		SELECT fired_at FROM rule_events
		WHERE rule_id = ? AND subject_kind = ? AND subject_key = ?
		  AND suppressed_by = ''
		ORDER BY fired_at DESC LIMIT 1`,
		ruleID, kind, strconv.FormatInt(key, 10)).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: last fired: %w", err)
	}
	return at, true, nil
}

// SeenDedupKey reports whether this rule has already decided about this exact
// change.
//
// Suppressed rows COUNT here, which is the opposite of LastFired and is
// deliberate. A change that was refused once for any reason is still the same
// change the second time, and re-deciding it would make a rule that was quiet at
// midnight noisy at one o'clock about a price that moved once.
func (s *Store) SeenDedupKey(ctx context.Context, ruleID int64, key string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM rule_events WHERE rule_id = ? AND dedup_key = ?`,
		ruleID, key).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: seen dedup key: %w", err)
	}
	return n > 0, nil
}

// SaveTarget inserts or updates somebody to tell.
func (s *Store) SaveTarget(ctx context.Context, t TargetRow) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	now := s.now().UTC().Unix()

	if t.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
			INSERT INTO notify_targets (name, kind, address, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			t.Name, t.Kind, t.Address, t.Enabled, now, now)
		if err != nil {
			return 0, fmt.Errorf("store: save target: %w", err)
		}
		return res.LastInsertId()
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_targets
		SET name = ?, kind = ?, address = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		t.Name, t.Kind, t.Address, t.Enabled, now, t.ID); err != nil {
		return 0, fmt.Errorf("store: save target %d: %w", t.ID, err)
	}
	return t.ID, nil
}

// Targets reads everybody, disabled included, because the panel lists them all
// and the checkbox is what says which is which.
func (s *Store) Targets(ctx context.Context) ([]TargetRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, kind, address, enabled FROM notify_targets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: targets: %w", err)
	}
	defer rows.Close()

	var out []TargetRow
	for rows.Next() {
		var t TargetRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.Address, &t.Enabled); err != nil {
			return nil, fmt.Errorf("store: targets: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EnabledTargets is the ids a rule's own list is intersected with, so that
// switching one target off silences it everywhere at once rather than requiring
// an edit to every rule that names it.
func (s *Store) EnabledTargets(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM notify_targets WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: enabled targets: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: enabled targets: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Enqueue puts one message in the outbox, pending.
//
// State is not a parameter. Everything this function writes is waiting to go —
// a caller that could pass 'sent' would be able to record a delivery that never
// happened, and the worker is the only thing entitled to move a row off pending.
func (s *Store) Enqueue(ctx context.Context, o OutboxRow) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO notify_outbox
		  (target_id, rule_event_id, created_at, due_at, attempts, state, body, attachment)
		VALUES (?, ?, ?, ?, 0, 'pending', ?, ?)`,
		o.TargetID, o.RuleEventID, o.CreatedAt, o.DueAt, o.Body, o.Attachment); err != nil {
		return fmt.Errorf("store: enqueue: %w", err)
	}
	return nil
}

// DueMessage is one outbox row that is ready to go, with the address it goes
// to. The join is what carries the address: the worker never learns what a
// target row is, only where this message is going.
type DueMessage struct {
	ID       int64
	TargetID int64
	Address  string
	Kind     string
	Body     string
	Attempts int
}

// DueMessages is the worker's only question: what is pending, ready, and has
// somebody switched on to receive it.
//
// The join filters on enabled = 1 and leaves everything else untouched. A row
// for a disabled target stays pending rather than being consumed — turning a
// target back on must not reveal that everything which arrived while it was off
// had been silently eaten.
func (s *Store) DueMessages(ctx context.Context, now int64, limit int) ([]DueMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.target_id, t.address, t.kind, o.body, o.attempts
		FROM notify_outbox o
		JOIN notify_targets t ON t.id = o.target_id
		WHERE o.state = 'pending' AND o.due_at <= ? AND t.enabled = 1
		ORDER BY o.due_at, o.id
		LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("store: due messages: %w", err)
	}
	defer rows.Close()

	var out []DueMessage
	for rows.Next() {
		var m DueMessage
		if err := rows.Scan(&m.ID, &m.TargetID, &m.Address, &m.Kind,
			&m.Body, &m.Attempts); err != nil {
			return nil, fmt.Errorf("store: due messages: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkSent records a delivery.
//
// last_error is cleared, because a row that went out carrying the text of the
// failure before it would show that failure in the panel for ever.
func (s *Store) MarkSent(ctx context.Context, id, at int64) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_outbox
		SET state = 'sent', sent_at = ?, attempts = attempts + 1, last_error = ''
		WHERE id = ? AND state = 'pending'`, at, id); err != nil {
		return fmt.Errorf("store: mark sent: %w", err)
	}
	return nil
}

// MarkRetry records a failed attempt and when to try again.
//
// dueAt is an absolute moment rather than a delay. Storing "wait five minutes"
// would mean a machine that reboots every four minutes retries for ever at the
// first backoff and never reaches the point of giving up.
func (s *Store) MarkRetry(ctx context.Context, id, dueAt int64, reason string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_outbox
		SET attempts = attempts + 1, due_at = ?, last_error = ?
		WHERE id = ? AND state = 'pending'`, dueAt, reason, id); err != nil {
		return fmt.Errorf("store: mark retry: %w", err)
	}
	return nil
}

// MarkGivenUp stops retrying this row.
//
// The reason is kept rather than the row being deleted: the panel shows what
// went wrong, and a message that vanished is indistinguishable from one that
// was never queued.
func (s *Store) MarkGivenUp(ctx context.Context, id int64, reason string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notify_outbox
		SET state = 'failed', attempts = attempts + 1, last_error = ?
		WHERE id = ? AND state = 'pending'`, reason, id); err != nil {
		return fmt.Errorf("store: mark given up: %w", err)
	}
	return nil
}
