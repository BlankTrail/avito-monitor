// SPDX-License-Identifier: AGPL-3.0-or-later

// Package rules decides what a person is told about.
//
// Lifted from WB_GO's internal/rules, which reads internal/track and nothing
// else of that project's — deleting its second event catalogue removed work
// rather than adding it. Three edits, and two of them exist because a listing
// here has incarnations: see ScopeKind, DedupKey and Suppressor.LastFired.
package rules

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// Event is one change with the context a rule needs to judge it.
//
// The change row already carries the watch, so the watch is not repeated here:
// two places holding one fact is two places to disagree. The thread and the
// seller are not on it and are looked up, because a change is about a listing
// and a rule may be about the thing that listing advertises or about who is
// selling it.
type Event struct {
	Change   store.ChangeRow
	ThreadID int64
	SellerID int64

	// Position is where the listing stood in the results, when that is known.
	// A pointer because most changes do not come from a window pass at all,
	// and a zero would read as "first" — the single most misleading value this
	// field could take.
	Position *int64
}

// value is what a condition leaf reads. The bool is false when there is no
// value, which the tree turns into a false leaf whatever the comparison — a
//
// price that was never read must not satisfy "under a thousand".
// float64 throughout, which is exact for every integer this product measures:
// the type holds whole numbers up to 2^53, and a price in kopecks reaches that
// at ninety trillion roubles. The arithmetic that must not lose a kopeck
// happens in int64 upstream in track; this is only the comparison.
func (ev Event) value(f Field) (float64, bool) {
	switch f {
	case FieldWas:
		return ev.was()
	case FieldNow:
		return ev.now()
	case FieldPriceDelta:
		was, okWas := ev.was()
		now, okNow := ev.now()
		if !okWas || !okNow {
			return 0, false
		}
		return now - was, true
	case FieldPricePct:
		was, okWas := ev.was()
		now, okNow := ev.now()
		if !okWas || !okNow {
			return 0, false
		}
		return track.PercentChange(int64(was), int64(now))
	case FieldConfidence:
		return ev.Change.Confidence, true
	case FieldWatchID:
		return float64(ev.Change.WatchID), true
	case FieldSellerID:
		return float64(ev.SellerID), true
	case FieldPosition:
		if ev.Position == nil {
			return 0, false
		}
		return float64(*ev.Position), true
	}
	return 0, false
}

// was and now read the two sides of the move.
//
// Both gates apply and both are load-bearing. HadBefore is the row saying there
// was an earlier value at all — a price-appeared has none. The parse is the
// second gate, because these columns are TEXT: they hold a title and a
// moderation reason as well as a price, and "Было > 1000" against «iPhone 15»
// must be false rather than an error or a zero.
func (ev Event) was() (float64, bool) {
	if !ev.Change.HadBefore {
		return 0, false
	}
	return parseNumber(ev.Change.Was)
}

func (ev Event) now() (float64, bool) {
	if !ev.Change.HasNow {
		return 0, false
	}
	return parseNumber(ev.Change.Now)
}

