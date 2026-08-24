# M1 Increment 5 — Rules, the outbox and the first message

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A change becomes a message a person actually receives, or a recorded reason why it did not.

**Architecture:** `internal/rules` lifts from WB_GO with three edits and decides what fires; `internal/notify` lifts its outbox worker; `internal/telegram` reaches Telegram over a `Sender` interface with two rungs — the Bot API and MTProto — chosen by a ladder that remembers what worked. Nothing silently drops a notification: every suppression writes a `rule_events` row naming its reason, because a monitor that goes quiet cannot be debugged by the person who stopped receiving messages.

**Tech Stack:** Go 1.26, stdlib, `github.com/gotd/td` for MTProto. That dependency is by a wide margin the largest line in the budget — some forty indirect modules for one delivery rung — and it is carried knowingly: `api.telegram.org` is blocked for part of the Russian audience, which is this product's audience, and a user in direct mode behind that block otherwise has no path at all.

**Spec:** `docs/superpowers/specs/2026-08-23-avito-monitor-m1-design.md` — §Events and rules (line 526), §Notifications (line 564), the `rules`, `rule_events`, `notify_targets` and `notify_outbox` DDL, increment 5 (line 1513).

**Donor:** `S:\Projects\WB_GO`. Four packages lift: `internal/rules` (`rules.go`, `predicate.go`, `suppress.go`, `persist.go`), `internal/notify` (`notify.go`, `worker.go`), and `internal/telegram` (`sender.go`, `bot.go`, `route.go`, `mtproto.go`). Each lift names its donor file in a comment, as increments 2 and 3 do.

## Global Constraints

- **Go 1.26, no CGO.** `go test -race` does not run on this machine — the race detector needs cgo and there is no gcc on PATH. Run `go test ./...`; `-race` is CI's job.
- **`internal/rules` imports `internal/track` and nothing else of ours.** That is what makes the lift a lift: WB_GO's `rules` reads `track` and never its second event catalogue, so deleting that catalogue removes work rather than adding it.
- **No token, key or session blob is ever logged, printed, or written to a file this program creates.** `telegram.Bot` already redacts its token from every error it returns; that behaviour is lifted and tested, not assumed.
- **Every suppression writes a `rule_events` row with its reason.** «Почему мне не сказали» is the question that column exists to answer.
- **Money is integer minor units beside a currency, never a float.**
- **Russian user-facing copy.** Messages, the «предположительно» prefix and the inline button are Russian; code, comments and identifiers are English.
- **Comments carry the reason, not the restatement.**

---

## File structure

| file | responsibility |
|---|---|
| `internal/rules/rules.go` | `Rule`, `Scope`, `Filter`, `Matches`, `Validate`, `DedupKey` |
| `internal/rules/predicate.go` | the condition tree, lifted whole |
| `internal/rules/suppress.go` | `Suppressor`, `Reason`, `Decision`, quiet hours |
| `internal/rules/persist.go` | `Save`, `Load`, `All` over the `rules` table |
| `internal/rules/engine.go` | `Engine.Apply` — one change against every rule |
| `internal/rules/defaults.go` | the nine default rules, enumerated |
| `internal/notify/notify.go` | `Message`, `Transport`, `Backoff` |
| `internal/notify/worker.go` | the outbox worker |
| `internal/telegram/sender.go` | the `Sender` seam and the two-rung ladder |
| `internal/telegram/bot.go` | the Bot API rung, with its token redaction |
| `internal/telegram/mtproto.go` | the MTProto rung over `gotd/td` |
| `internal/telegram/message.go` | a `ChangeRow` rendered as Russian prose |
| `cmd/avitomon/notify.go` | `avitomon notify` — send the queue, or one test message |

---

## Task 1: `internal/rules` — the lift and its three edits

**Files:**
- Create: `internal/rules/rules.go`, `internal/rules/predicate.go`, `internal/rules/rules_test.go`

**Interfaces:**
- Consumes: `track.Kind`, `track.Change`, `store.ChangeRow`.
- Produces:

```go
type ScopeKind string // "listing" | "thread" | "seller" | "watch" | "filter"
type Filter struct {
    Kinds           []track.Kind
    PriceMinMinor   int64
    PriceMaxMinor   int64
    IncludeUnpriced bool
}
type Scope struct { Kind ScopeKind; ID int64; Filter Filter }
func (s Scope) Covers(ev Event) bool

type Event struct {
    Change   store.ChangeRow
    ThreadID int64
    SellerID int64
    WatchID  int64
}

type Rule struct {
    ID             int64
    Name           string
    Kind           track.Kind
    Condition      Node
    Scope          Scope
    Urgent         bool
    ThresholdPct   int
    ThresholdMinor int64
    MinInterval    time.Duration
    Aggregate      bool
    Targets        []int64
    Enabled        bool
    FromDefaults   bool
}
func (r Rule) Matches(ev Event) bool
func (r Rule) Validate() error
func DedupKey(r Rule, ev Event) string
```

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func priceEvent() Event {
	return Event{
		Change: store.ChangeRow{
			Kind: track.KindPriceChanged, ItemID: 7800000001,
			Field: "price", Was: "4000000", Now: "3500000",
			Unit: track.UnitMinor, Confidence: 1.0,
		},
		ThreadID: 5000, SellerID: 42, WatchID: 1,
	}
}

func priceRule() Rule {
	return Rule{
		ID: 1, Name: "цена упала", Kind: track.KindPriceChanged,
		Condition: Always{}, Scope: Scope{Kind: ScopeWatch, ID: 1},
		Enabled: true, Targets: []int64{1},
	}
}

// TestDedupKeyForAPriceKindIsOnTheThread.
//
// This is a bug WB_GO cannot have. Without it a seller who reposts the same
// item at the same price every three days produces a fresh price-changed every
// time, because each incarnation is a new number with no history — and that is
// precisely the noise that gets a monitor switched off in its first week.
func TestDedupKeyForAPriceKindIsOnTheThread(t *testing.T) {
	a := priceEvent()
	b := priceEvent()
	b.Change.ItemID = 7800000999 // a repost: new number, same thread
	b.ThreadID = a.ThreadID

	if DedupKey(priceRule(), a) != DedupKey(priceRule(), b) {
		t.Error("two incarnations of one advertisement produced different dedup " +
			"keys; the second repost at the same price would be announced again")
	}
	if !strings.Contains(DedupKey(priceRule(), a), "5000") {
		t.Error("the key does not carry the thread id")
	}
	// And it still carries the numbers: a price that oscillates between two
	// values all afternoon produces a genuinely identical change each time it
	// comes back, and that IS the repetition deduplication means.
	c := priceEvent()
	c.Change.Now = "3600000"
	if DedupKey(priceRule(), a) == DedupKey(priceRule(), c) {
		t.Error("two different price moves share a key")
	}
}

// TestDedupKeyForAnAppearanceCarriesNoNumbers.
//
// A listing may be announced as new at most once, ever, however many times it
// flaps in and out of the window.
func TestDedupKeyForAnAppearanceCarriesNoNumbers(t *testing.T) {
	r := priceRule()
	r.Kind = track.KindListingAppeared

	a := priceEvent()
	a.Change.Kind, a.Change.Was, a.Change.Now = track.KindListingAppeared, "", ""
	b := a
	b.Change.TS = a.Change.TS + 100_000

	if DedupKey(r, a) != DedupKey(r, b) {
		t.Error("the same listing appearing twice produced two keys; a listing " +
			"that flaps in and out would be announced as new each time")
	}
	if strings.Contains(DedupKey(r, a), "4000000") {
		t.Error("an appearance key carries a number")
	}
}

// TestAnUnknownScopeCoversNothing. WB_GO's default, kept deliberately: a scope
// this build does not understand must not turn into "notify on every change in
// the database".
func TestAnUnknownScopeCoversNothing(t *testing.T) {
	s := Scope{Kind: ScopeKind("galaxy"), ID: 1}
	if s.Covers(priceEvent()) {
		t.Error("an unknown scope covered an event")
	}
}

// TestAPriceBandDoesNotIncludeSomethingWhoseTPriceWasNotRead.
//
// IncludeUnpriced defaults to false, keeping WB_GO's reasoning: a price band
// cannot include something whose price was not read. Here that is a large
// fraction of the catalogue rather than an edge case — «Цена договорная» is
// ordinary on this site — so the flag exists and its default is the safe one.
func TestAPriceBandDoesNotIncludeSomethingWhosePriceWasNotRead(t *testing.T) {
	ev := priceEvent()
	ev.Change.Now = "" // no price read
	s := Scope{Kind: ScopeFilter, Filter: Filter{
		PriceMinMinor: 1000000, PriceMaxMinor: 9000000,
	}}
	if s.Covers(ev) {
		t.Error("a price band covered a change with no price")
	}
	s.Filter.IncludeUnpriced = true
	if !s.Covers(ev) {
		t.Error("IncludeUnpriced did not include it")
	}
}

// TestValidateRefusesARuleThatCanNeverMatch. A rule that cannot fire is worse
// than no rule: the person believes they are covered.
func TestValidateRefusesARuleThatCanNeverMatch(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Rule)
	}{
		{"no kind", func(r *Rule) { r.Kind = "" }},
		{"a kind nothing emits", func(r *Rule) { r.Kind = track.Kind("price-doubled") }},
		{"no targets", func(r *Rule) { r.Targets = nil }},
		{"an inverted price band", func(r *Rule) {
			r.Scope = Scope{Kind: ScopeFilter, Filter: Filter{
				PriceMinMinor: 9000000, PriceMaxMinor: 1000000,
			}}
		}},
		{"a negative threshold", func(r *Rule) { r.ThresholdPct = -5 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := priceRule()
			c.edit(&r)
			if err := r.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestValidateReportsEveryReasonAtOnce. A form that rejects one field at a time
// is a form somebody submits five times.
func TestValidateReportsEveryReasonAtOnce(t *testing.T) {
	r := priceRule()
	r.Kind, r.Targets, r.ThresholdPct = "", nil, -1
	err := r.Validate()
	if err == nil {
		t.Fatal("accepted a rule with three faults")
	}
	for _, want := range []string{"kind", "target", "threshold"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("err = %q; it does not mention %q", err, want)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/rules/ -v
```

Expected: FAIL to build — `undefined: Event`.

- [ ] **Step 3: Lift `predicate.go` unchanged**

```bash
cd S:/Projects/Avito_Go && mkdir -p internal/rules && \
  cp "S:/Projects/WB_GO/internal/rules/predicate.go" internal/rules/predicate.go
```

Then replace WB_GO's `Field` catalogue with this target's: `price_minor`,
`price_pct`, `discount_pct`, `title`, `confidence`, `position`, `watch_id`,
`seller_id`. Keep `Op`, `Cmp`, `Node`, `Eval` and `Fields()` exactly as they
are — the tree, the short-circuiting and the unknown-field-is-false default are
the reason to lift rather than write.

Add the donor comment at the top:

```go
// Lifted from WB_GO's internal/rules/predicate.go. The tree, its
// short-circuiting and its unknown-field-is-false default are unchanged; only
// the Field catalogue is this target's, because a field name is the one part of
// a predicate that is about the domain rather than about predicates.
```

- [ ] **Step 4: Write `rules.go` with the three edits**

The edits, each with its reason in a comment:

```go
// ScopeKind is what a rule is scoped to.
//
// WB_GO has product | category | supplier | job. This target's grain is
// different in one way that matters: a listing has incarnations, so a rule can
// be about THIS advertisement or about the thing it advertises across reposts,
// and those are different subscriptions.
const (
	ScopeListing ScopeKind = "listing"
	ScopeThread  ScopeKind = "thread"
	ScopeSeller  ScopeKind = "seller"
	ScopeWatch   ScopeKind = "watch"
	ScopeFilter  ScopeKind = "filter"
)

// DedupKey identifies "this same change again".
//
// Two departures from WB_GO's, and both are about reposts.
//
// Price and title kinds key on thread_id rather than on the listing. Without
// it a seller who reposts the same item at the same price every three days
// produces a fresh price-changed every time, because each incarnation is a new
// number with no history — and that is precisely the noise that gets a monitor
// switched off in its first week.
//
// listing-appeared carries no numbers at all, so a listing is announced as new
// at most once ever, however many times it flaps in and out of the window.
func DedupKey(r Rule, ev Event) string {
	c := ev.Change
	parts := []string{strconv.FormatInt(r.ID, 10), string(c.Kind)}

	switch c.Kind {
	case track.KindListingAppeared, track.KindListingReturned:
		// No subject beyond the listing and no numbers at all. This fires at
		// most once per listing per rule, ever.
		parts = append(parts, strconv.FormatInt(c.ItemID, 10))

	case track.KindPriceChanged, track.KindPriceAppeared,
		track.KindPriceWithdrawn, track.KindDiscountChanged,
		track.KindTitleChanged:
		// The thread, so a repost at the same price is the same change.
		parts = append(parts,
			strconv.FormatInt(ev.ThreadID, 10), c.Field, c.Was, c.Now)

	default:
		// Everything else keys on the listing and its numbers, which is
		// WB_GO's shape. The numbers are in it deliberately: a value that
		// oscillates between two states all afternoon produces a genuinely
		// identical change every time it comes back, and that is exactly the
		// repetition deduplication means. Leave them out and a listing moving
		// A → B → A → B reports two distinct events; put a timestamp in and
		// nothing is ever a duplicate of anything.
		parts = append(parts,
			strconv.FormatInt(c.ItemID, 10), c.Field, c.Was, c.Now)
	}
	return strings.Join(parts, "|")
}
```

`Filter.IncludeUnpriced` is the third:

```go
	// IncludeUnpriced admits a change whose price was not read into a price
	// band. Default false, which is WB_GO's reasoning — a band cannot include
	// something whose price is unknown — but the flag exists because on this
	// site «Цена договорная» is ordinary rather than an edge case, and a person
	// watching a band may well want those too.
	IncludeUnpriced bool
```

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/rules/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test and subtest PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/rules/ && git commit -m "$(cat <<'MSG'
feat(rules): the lift, and the three edits reposts force

predicate.go is WB_GO's unchanged — the tree, the short-circuiting and
the unknown-field-is-false default are the reason to lift rather than
write. Only the Field catalogue is this target's.

Three edits, and two of them are about the same thing: a listing here has
incarnations.

ScopeKind gains thread beside listing, because a rule can be about THIS
advertisement or about the thing it advertises across reposts, and those
are different subscriptions.

DedupKey keys price and title kinds on thread_id. Without it a seller who
reposts the same item at the same price every three days produces a fresh
price-changed every time — each incarnation is a new number with no
history — and that is precisely the noise that gets a monitor switched
off in its first week. For listing-appeared the key carries no numbers at
all, so a listing is announced as new at most once ever, however often it
flaps in and out of the window.

Filter.IncludeUnpriced defaults to false, keeping WB_GO's reasoning that
a price band cannot include something whose price is unknown — while
acknowledging that here «Цена договорная» is ordinary rather than an edge
case.

MSG
)"
```

---

## Task 2: Suppression, and the row every refusal writes

**Files:**
- Create: `internal/rules/suppress.go`, `internal/rules/suppress_test.go`

**Interfaces:**
- Produces:

```go
type Reason string
const (
    ReasonNone       Reason = ""
    ReasonQuietHours Reason = "quiet-hours"
    ReasonRateLimit  Reason = "rate-limit"
    ReasonDuplicate  Reason = "duplicate"
    ReasonThreshold  Reason = "threshold"
    ReasonSeed       Reason = "seed"
    ReasonRepost     Reason = "repost"
)
type Decision struct { Send bool; Reason Reason; DedupKey string }
type QuietHours struct { FromMin, ToMin int }
func (q QuietHours) Active(t time.Time) bool
type Suppressor struct { Quiet QuietHours; Now func() time.Time; LastFired func(ruleID int64, subject SubjectPair) (time.Time, bool); SeenKey func(ruleID int64, key string) bool }
func (s Suppressor) Decide(r Rule, ev Event) Decision
```

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

func passthrough() Suppressor {
	return Suppressor{
		Now:       func() time.Time { return time.Unix(10_000, 0).UTC() },
		LastFired: func(int64, SubjectPair) (time.Time, bool) { return time.Time{}, false },
		SeenKey:   func(int64, string) bool { return false },
	}
}

func TestAnOrdinaryChangeIsSent(t *testing.T) {
	d := passthrough().Decide(priceRule(), priceEvent())
	if !d.Send {
		t.Errorf("an ordinary change was suppressed: %s", d.Reason)
	}
	if d.DedupKey == "" {
		t.Error("no dedup key recorded; the next decision compares against it")
	}
}

// TestEverySuppressionRecordsItsDedupKey. WB_GO's rule, kept: the key is
// recorded either way, because the next decision compares against it and a
// suppressed firing that recorded nothing would make the one after it a
// duplicate of nothing.
func TestEverySuppressionRecordsItsDedupKey(t *testing.T) {
	s := passthrough()
	s.SeenKey = func(int64, string) bool { return true }
	d := s.Decide(priceRule(), priceEvent())
	if d.Send || d.Reason != ReasonDuplicate {
		t.Fatalf("Send=%v Reason=%q, want a duplicate refusal", d.Send, d.Reason)
	}
	if d.DedupKey == "" {
		t.Error("a suppressed decision recorded no key")
	}
}

// TestTheCheapestTestRunsFirst. Ordering is not style here: SeenKey and
// LastFired are database reads, and a rule whose threshold refuses the change
// must not pay for them.
func TestTheCheapestTestRunsFirst(t *testing.T) {
	reads := 0
	s := passthrough()
	s.SeenKey = func(int64, string) bool { reads++; return false }
	s.LastFired = func(int64, SubjectPair) (time.Time, bool) { reads++; return time.Time{}, false }

	r := priceRule()
	r.ThresholdMinor = 100_000_000 // nothing clears this

	d := s.Decide(r, priceEvent())
	if d.Send || d.Reason != ReasonThreshold {
		t.Fatalf("Send=%v Reason=%q, want a threshold refusal", d.Send, d.Reason)
	}
	if reads != 0 {
		t.Errorf("%d database reads before the threshold refused it", reads)
	}
}

// TestTheRateLimitIsPerSubjectPair.
//
// WB_GO's LastFired takes one id. Here it takes a pair, because the limit is
// per thread for a price rule and per listing for a lifecycle one: a repost
// must not reset a price rule's limit, and it must not silence the successor's
// appearance either.
func TestTheRateLimitIsPerSubjectPair(t *testing.T) {
	var asked []SubjectPair
	s := passthrough()
	s.LastFired = func(_ int64, p SubjectPair) (time.Time, bool) {
		asked = append(asked, p)
		return time.Time{}, false
	}

	priced := priceRule()
	priced.MinInterval = time.Hour
	s.Decide(priced, priceEvent())

	lifecycle := priceRule()
	lifecycle.Kind, lifecycle.MinInterval = track.KindListingAppeared, time.Hour
	ev := priceEvent()
	ev.Change.Kind = track.KindListingAppeared
	s.Decide(lifecycle, ev)

	if len(asked) != 2 {
		t.Fatalf("asked %d times, want 2", len(asked))
	}
	if asked[0].Kind != SubjectThread || asked[0].Key != 5000 {
		t.Errorf("a price rule asked about %+v, want the thread", asked[0])
	}
	if asked[1].Kind != SubjectListing || asked[1].Key != 7800000001 {
		t.Errorf("a lifecycle rule asked about %+v, want the listing", asked[1])
	}
}

// TestQuietHoursSpanMidnight. 22:00 to 07:00 is the ordinary setting and the
// one a naive from < to comparison gets wrong.
func TestQuietHoursSpanMidnight(t *testing.T) {
	q := QuietHours{FromMin: 22 * 60, ToMin: 7 * 60}
	cases := []struct {
		hour int
		want bool
	}{{23, true}, {2, true}, {6, true}, {7, false}, {12, false}, {21, false}}
	for _, c := range cases {
		at := time.Date(2026, 8, 24, c.hour, 30, 0, 0, time.UTC)
		if got := q.Active(at); got != c.want {
			t.Errorf("Active(%02d:30) = %v, want %v", c.hour, got, c.want)
		}
	}
}

// TestUrgentIsExemptFromQuietHoursAndFromNothingElse. It is the single
// exemption the design allows, so that "urgent" keeps meaning something.
func TestUrgentIsExemptFromQuietHoursAndFromNothingElse(t *testing.T) {
	s := passthrough()
	s.Quiet = QuietHours{FromMin: 0, ToMin: 24 * 60}

	r := priceRule()
	if d := s.Decide(r, priceEvent()); d.Send || d.Reason != ReasonQuietHours {
		t.Fatalf("Send=%v Reason=%q inside quiet hours", d.Send, d.Reason)
	}
	r.Urgent = true
	if d := s.Decide(r, priceEvent()); !d.Send {
		t.Error("an urgent rule was silenced by quiet hours")
	}

	// And urgency buys nothing against a duplicate.
	s.SeenKey = func(int64, string) bool { return true }
	if d := s.Decide(r, priceEvent()); d.Send {
		t.Error("an urgent rule sent a duplicate; urgency exempts from quiet " +
			"hours and from nothing else")
	}
}

// TestASeedingChangeIsSuppressedWithItsOwnReason.
//
// A watch's first pass produces thirty arrivals that are real and are not news.
// The row is written anyway: a monitor that silently drops notifications cannot
// be debugged by the person who stopped receiving them.
func TestASeedingChangeIsSuppressedWithItsOwnReason(t *testing.T) {
	ev := priceEvent()
	ev.Change.Kind, ev.Change.Backfill = track.KindListingAppeared, true
	r := priceRule()
	r.Kind = track.KindListingAppeared

	d := passthrough().Decide(r, ev)
	if d.Send {
		t.Error("a backfilled arrival was sent")
	}
	if d.Reason != ReasonSeed {
		t.Errorf("Reason = %q, want %q — «почему мне не сказали» is what that "+
			"column answers", d.Reason, ReasonSeed)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/rules/ -run 'Suppress|Quiet|Urgent|RateLimit|Cheapest|Seeding|Dedup' -v
```

Expected: FAIL to build.

- [ ] **Step 3: Lift `suppress.go` and make the two edits**

Copy `S:/Projects/WB_GO/internal/rules/suppress.go`, keep its cheapest-first
ordering and its "record the key either way" rule, then:

- `LastFired` takes a `SubjectPair` rather than an id, with the comment above;
- `Reason` gains `seed` and `repost`, and the seed test is `ev.Change.Backfill`
  — increment 4 already marks exactly the changes a seed and an incomparable
  pass produced, so this needs no new signal.

```go
// SubjectPair is what a rate limit counts against.
//
// A price rule counts per thread and a lifecycle rule per listing, and the
// difference is a repost: it must not reset a price rule's limit, and it must
// not silence the successor's appearance either. One id could not express both.
type SubjectPair struct {
	Kind SubjectKind // "listing" | "thread"
	Key  int64
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/rules/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/rules/ && git commit -m "$(cat <<'MSG'
feat(rules): suppression, and the row every refusal writes

WB_GO's cheapest-first ordering and its record-the-key-either-way rule,
lifted. The ordering is not style: SeenKey and LastFired are database
reads, and a rule whose threshold refuses the change must not pay for
them. The key is recorded even on a refusal, because the next decision
compares against it and a suppressed firing that recorded nothing would
make the one after it a duplicate of nothing.

Two edits. LastFired takes a subject PAIR rather than one id, because the
limit is per thread for a price rule and per listing for a lifecycle one
— a repost must not reset a price rule's limit and must not silence the
successor's appearance either.

And Reason gains seed and repost. The seed test reads Change.Backfill,
which increment 4 already sets on exactly the changes a seeding pass and
an incomparable pass produced, so no new signal was needed. Both write
the rule_events row: a monitor that silently drops notifications cannot
be debugged by the person who stopped receiving them.

Urgent exempts from quiet hours and from nothing else, so that "urgent"
keeps meaning something.

MSG
)"
```

---

## Task 3: `Engine.Apply` and the row every firing writes

**Files:**
- Create: `internal/rules/engine.go`, `internal/rules/persist.go`, `internal/rules/engine_test.go`
- Modify: `internal/store/rules.go` (new file) — the reads and writes the engine needs

**Interfaces:**
- Consumes: `Rule`, `Suppressor`, `Decision` from Tasks 1 and 2.
- Produces:

```go
// In internal/store:
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
    MinIntervalSec int
    Aggregate      bool
    Targets        string // JSON array of target ids
    Enabled        bool
    FromDefaults   bool
}
type RuleEventRow struct {
    RuleID      int64
    ChangeID    *int64
    FiredAt     int64
    Kind        string
    SubjectKind string
    SubjectKey  int64
    Field       string
    SuppressedBy string
    DedupKey    string
}
type OutboxRow struct {
    TargetID    int64
    RuleEventID *int64
    CreatedAt   int64
    DueAt       int64
    Body        string
}
func (s *Store) SaveRule(ctx context.Context, r RuleRow) (int64, error)
func (s *Store) Rules(ctx context.Context) ([]RuleRow, error)
func (s *Store) RecordRuleEvent(ctx context.Context, e RuleEventRow) (int64, error)
func (s *Store) LastFired(ctx context.Context, ruleID int64, kind string, key int64) (int64, bool, error)
func (s *Store) SeenDedupKey(ctx context.Context, ruleID int64, key string) (bool, error)
func (s *Store) Enqueue(ctx context.Context, o OutboxRow) error
func (s *Store) EnabledTargets(ctx context.Context) ([]int64, error)

// In internal/rules:
type Engine struct {
    Store      *store.Store
    Suppressor Suppressor
    Render     func(store.ChangeRow) string
    Now        func() time.Time
}
func (e *Engine) Apply(ctx context.Context, all []Rule, ev Event) (queued int, err error)
```

- [ ] **Step 1: Write the failing test**

Create `internal/rules/engine_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"context"
	"path/filepath"
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

	queued, err := engineOn(t, s).Apply(ctx, []Rule{r}, priceEvent())
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

	queued, err := engineOn(t, s).Apply(ctx, []Rule{r}, priceEvent())
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

	if _, err := e.Apply(ctx, []Rule{r}, priceEvent()); err != nil {
		t.Fatal(err)
	}
	queued, err := e.Apply(ctx, []Rule{r}, priceEvent())
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

	queued, err := engineOn(t, s).Apply(ctx, []Rule{first, second}, priceEvent())
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

	if _, err := engineOn(t, s).Apply(ctx, []Rule{r}, priceEvent()); err != nil {
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
	if _, err := engineOn(t, s).Apply(ctx, []Rule{r}, ev); err != nil {
		t.Fatalf("a firing about a change we cannot resolve failed: %v", err)
	}
	if len(ruleEvents(t, s)) != 1 {
		t.Error("the firing was lost")
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/rules/ -run 'Apply|Dedup|RuleEvent|Trace' -v
```

Expected: FAIL to build — `undefined: Engine`.

- [ ] **Step 3: Write `internal/store/rules.go`**

The store side is plain SQL over four tables. Two queries carry a rule each:

```go
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
		ORDER BY fired_at DESC LIMIT 1`, ruleID, kind, key).Scan(&at)
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
// change the second time, and re-deciding it would make a rule that was quiet
// at midnight noisy at one o'clock about a price that moved once.
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
```

`SaveRule` upserts on id so editing a rule keeps its history — its
`rule_events` rows reference the id, and a new row would orphan them.
`Rules` reads them all. `RecordRuleEvent` and `Enqueue` are single inserts.
`SaveTarget`, `Targets`, `EnabledTargets` and `RuleEvents` are the same shape.

- [ ] **Step 4: Write `engine.go`**

```go
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
// Lifted in shape from WB_GO's internal/rules/rules.go Apply. The one thing it
// does that WB_GO's does not have to is treat a change it cannot resolve as
// still worth recording — see ruleEventFor.
type Engine struct {
	Store      *store.Store
	Suppressor Suppressor
	// Render turns a change into the body a person reads. It is a function
	// rather than an import so that internal/rules never learns what Telegram
	// is: the engine's job ends at "this should be sent".
	Render func(store.ChangeRow) string
	Now    func() time.Time
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
		body := e.Render(ev.Change)
		for _, target := range r.Targets {
			if err := e.Store.Enqueue(ctx, store.OutboxRow{
				TargetID: target, RuleEventID: &eventID,
				CreatedAt: e.Now().Unix(), DueAt: e.Now().Unix(),
				Body: body,
			}); err != nil {
				return queued, err
			}
			queued++
		}
	}
	return queued, nil
}

func (e *Engine) record(ctx context.Context, r Rule, ev Event, d Decision) (int64, error) {
	subject := subjectFor(r, ev)
	var changeID *int64
	if ev.Change.ID != 0 {
		id := ev.Change.ID
		changeID = &id
	}
	id, err := e.Store.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: r.ID, ChangeID: changeID, FiredAt: e.Now().Unix(),
		Kind: string(ev.Change.Kind),
		SubjectKind: string(subject.Kind), SubjectKey: subject.Key,
		Field: ev.Change.Field,
		SuppressedBy: string(d.Reason), DedupKey: d.DedupKey,
	})
	if err != nil {
		return 0, fmt.Errorf("rules: record firing: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/rules/ ./internal/store/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/rules/ internal/store/ && git commit -m "$(cat <<'MSG'
feat(rules): Apply, and the row every decision writes

One change against every rule. A rule that matches produces a
rule_events row whatever the decision was; a rule that does not match
produces nothing at all, and the difference matters — a rule of the wrong
kind was never about this change, and writing a row for it would fill the
table with the cartesian product of every rule and every change and bury
the rows that answer «почему мне не сказали».

Two queries whose rules are opposites, on purpose.

LastFired excludes suppressed rows, because a rate limit counts messages
somebody received; counting refusals would let one quiet-hours
suppression start the clock as if a message had gone out.

SeenDedupKey includes them, because a change refused once for any reason
is still the same change the second time, and re-deciding it would make a
rule that was quiet at midnight noisy at one o'clock about a price that
moved once.

rule_events.change_id is nullable with no foreign key, for the same
reason changes.pass_id is: retention removes old changes, and a firing
must outlive the change it rests on.

Render is a function on the Engine rather than an import, so that
internal/rules never learns what Telegram is. Its job ends at "this
should be sent".

MSG
)"
```

---

## Task 4: The nine default rules

Adding a watch creates four rules scoped to it. Five more are global and created
on first run. "The default set" appearing once and never defined is how a
headline feature goes missing, so this task enumerates it and a test counts it.

**Files:**
- Create: `internal/rules/defaults.go`, `internal/rules/defaults_test.go`

**Interfaces:**
- Produces: `func DefaultsForWatch(watchID int64, targets []int64) []Rule`; `func GlobalDefaults(targets []int64) []Rule`.

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package rules

import (
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// TestAddingAWatchCreatesFourRulesScopedToIt.
//
// Per watch and not one shared global copy, for two reasons that pull the same
// way: a rule scoped to a watch that does not exist yet cannot be written in
// advance, and a shared rule would make deleting one watch silence another.
func TestAddingAWatchCreatesFourRulesScopedToIt(t *testing.T) {
	got := DefaultsForWatch(7, []int64{1})
	if len(got) != 4 {
		t.Fatalf("%d rules, want 4", len(got))
	}
	want := map[track.Kind]bool{
		track.KindListingAppeared: true, track.KindPriceChanged: true,
		track.KindListingVanished: true, track.KindListingClosed: true,
	}
	seen := map[track.Kind]bool{}
	for _, r := range got {
		if !want[r.Kind] {
			t.Errorf("unexpected default %q", r.Kind)
		}
		seen[r.Kind] = true
		if r.Scope.Kind != ScopeWatch || r.Scope.ID != 7 {
			t.Errorf("%q is scoped to %+v, want watch 7", r.Kind, r.Scope)
		}
		if !r.FromDefaults {
			t.Errorf("%q is not marked as coming from the default set; the panel "+
				"says which rules did, so that deleting one is an informed act", r.Kind)
		}
		if !r.Enabled {
			t.Errorf("%q is created disabled", r.Kind)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%q does not validate: %v", r.Kind, err)
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("%q is missing from the default set", k)
		}
	}
}

// TestTheAppearanceRuleNeedsNoThresholdAndNoRateLimit.
//
// Its dedup key carries no numbers, so it fires at most once per listing ever.
// A floor and a limit would both be dead configuration a person still has to
// read and reason about on the Правила screen.
func TestTheAppearanceRuleNeedsNoThresholdAndNoRateLimit(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind != track.KindListingAppeared {
			continue
		}
		if r.ThresholdPct != 0 || r.ThresholdMinor != 0 {
			t.Errorf("threshold %d%% / %d minor on an appearance rule",
				r.ThresholdPct, r.ThresholdMinor)
		}
		if r.MinInterval != 0 {
			t.Errorf("MinInterval %v on a rule that fires once per listing ever",
				r.MinInterval)
		}
		return
	}
	t.Fatal("no appearance rule")
}

// TestThePriceRuleHasAFloorSoARoundingMoveIsNotNews.
func TestThePriceRuleHasAFloorSoARoundingMoveIsNotNews(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind != track.KindPriceChanged {
			continue
		}
		if r.ThresholdPct <= 0 && r.ThresholdMinor <= 0 {
			t.Error("the price rule has no floor; a one-rouble move would be a " +
				"message")
		}
		return
	}
	t.Fatal("no price rule")
}

// TestTheVanishedRuleIsEnabledByDefault, per decision 5. The honesty comes from
// the «предположительно» prefix in the message rather than from hiding the
// alert.
func TestTheVanishedRuleIsEnabledByDefault(t *testing.T) {
	for _, r := range DefaultsForWatch(1, []int64{1}) {
		if r.Kind == track.KindListingVanished && !r.Enabled {
			t.Error("the vanished rule is created disabled; decision 5 says it " +
				"ships on, and the prefix is what makes that honest")
		}
	}
}

// TestTheFiveGlobalRulesAreRateLimitedToOnePerSixHours.
//
// An install that has been flagged should say so once rather than either never
// or continuously.
func TestTheFiveGlobalRulesAreRateLimitedToOnePerSixHours(t *testing.T) {
	got := GlobalDefaults([]int64{1})
	if len(got) != 5 {
		t.Fatalf("%d global rules, want 5", len(got))
	}
	want := map[track.Kind]bool{
		track.KindTransportBlocked: true, track.KindExtractionFellBack: true,
		track.KindCollectorShapeUnknown: true, track.KindCollectorSilent: true,
		track.KindWorkerPanicked: true,
	}
	for _, r := range got {
		if !want[r.Kind] {
			t.Errorf("unexpected global default %q", r.Kind)
		}
		if r.MinInterval != 6*time.Hour {
			t.Errorf("%q has MinInterval %v, want six hours", r.Kind, r.MinInterval)
		}
		if r.Scope.Kind != ScopeFilter {
			t.Errorf("%q is scoped to %q; a health rule is about the install and "+
				"not about one watch", r.Kind, r.Scope.Kind)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%q does not validate: %v", r.Kind, err)
		}
	}
}

// TestEveryDefaultRuleNamesAKindThisBuildCanEmit. A default that can never fire
// is worse than no default: the person believes they are covered.
func TestEveryDefaultRuleNamesAKindThisBuildCanEmit(t *testing.T) {
	known := map[track.Kind]bool{}
	for _, k := range track.Kinds() {
		known[k] = true
	}
	all := append(DefaultsForWatch(1, []int64{1}), GlobalDefaults([]int64{1})...)
	for _, r := range all {
		if !known[r.Kind] {
			t.Errorf("%q is not in track.Kinds()", r.Kind)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/rules/ -run 'Default|Global|Appearance|PriceRule|Vanished' -v
```

Expected: FAIL to build — `undefined: DefaultsForWatch`.

- [ ] **Step 3: Write `defaults.go`**

```go
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
			Condition: Always{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Изменилась цена", Kind: track.KindPriceChanged,
			// Keyed on the thread by DedupKey, so a repost at the same price is
			// not news, and floored here so a rounding move is not either. Both
			// numbers, because a percentage alone lets a cheap listing shout
			// and a minor-unit floor alone lets an expensive one whisper.
			ThresholdPct: 1, ThresholdMinor: 10_000,
			Condition: Always{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Объявление пропало", Kind: track.KindListingVanished,
			// Enabled per decision 5. The message is prefixed «предположительно»
			// — see internal/telegram/message.go — which is what makes an
			// enabled-by-default removal alert honest rather than sloppy.
			Condition: Always{}, Scope: scope, Targets: targets,
			Enabled: true, FromDefaults: true,
		},
		{
			Name: "Объявление закрыто", Kind: track.KindListingClosed,
			// The confirmation of the third, carrying confidence 1.0 because an
			// item page stated it rather than a window implying it.
			Condition: Always{}, Scope: scope, Targets: targets,
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
			Condition: Always{},
			Scope:     Scope{Kind: ScopeFilter, Filter: Filter{IncludeUnpriced: true}},
			MinInterval: healthQuietFor,
			Targets:     targets, Enabled: true, FromDefaults: true,
		})
	}
	return out
}
```

`IncludeUnpriced` is true on the health scope because a transport change has no
price at all, and the default false would filter every one of them out.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/rules/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/rules/ && git commit -m "$(cat <<'MSG'
feat(rules): the nine default rules, enumerated

"The default set" appearing once in a design and never defined is how a
headline feature goes missing, so it is written out and a test counts it.

Four per watch and not one shared global copy, for two reasons that pull
the same way: a rule scoped to a watch that does not exist yet cannot be
written in advance, and a shared rule would make deleting one watch
silence another.

The appearance rule has no threshold and no rate limit, and neither is an
oversight — its dedup key carries no numbers so it fires at most once per
listing ever, and both would be dead configuration a person still has to
read. The price rule carries both a percentage and a minor-unit floor,
because a percentage alone lets a cheap listing shout and a floor alone
lets an expensive one whisper. The vanished rule ships enabled per
decision 5, and the «предположительно» prefix is what makes that honest.

The five health rules are scoped to a filter rather than to a watch,
because "my collector has been flagged" is not a fact about one query,
and they are rate-limited to one per six hours: a flagged install should
say so once rather than either never or continuously.

MSG
)"
```

---

## Task 5: `internal/notify` — the outbox worker

**Files:**
- Create: `internal/notify/notify.go`, `internal/notify/worker.go`, `internal/notify/worker_test.go`

**Interfaces:**
- Produces:

```go
type Message struct {
    TargetID int64
    Address  string
    Body     string
}
type Transport interface{ Send(context.Context, Message) error }
type TransportFunc func(context.Context, Message) error
func Backoff(attempts int) time.Duration
type Worker struct {
    Store     *store.Store
    Transport Transport
    Now       func() time.Time
    Max       int
}
type Stats struct{ Sent, Failed, GaveUp int }
func (w *Worker) Run(ctx context.Context) (Stats, error)
```

- [ ] **Step 1: Write the failing test**

```go
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

	id, err := s.RecordRuleEvent(ctx, store.RuleEventRow{
		RuleID: 1, FiredAt: 1000, Kind: "listing-appeared",
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
	if state != "queued" {
		t.Errorf("state = %q; a disabled target must not consume the queue", state)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/notify/ -v
```

Expected: FAIL to build — `undefined: Worker`.

- [ ] **Step 3: Write `notify.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify drains the outbox.
//
// Lifted from WB_GO's internal/notify, with its two load-bearing decisions
// intact and named where they live: due_at is an absolute moment, and
// rule_event_id is nullable and cleared rather than cascaded.
package notify

import "time"

// backoffs are what a failed send waits before the next attempt.
//
// They stop growing at the last entry rather than doubling for ever: an hour is
// long enough that a retry costs nothing, and a queue whose head waits eight
// days is a queue that has effectively lost the message without saying so.
var backoffs = []time.Duration{
	30 * time.Second, 2 * time.Minute, 10 * time.Minute,
	30 * time.Minute, time.Hour,
}

// maxAttempts is when a row is given up on. A row that retries for ever is a
// queue that never drains, and the panel would show a growing number nobody can
// act on.
const maxAttempts = 12

func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(backoffs) {
		attempts = len(backoffs)
	}
	return backoffs[attempts-1]
}
```

- [ ] **Step 4: Write `worker.go`**

`Run` reads due rows joined to enabled targets, sends each, and on failure
writes `attempts + 1`, `last_error` and `due_at = now + Backoff(attempts)`. Past
`maxAttempts` the state becomes `failed`.

The join to `notify_targets` filters on `enabled = 1` and the row is left
`queued` when there is no enabled target — a disabled target must not consume
the queue, or turning it back on would silently have eaten everything that
arrived while it was off.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/notify/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/notify/ && git commit -m "$(cat <<'MSG'
feat(notify): the outbox worker, with its two load-bearing decisions

due_at is an absolute moment. Storing "wait five minutes" instead of "not
before 14:05" means a machine that reboots every four minutes retries for
ever at the first backoff and never reaches the point of giving up.

rule_event_id is nullable and cleared rather than cascaded, so a message
delayed by an outage survives its rule being edited. The alternative
loses precisely the notifications that were already late.

Two more that are this target's rather than WB_GO's. The backoff stops
growing at an hour, because a queue whose head waits eight days has lost
the message without saying so. And a row for a disabled target is left
queued rather than consumed: turning a target back on must not reveal
that everything which arrived while it was off had been silently eaten.

MSG
)"
```

---

## Task 6: The Bot API rung, and the token that never leaks

**Files:**
- Create: `internal/telegram/sender.go`, `internal/telegram/bot.go`, `internal/telegram/bot_test.go`

**Interfaces:**
- Produces:

```go
type Sender interface {
    Name() string
    Check(ctx context.Context) error
    SendMessage(ctx context.Context, chat, text string) error
}
type Bot struct{ ... }
func NewBot(token string, do func(*http.Request) (*http.Response, error)) *Bot
type Ladder struct{ Rungs []Sender }
func (l *Ladder) SendMessage(ctx context.Context, chat, text string) error
func (l *Ladder) Current() string
```

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testToken = "123456:AAHsecretsecretsecret"

func reply(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// TestTheTokenNeverAppearsInAnError.
//
// The token is in the URL of every Bot API call, so any error that quotes the
// request quotes the token — into a log, a panel screen, or a bug report a
// person pastes into an issue. The redaction is lifted from WB_GO and tested
// rather than assumed.
func TestTheTokenNeverAppearsInAnError(t *testing.T) {
	cases := []struct {
		name string
		do   func(*http.Request) (*http.Response, error)
	}{
		{"a transport failure quoting the url", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("Post " + r.URL.String() + ": dial tcp: refused")
		}},
		{"an api error", func(*http.Request) (*http.Response, error) {
			return reply(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`), nil
		}},
		{"a body that is not json", func(*http.Request) (*http.Response, error) {
			return reply(200, "<html>proxy error</html>"), nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewBot(testToken, c.do).SendMessage(context.Background(), "42", "привет")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testToken) ||
				strings.Contains(err.Error(), "AAHsecret") {
				t.Fatalf("the token leaked: %v", err)
			}
		})
	}
}

// TestAnApiErrorSaysWhatTelegramSaid. "400 Bad Request" alone sends somebody to
// read our source; Telegram's own description is what they need.
func TestAnApiErrorSaysWhatTelegramSaid(t *testing.T) {
	err := NewBot(testToken, func(*http.Request) (*http.Response, error) {
		return reply(400, `{"ok":false,"error_code":400,"description":"chat not found"}`), nil
	}).SendMessage(context.Background(), "42", "привет")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("err = %v; it must carry Telegram's own description", err)
	}
}