func parseNumber(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ScopeKind is what a rule is scoped to.
//
// WB_GO has product | category | supplier | job. This target's grain differs in
// one way that matters: a listing has incarnations, so a rule can be about THIS
// advertisement or about the thing it advertises across reposts, and those are
// two different subscriptions.
type ScopeKind string

const (
	ScopeListing ScopeKind = "listing"
	ScopeThread  ScopeKind = "thread"
	ScopeSeller  ScopeKind = "seller"
	ScopeWatch   ScopeKind = "watch"
	ScopeFilter  ScopeKind = "filter"
)

// Filter is a scope that is a query rather than a subject.
type Filter struct {
	Kinds         []track.Kind
	PriceMinMinor int64
	PriceMaxMinor int64

	// IncludeUnpriced admits a change whose price was not read into a price
	// band. Default false, which is WB_GO's reasoning — a band cannot include
	// something whose price is unknown — but the flag exists because on this
	// site «Цена договорная» is ordinary rather than an edge case, and a person
	// watching a band may well want those too.
	IncludeUnpriced bool
}

// Scope says which changes a rule is about.
type Scope struct {
	Kind   ScopeKind
	ID     int64
	Filter Filter
}

// Covers reports whether this scope admits the event.
//
// An unknown scope covers nothing, and that default is deliberate: a scope this
// build cannot understand — a hand-edited database, a rule written by a newer
// release — must notify on nothing rather than on every change in the database.
func (s Scope) Covers(ev Event) bool {
	switch s.Kind {
	case ScopeListing:
		return ev.Change.ItemID == s.ID
	case ScopeThread:
		return ev.ThreadID == s.ID
	case ScopeSeller:
		return ev.SellerID == s.ID
	case ScopeWatch:
		return ev.Change.WatchID == s.ID
	case ScopeFilter:
		return s.Filter.covers(ev)
	}
	return false
}

func (f Filter) covers(ev Event) bool {
	if len(f.Kinds) > 0 {
		ok := false
		for _, k := range f.Kinds {
			if k == ev.Change.Kind {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.PriceMinMinor == 0 && f.PriceMaxMinor == 0 {
		return true
	}
	price, ok := ev.now()
	if !ok {
		return f.IncludeUnpriced
	}
	// Inclusive of both ends: a person typing 40000 as the top means "and forty
	// thousand", not "up to but not including".
	if f.PriceMinMinor != 0 && price < float64(f.PriceMinMinor) {
		return false
	}
	if f.PriceMaxMinor != 0 && price > float64(f.PriceMaxMinor) {
		return false
	}
	return true
}

// Rule is one instruction: tell me when this happens.
type Rule struct {
	ID   int64
	Name string
	// Kind is the one change kind this rule watches. One kind per rule rather
	// than a set, because the condition is written in that kind's unit — a
	// threshold in kopecks means nothing against a confidence.
	Kind      track.Kind
	Condition Node
	Scope     Scope

	// Urgent exempts the rule from quiet hours, and nothing else. It is the
	// single exemption the design allows, so that "urgent" keeps meaning
	// something.
	Urgent bool

	// ThresholdPct and ThresholdMinor are the significance floor: a move
	// smaller than either is not worth a message. Both zero means every move
	// counts. They are separate from the condition on purpose — a person
	// setting up a rule thinks "tell me about price falls" first and "but not
	// for eight kopecks" second, and folding the second into the tree would
	// make the common case a two-node condition.
	ThresholdPct   int
	ThresholdMinor int64

	// MinInterval is the floor on how often this rule may fire about one
	// subject. Zero means no floor.
	MinInterval time.Duration

	// Aggregate says this rule's firings should be batched into one message.
	Aggregate bool

	Targets []int64
	Enabled bool

	// FromDefaults marks a rule the program created rather than a person. The
	// panel says so, which is what makes deleting one an informed act rather
	// than a surprise later.
	FromDefaults bool
}

// Matches reports whether the rule applies to this change at all — before any
// suppression. Kind, scope and condition, in that order, cheapest first: the
// tree is the only one of the three that can be arbitrarily deep.
func (r Rule) Matches(ev Event) bool {
	if !r.Enabled {
		return false
	}
	if r.Kind != ev.Change.Kind {
		return false
	}
	if !r.Scope.Covers(ev) {
		return false
	}
	return r.Condition.Eval(ev)
}

// Validate reports every reason a rule cannot be saved, at once.
//
// A rule that cannot fire is worse than no rule, because the person believes
// they are covered. And a form that rejects one field at a time is a form
// somebody submits five times.
func (r Rule) Validate() error {
	var bad []string

	if r.Kind == "" {
		bad = append(bad, "не выбран вид изменения (kind)")
	} else {
		known := false
		for _, k := range track.Kinds() {
			if k == r.Kind {
				known = true
				break
			}
		}
		if !known {
			bad = append(bad, fmt.Sprintf(
				"вид изменения %q эта сборка не умеет присылать (kind)", r.Kind))
		}
	}

	if len(r.Targets) == 0 {
		bad = append(bad, "не выбран получатель (target)")
	}
	if r.ThresholdPct < 0 || r.ThresholdMinor < 0 {
		bad = append(bad, "порог (threshold) не может быть отрицательным")
	}
	if r.MinInterval < 0 {
		bad = append(bad, "интервал не может быть отрицательным")
	}

	switch r.Scope.Kind {
	case ScopeListing, ScopeThread, ScopeSeller, ScopeWatch:
		if r.Scope.ID == 0 {
			bad = append(bad, fmt.Sprintf("не указан предмет для области %q", r.Scope.Kind))
		}
	case ScopeFilter:
		f := r.Scope.Filter
		if f.PriceMinMinor != 0 && f.PriceMaxMinor != 0 && f.PriceMinMinor > f.PriceMaxMinor {
			bad = append(bad, "нижняя граница цены выше верхней")
		}
	default:
		bad = append(bad, fmt.Sprintf("область %q эта сборка не знает", r.Scope.Kind))
	}

	if err := r.Condition.Validate(); err != nil {
		bad = append(bad, err.Error())
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("rules: %s", strings.Join(bad, "; "))
}

// DedupKey identifies "this same change again".
//
// Two departures from WB_GO's, and both are about reposts.
//
// Price and title kinds key on the THREAD rather than on the listing. Without
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
		parts = append(parts, strconv.FormatInt(c.ItemID, 10))

	case track.KindPriceChanged, track.KindPriceAppeared,
		track.KindPriceWithdrawn, track.KindDiscountChanged,
		track.KindTitleChanged:
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