// TestCheckUsesGetMe, which is the only call that proves the token works
// without sending anything to anybody.
func TestCheckUsesGetMe(t *testing.T) {
	var path string
	b := NewBot(testToken, func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		return reply(200, `{"ok":true,"result":{"username":"bot"}}`), nil
	})
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/getMe") {
		t.Errorf("Check called %q, want getMe", path)
	}
}

// stubSender is a rung that answers as told.
type stubSender struct {
	name  string
	check error
	send  error
	sends int
	checks int
}

func (s *stubSender) Name() string { return s.name }
func (s *stubSender) Check(context.Context) error { s.checks++; return s.check }
func (s *stubSender) SendMessage(context.Context, string, string) error {
	s.sends++
	return s.send
}

// TestTheLadderRemembersWhatWorked.
//
// A live check costs a round trip, and paying it before every notification
// would spend thirty an hour on a question whose answer changes about never.
func TestTheLadderRemembersWhatWorked(t *testing.T) {
	first := &stubSender{name: "bot-api"}
	l := &Ladder{Rungs: []Sender{first}}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := l.SendMessage(ctx, "42", "привет"); err != nil {
			t.Fatal(err)
		}
	}
	if first.checks != 1 {
		t.Errorf("checked %d times for five messages, want 1", first.checks)
	}
	if first.sends != 5 {
		t.Errorf("sent %d times, want 5", first.sends)
	}
	if l.Current() != "bot-api" {
		t.Errorf("Current() = %q; the settings screen shows it", l.Current())
	}
}

// TestTheLadderFallsThroughToTheNextRung.
func TestTheLadderFallsThroughToTheNextRung(t *testing.T) {
	blocked := &stubSender{name: "bot-api", check: errors.New("blocked")}
	working := &stubSender{name: "mtproto"}
	l := &Ladder{Rungs: []Sender{blocked, working}}

	if err := l.SendMessage(context.Background(), "42", "привет"); err != nil {
		t.Fatal(err)
	}
	if working.sends != 1 {
		t.Error("the second rung was not used")
	}
	if l.Current() != "mtproto" {
		t.Errorf("Current() = %q, want mtproto", l.Current())
	}
}

// TestTheLadderReChecksAfterAFailure, so a rung that stops working does not pin
// the product to it.
func TestTheLadderReChecksAfterAFailure(t *testing.T) {
	flaky := &stubSender{name: "bot-api"}
	spare := &stubSender{name: "mtproto"}
	l := &Ladder{Rungs: []Sender{flaky, spare}}
	ctx := context.Background()

	if err := l.SendMessage(ctx, "42", "первое"); err != nil {
		t.Fatal(err)
	}
	flaky.send = errors.New("now blocked")
	if err := l.SendMessage(ctx, "42", "второе"); err != nil {
		t.Fatal(err)
	}
	if spare.sends != 1 {
		t.Error("a failure on the remembered rung did not fall through")
	}
}

// TestALadderWithNoWorkingRungSaysSoOnce. An error naming every rung and what
// each said is what a person needs; "could not send" is not.
func TestALadderWithNoWorkingRungSaysSoOnce(t *testing.T) {
	a := &stubSender{name: "bot-api", check: errors.New("api.telegram.org blocked")}
	b := &stubSender{name: "mtproto", check: errors.New("no api id configured")}
	err := (&Ladder{Rungs: []Sender{a, b}}).SendMessage(context.Background(), "42", "привет")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"bot-api", "blocked", "mtproto", "api id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q; it does not mention %q", err, want)
		}
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/telegram/ -v
```

Expected: FAIL to build — `undefined: NewBot`.

- [ ] **Step 3: Write `sender.go`**

```go
// Sender is one way to deliver a message.
//
// It is an interface from the first commit rather than from the commit that
// needs two, and the reason is structural: MTProto is not HTTP. It cannot be
// handed an *http.Request, so it could never have been a rung of an HTTP
// transport ladder however the code was arranged. WB_GO built that ladder one
// level too low and had to move it; this starts where it ended up.
//
// Three verbs, which is what M1 needs. SendDocument and SendPhoto belong to
// increment 6's export and chart, and adding them now would be an interface
// with two implementations neither of which anything calls.
type Sender interface {
	// Name is what the settings screen shows. "Which path is my Telegram on"
	// is a question the user asks, and an unnamed rung can only be described
	// as "the one that worked".
	Name() string
	// Check reports whether this rung can deliver right now. For the Bot API it
	// is getMe; for MTProto it is the session coming up. It is the only check
	// that proves what a message will actually need, which is why the ladder
	// makes it rather than assuming a rung that dialled is a rung that works.
	Check(ctx context.Context) error
	SendMessage(ctx context.Context, chat, text string) error
}
```

`Ladder` caches the working rung, re-checks after a send failure, and returns an
error naming every rung and what each said.

- [ ] **Step 4: Write `bot.go`**

Lifted from WB_GO's, with `SendDocument`, `SendPhoto` and `splitThread` left
behind. Keep `redact` exactly: it wraps every returned error and replaces the
token with `<токен>`.

- [ ] **Step 5: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/telegram/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test and subtest PASS.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/telegram/ && git commit -m "$(cat <<'MSG'
feat(telegram): the Bot API rung, and a token that stays out of errors

The token is in the URL of every Bot API call, so any error quoting the
request quotes the token — into a log, a panel screen, or a bug report
somebody pastes into an issue. WB_GO's redaction is lifted and tested
across three shapes of failure rather than assumed: a transport error
that quotes the URL, an API error, and a body that is not JSON at all.

Sender is an interface from the first commit rather than from the commit
that needs two, and the reason is structural rather than stylistic:
MTProto is not HTTP and cannot be handed an *http.Request, so it could
never have been a rung of an HTTP transport ladder however the code was
arranged. WB_GO built that ladder one level too low and had to move it.

Three verbs and not five. SendDocument and SendPhoto belong to increment
6's export and chart; adding them now would be an interface with two
implementations neither of which anything calls.

The ladder remembers what worked — a live check costs a round trip, and
paying it before every notification would spend thirty an hour on a
question whose answer changes about never — and re-checks after a
failure, so a rung that stops working does not pin the product to it.

MSG
)"
```

---

## Task 7: The message a person actually reads

**Files:**
- Create: `internal/telegram/message.go`, `internal/telegram/message_test.go`

**Interfaces:**
- Produces:

```go
type Subject struct {
    WatchName   string
    Title       string
    URLPath     string
    PreviousID  int64 // for a repost, the number it replaces
}
func Render(c store.ChangeRow, s Subject) string
```

- [ ] **Step 1: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func subject() Subject {
	return Subject{
		WatchName: "iPhone 15", Title: "iPhone 15, 128 ГБ",
		URLPath: "/m/x_7800000001",
	}
}

// TestAVanishedMessageIsPrefixedPredpolozhitelno.
//
// Decision 5 enables this alert by default, and 0.3 is a conclusion drawn from
// something not being in a window rather than a fact a page stated. The prefix
// is what makes an enabled-by-default removal alert honest rather than sloppy.
func TestAVanishedMessageIsPrefixedPredpolozhitelno(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingVanished, ItemID: 7800000001, Confidence: 0.3,
	}, subject())

	if !strings.HasPrefix(got, "Предположительно") {
		t.Errorf("message = %q; a 0.3 removal must say how sure it is", got)
	}
	if !strings.Contains(got, "iPhone 15") {
		t.Error("the message does not say which watch stopped seeing it")
	}
}

// TestAClosedMessageStatesItAsFact, because an item page said so and the
// confidence is 1.0.
func TestAClosedMessageStatesItAsFact(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingClosed, Confidence: 1.0,
	}, subject())
	if strings.Contains(strings.ToLower(got), "предположительно") {
		t.Errorf("a stated fact was hedged: %q", got)
	}
}

// TestAPriceMessageCarriesBothNumbersAndTheDirection.
//
// A message saying only «цена изменилась» makes the reader open the app to find
// out which way, which is the one thing a notification is supposed to save them.
func TestAPriceMessageCarriesBothNumbersAndTheDirection(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindPriceChanged, Field: "price",
		Was: "4000000", Now: "3500000", Unit: track.UnitMinor,
		HadBefore: true, HasNow: true, Confidence: 1.0,
	}, subject())

	for _, want := range []string{"40 000", "35 000"} {
		if !strings.Contains(got, want) {
			t.Errorf("message = %q; it does not carry %q", got, want)
		}
	}
	if !strings.Contains(got, "↓") && !strings.Contains(got, "упала") {
		t.Errorf("message = %q; it does not say which way", got)
	}
}

// TestMoneyIsRenderedFromMinorUnitsAndNeverFromAFloat.
//
// The single highest-consequence line in this codebase is the unit boundary. A
// hundredfold error still renders as a plausible number, which is exactly why
// it has to be tested at the point a person reads it.
func TestMoneyIsRenderedFromMinorUnitsAndNeverFromAFloat(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindPriceChanged, Was: "100", Now: "199",
		Unit: track.UnitMinor, HadBefore: true, HasNow: true,
	}, subject())
	if !strings.Contains(got, "1,00") && !strings.Contains(got, "1 ₽") {
		t.Errorf("message = %q; 100 minor units is one rouble", got)
	}
	if strings.Contains(got, "100 ₽") || strings.Contains(got, "199 ₽") {
		t.Errorf("message = %q; minor units were printed as roubles", got)
	}
}

// TestARepostNamesTheNumberItReplaces, so the reader can check.
func TestARepostNamesTheNumberItReplaces(t *testing.T) {
	s := subject()
	s.PreviousID = 7700000001
	got := Render(store.ChangeRow{
		Kind: track.KindListingReposted, Confidence: 0.8,
	}, s)
	if !strings.Contains(got, "7700000001") {
		t.Errorf("message = %q; a repost must name the old number", got)
	}
}

// TestEveryKindRenders.
//
// A kind with no case falls through to its own identifier, which reaches a
// person as "listing-left-window" and tells them nothing. The catalogue is
// closed, so this can be exhaustive.
func TestEveryKindRenders(t *testing.T) {
	for _, k := range track.Kinds() {
		got := Render(store.ChangeRow{Kind: k, Confidence: 1.0}, subject())
		if got == "" {
			t.Errorf("%q renders as nothing", k)
			continue
		}
		if strings.Contains(got, string(k)) {
			t.Errorf("%q renders as its own identifier: %q — that reaches a "+
				"person as jargon", k, got)
		}
	}
}

// TestTheListingIsLinkedSoTheReaderCanOpenIt.
func TestTheListingIsLinkedSoTheReaderCanOpenIt(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingAppeared, ItemID: 7800000001, Confidence: 1.0,
	}, subject())
	if !strings.Contains(got, "avito.ru/m/x_7800000001") {
		t.Errorf("message = %q; it carries no link", got)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd S:/Projects/Avito_Go && go test ./internal/telegram/ -run 'Message|Render|Vanished|Closed|Price|Repost|Kind|Linked' -v
```

Expected: FAIL to build — `undefined: Render`.

- [ ] **Step 3: Write `message.go`**

Russian copy. One `switch` over the closed catalogue with no default that prints
the identifier — a `default` that does is how jargon reaches a person.

```go
// hedge is what a conclusion drawn from a window is worth saying.
//
// Decision 5 ships the removal alert enabled, and 0.3 means the listing left a
// window rather than a page stating it was closed. The prefix is the whole of
// what makes that honest: an enabled-by-default removal alert that did not say
// how sure it was would be sloppy, and turning the alert off instead would lose
// the feature people asked for.
func hedge(confidence float64) string {
	if confidence < 1.0 {
		return "Предположительно "
	}
	return ""
}

// rubles renders integer minor units.
//
// Never from a float, and this is the single highest-consequence line in the
// codebase: Avito states prices in whole roubles, Wildberries states them in
// kopecks, and code was lifted from a Wildberries project. A hundredfold error
// still sorts correctly and still renders as a plausible number.
func rubles(minor int64) string {
	whole, frac := minor/100, minor%100
	if frac < 0 {
		frac = -frac
	}
	// Thin spaces between thousands, which is how a Russian reader expects a
	// price to look, and the kopecks only when there are any — «40 000 ₽» and
	// not «40 000,00 ₽», because the second reads as a spreadsheet.
	out := group(whole)
	if frac != 0 {
		out += fmt.Sprintf(",%02d", frac)
	}
	return out + " ₽"
}

// group inserts a separator every three digits from the right.
func group(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ') // a non-breaking space: a price must not wrap
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go test ./internal/telegram/ -v 2>&1 | grep -E "^(---|ok|FAIL)"
```

Expected: every test PASS.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add internal/telegram/ && git commit -m "$(cat <<'MSG'
feat(telegram): the message a person actually reads

Messages carry the evidence and not just the verdict.

A listing-vanished at confidence 0.3 is prefixed «предположительно» and
says which watch stopped seeing it. That prefix is the whole of what
makes an enabled-by-default removal alert honest: turning the alert off
instead would lose the feature people asked for, and sending it unhedged
would spend their trust on a guess.

A price message carries both numbers and the direction, because a message
saying only «цена изменилась» makes the reader open the app to find out
which way — the one thing a notification is supposed to save them.

Money renders from integer minor units and never from a float. That is
the single highest-consequence line in this codebase: Avito states prices
in whole roubles, Wildberries in kopecks, and code here was lifted from a
Wildberries project. A hundredfold error still renders as a plausible
number, so it is tested at the point a person reads it.

The switch over the closed catalogue has no default that prints the kind
identifier. A missing case would reach somebody as "listing-left-window",
which tells them nothing, so a test walks track.Kinds() and refuses any
rendering that contains its own kind name.

MSG
)"
```

---

## Task 8: MTProto, verified rather than trusted

WB_GO ships this transport with its live exchange never verified against real
Telegram. It is lifted as a starting point and finished here, and the last step
of this task is the verification — which is the reason MTProto is in M1 at all.

**Files:**
- Create: `internal/telegram/mtproto.go`, `internal/telegram/mtproto_test.go`
- Modify: `internal/store/settings.go` — the session blob accessors

**Interfaces:**
- Produces:

```go
type MTConfig struct {
    APIID     int
    APIHash   string
    BotToken  string
    Session   SessionStore
}
type SessionStore interface {
    LoadSession(ctx context.Context) ([]byte, error)
    StoreSession(ctx context.Context, data []byte) error
}
func NewMTProto(cfg MTConfig) *MTProto  // satisfies Sender
```

- [ ] **Step 1: Take the dependency**

```bash
cd S:/Projects/Avito_Go && go get github.com/gotd/td@latest && go mod tidy && \
  go list -m all | wc -l
```

Expected: about forty modules more than before. That is the known price and the
reason is the spec's: `api.telegram.org` is blocked for part of the Russian
audience, which is this product's audience, and a user in direct mode behind
that block otherwise has no path at all.

- [ ] **Step 2: Write the failing test**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memSession is a SessionStore that keeps the blob in memory.
type memSession struct{ data []byte }

func (m *memSession) LoadSession(context.Context) ([]byte, error) { return m.data, nil }
func (m *memSession) StoreSession(_ context.Context, d []byte) error {
	m.data = append([]byte(nil), d...)
	return nil
}

// TestCheckFailsClosedWithoutCredentials.
//
// The Bot API rung needs only a token; MTProto's bot login needs the token AND
// an application identity from my.telegram.org. A half-configured rung that
// fails at send time is worse than one that refuses at check time, because the
// ladder would have committed to it.
func TestCheckFailsClosedWithoutCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  MTConfig
	}{
		{"nothing at all", MTConfig{Session: &memSession{}}},
		{"a token but no api id", MTConfig{BotToken: "123:AAH", Session: &memSession{}}},
		{"an api id but no hash", MTConfig{APIID: 1, BotToken: "123:AAH", Session: &memSession{}}},
		{"an api id and hash but no token", MTConfig{APIID: 1, APIHash: "h", Session: &memSession{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewMTProto(c.cfg).Check(context.Background())
			if err == nil {
				t.Fatal("Check passed with incomplete credentials")
			}
			// The message goes on the settings screen, so it must say which
			// field is missing rather than "not configured".
			if !strings.Contains(err.Error(), "api") &&
				!strings.Contains(err.Error(), "token") {
				t.Errorf("err = %q; it does not name what is missing", err)
			}
		})
	}
}

// TestNoCredentialEverReachesAFileThisProgramWrites.
//
// The session is a credential in the strongest sense: anyone holding it is the
// bot. gotd's convenient default is a file beside the binary, which is exactly
// what this must not do — the blob belongs in settings.telegram_mtproto_session,
// a column the panel does not display.
func TestNoCredentialEverReachesAFileThisProgramWrites(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Cleanup(func() { os.Chdir(wd) })

	sess := &memSession{}
	m := NewMTProto(MTConfig{
		APIID: 1, APIHash: "hash", BotToken: "123:AAHsecret", Session: sess,
	})
	// Check will fail — there is no Telegram here — and that is fine. What must
	// not happen is a file appearing.
	_ = m.Check(context.Background())

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("MTProto wrote %q into the working directory", filepath.Join(dir, e.Name()))
	}
}

// TestTheSessionRoundTripsThroughTheStore, so a restart does not re-authorise.
func TestTheSessionRoundTripsThroughTheStore(t *testing.T) {
	sess := &memSession{}
	if err := sess.StoreSession(context.Background(), []byte("blob")); err != nil {
		t.Fatal(err)
	}
	got, err := sess.LoadSession(context.Background())
	if err != nil || string(got) != "blob" {
		t.Errorf("round trip gave %q, %v", got, err)
	}
}

// TestNameIsStableSoTheSettingsScreenCanShowIt.
func TestNameIsStableSoTheSettingsScreenCanShowIt(t *testing.T) {
	if n := NewMTProto(MTConfig{Session: &memSession{}}).Name(); n != "mtproto" {
		t.Errorf("Name() = %q, want mtproto", n)
	}
}
```

- [ ] **Step 3: Write `mtproto.go` and the store's session accessors**

`SessionStore` is satisfied by a small type over `settings.telegram_mtproto_session`:

```go
// StoreSession keeps the MTProto session in the database rather than beside the
// binary.
//
// It is a credential in the strongest sense — anyone holding it is the bot —
// and gotd's convenient default is a file in the working directory. The column
// is a BLOB the panel does not display, and this is the only way in or out of
// it.
func (s *Store) StoreMTProtoSession(ctx context.Context, data []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE settings SET telegram_mtproto_session = ?, updated_at = ?
		 WHERE id = 1`, data, s.now().UTC().Unix()); err != nil {
		// The blob is never interpolated into this message, and neither is
		// anything derived from it.
		return fmt.Errorf("store: mtproto session: %w", err)
	}
	return nil
}

func (s *Store) MTProtoSession(ctx context.Context) ([]byte, error) {
	var b []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT telegram_mtproto_session FROM settings WHERE id = 1`).
		Scan(&b); err != nil {
		return nil, fmt.Errorf("store: mtproto session: %w", err)
	}
	return b, nil
}
```

`Check` validates the three fields before dialling anything, and returns an
error naming the missing one.

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd S:/Projects/Avito_Go && gofmt -w ./internal && go build ./... && \
  go test ./internal/telegram/ -v 2>&1 | grep -E "^(---|ok|FAIL|    )"
```

Expected: every test PASS.

- [ ] **Step 5: Verify the live exchange — the point of this task**

This needs real credentials from my.telegram.org and a real bot token, and it is
the one step in this plan that cannot run unattended. **Ask the operator for
them rather than inventing any.**

```bash
cd S:/Projects/Avito_Go && go run ./cmd/avitomon notify test \
  -db avito.db -rung mtproto -chat <chat id>
```

Then write `docs/measurements/2026-08-XX-mtproto.md` recording what the exchange
actually did: whether the bot login needed anything beyond the token and the
application identity, how long the first session took to establish, what the
second run cost with the session restored, and what the failure looks like when
the api id is wrong.

**If it does not work, say so in that document and leave the rung out of the
default ladder.** A delivery path nobody has seen work is worse than an absent
one, because the settings screen would offer it and a person would rely on it.

- [ ] **Step 6: Commit**

```bash
cd S:/Projects/Avito_Go && git add go.mod go.sum internal/ docs/measurements/ && \
  git commit -m "$(cat <<'MSG'
feat(telegram): the MTProto rung, and what its live exchange actually did

gotd/td is by a wide margin the largest line in the dependency budget —
some forty indirect modules for one delivery rung — and it is carried
knowingly. api.telegram.org is blocked for part of the Russian audience,
which is this product's audience, and a user in direct mode behind that
block otherwise has no path at all.

WB_GO ships this transport with its live exchange never verified against
real Telegram. It is lifted as a starting point and finished here, and
what the exchange did is written down rather than assumed.

Check validates the three credentials before dialling and names the
missing one. The Bot API rung needs only a token; MTProto's bot login
needs the token AND an application identity, and a half-configured rung
that fails at send time is worse than one that refuses at check time —
the ladder would already have committed to it.

The session goes into settings.telegram_mtproto_session and nowhere else.
It is a credential in the strongest sense, anyone holding it is the bot,
and gotd's convenient default is a file beside the binary. A test chdirs
into an empty directory and fails if anything appears there.

MSG
)"
```

---

## Task 9: `avitomon notify`, and the first message end to end

**Files:**
- Create: `cmd/avitomon/notify.go`
- Modify: `cmd/avitomon/main.go`, `cmd/avitomon/watch.go`, `cmd/avitomon/run.go`

- [ ] **Step 1: Write `notify.go`**

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/notify"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/telegram"
)

func notifyCmd(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	dbPath := fs.String("db", "avito.db", "database file")
	test := fs.Bool("test", false, "send one message to prove the path works")
	chat := fs.String("chat", "", "-test: which chat")
	rung := fs.String("rung", "", "-test: force one rung (bot-api|mtproto)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	sender, err := buildLadder(ctx, s, *rung)
	if err != nil {
		return err
	}

	if *test {
		if *chat == "" {
			return errors.New("-test needs -chat")
		}
		if err := sender.SendMessage(ctx, *chat, "Avito Monitor: проверка связи."); err != nil {
			return err
		}
		fmt.Printf("sent through %s\n", sender.Current())
		return nil
	}

	w := &notify.Worker{
		Store: s, Max: 100, Now: time.Now,
		Transport: notify.TransportFunc(func(ctx context.Context, m notify.Message) error {
			return sender.SendMessage(ctx, m.Address, m.Body)
		}),
	}
	st, err := w.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("sent %d, failed %d, gave up on %d (path: %s)\n",
		st.Sent, st.Failed, st.GaveUp, sender.Current())
	return nil
}
```

- [ ] **Step 2: Create the default rules where a watch is created**

In `watch.go`, after `AddWatch`, save `rules.DefaultsForWatch(id, targets)`; on
an empty `rules` table also save `rules.GlobalDefaults(targets)`. Print what was
created:

```go
	fmt.Printf("watch %d added, with %d default rules\n", id, len(made))
	for _, r := range made {
		fmt.Printf("  %s\n", r.Name)
	}
```

A rule a person did not know they had is a rule they cannot turn off.

- [ ] **Step 3: Apply the rules where a pass is saved**

In `run.go`, after `SavePass`, read the changes that pass produced and run them
through `Engine.Apply`. The engine is built here rather than inside the store,
because `internal/store` must not learn what a rule is.

- [ ] **Step 4: Run the increment's demonstrable behaviour**

```bash
cd S:/Projects/Avito_Go && D=/tmp/inc5.db && rm -f "$D"* && \
  go run ./cmd/avitomon watch add -db "$D" "https://m.avito.ru/all?q=iphone+15&s=104" && \
  go run ./cmd/avitomon transport -db "$D" set direct && \
  go run ./cmd/avitomon run --once -db "$D"
```

Expected: thirty arrivals, and **no messages queued** — each suppressed with
`suppressed_by = 'seed'`. Check it:

```bash
cd S:/Projects/Avito_Go && sqlite3 /tmp/inc5.db \
  "SELECT suppressed_by, count(*) FROM rule_events GROUP BY 1;
   SELECT count(*) AS queued FROM notify_outbox;"
```

Expected: `seed|30` and `queued|0`. That is the increment's third demonstrable —
*a `rule_events` row exists for every suppression with its reason.*

Then, with a target configured and a second pass that finds something genuinely
new, exactly one message per new listing, and none for a listing that flapped.

- [ ] **Step 5: Commit**

```bash
cd S:/Projects/Avito_Go && git add cmd/avitomon/ && git commit -m "$(cat <<'MSG'
feat(cmd): avitomon notify, and the seeding pass that says nothing

watch add now creates the four default rules and prints them, because a
rule a person did not know they had is a rule they cannot turn off. The
five global health rules are created on first run.

run --once applies the rules to the changes its pass produced. The engine
is built in the command rather than inside the store, so internal/store
never learns what a rule is.

The demonstrable is again the quiet one: a seeding pass produces thirty
arrivals, thirty rule_events rows with suppressed_by = 'seed', and zero
queued messages. Nobody is told about thirty listings that were already
there — and the reason each was withheld is in the table, because
«почему мне не сказали» has to be answerable.

MSG
)"
```

---

## Done when

- `go build ./...` and `go vet ./...` are clean; `go test ./...` passes.
- A new listing on a seeded watch produces exactly one Telegram message.
- The same listing flapping in and out of the window produces no second one.
- A `rule_events` row exists for every suppression, carrying its reason and its dedup key; a rule that never matched leaves no row at all.
- The seeding pass's arrivals are all suppressed with `suppressed_by = 'seed'`.
- A `listing-vanished` message is prefixed «предположительно» and names the watch.
- The Bot API token appears in no error, under three different shapes of failure.
- Adding a watch creates exactly four rules scoped to it, all marked as coming from the default set, and the five global health rules exist after the first run.
- No credential reaches a file this program writes, proved by a test that runs in an empty directory.
- **MTProto's live exchange is verified against real Telegram and written up — including, if that is what happens, that it did not work.**


## What this increment deliberately does not build

Named, because each of these appears in the spec's §Notifications and a reader
who found it missing would reasonably assume it was forgotten.

**`ReasonRepost` exists and nothing sets it.** The repost linker is increment 7,
so no alert is withheld for that reason yet. The constant is defined now
because `rule_events.suppressed_by` is a TEXT column with no CHECK and a reason
added later would be a value older rows could not have — better a reason that
is never used this milestone than a column whose vocabulary grew.

**No inline button.** «Это не перепубликация» pins a pair and replays a withheld
alert, and there is nothing to pin and nothing withheld until the linker exists.
`Render` names the previous number on a `listing-reposted` so the message is
already useful, and the button is one field on the send call when increment 7
has something for it to do.

**No confirmation sweeper.** The spec says a `listing-vanished` guess becomes a
stated fact when a later sweep confirms it, and that second message carries
confidence 1.0. The sweeper spends one request per listing on item pages, which
is a collection decision rather than a notification one; `KindListingClosed` and
its default rule are here and ready for it.

**`SendDocument` and `SendPhoto` are not on the `Sender` interface.** They belong
to increment 6's export and chart. Adding them now would mean two
implementations of two methods nothing calls, and MTProto's would be written
against a use case nobody had exercised.

---

## What increment 6 inherits

`notify_outbox` rows that a worker drains, a `Sender` ladder that already knows
which rung works, and `rules` rows the Правила screen edits. Increment 6's panel
adds `SendDocument` and `SendPhoto` to the seam for the chart and the export;
nothing in this increment needs them, which is why they are not here.
