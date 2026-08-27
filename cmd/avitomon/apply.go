// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"strconv"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/rules"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/telegram"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// applyRules runs every rule against every change this pass produced, and
// returns how many messages reached the queue.
//
// The engine is assembled here rather than inside the store, so that
// internal/store never learns what a rule is and internal/rules never learns
// what Telegram is. This command is the one place that knows all three.
func applyRules(ctx context.Context, s *store.Store, w store.Watch, since int64) (int, error) {
	all, err := rules.All(ctx, s)
	if err != nil {
		return 0, err
	}
	if len(all) == 0 {
		return 0, nil
	}
	changes, err := s.Changes(ctx, w.ID, since, 1000)
	if err != nil {
		return 0, err
	}
	if len(changes) == 0 {
		return 0, nil
	}

	// The names the messages will carry, read once for the whole pass rather
	// than once per change: a seeding pass produces thirty changes over thirty
	// listings, and thirty queries to answer "what is this called" is
	// twenty-nine more than this needs.
	items := make([]int64, 0, len(changes))
	for _, c := range changes {
		items = append(items, c.ItemID)
	}
	named, err := s.ListingNames(ctx, items)
	if err != nil {
		return 0, err
	}

	e := buildEngine(s, named, w)
	queued := 0
	for _, c := range changes {
		ev := rules.Event{Change: c}
		// The predecessor, when this listing was linked to one. It is what
		// makes suppress.Decide answer ReasonRepost — the field and the reason
		// have both existed since increment 5 with nothing to set them, and
		// this is the line that finally does.
		//
		// Read per change rather than joined into the query: only an
		// appearance can be a repost, so this is one indexed lookup on the few
		// changes of that kind rather than a column on every row.
		if c.Kind == track.KindListingAppeared || c.Kind == track.KindListingReposted {
			predecessor, err := s.RepostOf(ctx, c.ItemID)
			if err != nil {
				return queued, err
			}
			if predecessor != 0 {
				ev.RepostOf = &predecessor
			}
		}
		n, err := e.Apply(ctx, all, ev)
		if err != nil {
			return queued, err
		}
		queued += n
	}
	// The aggregating rules have queued nothing yet. One Engine is one pass,
	// and the pass is the boundary that decides what «вместо сорока сообщений
	// одно» counts the forty inside.
	n, err := e.Flush(ctx)
	return queued + n, err
}

// buildEngine puts the three halves together.
//
// Both history lookups treat a failed query as "no history", which errs towards
// sending. That is deliberate and it is the same choice twice: the alternative
// would write a reason into rule_events.suppressed_by that is not the reason a
// message was withheld — and that column exists to answer «почему мне не
// сказали». A duplicate message is visible and annoying; a real one suppressed
// and filed as a duplicate is invisible.
func buildEngine(s *store.Store, named map[int64]store.ListingName, w store.Watch) *rules.Engine {
	return &rules.Engine{
		Store: s, Now: time.Now,
		Render: func(c store.ChangeRow) string {
			return telegram.Render(c, subjectFor(named, w, c))
		},
		Summarise: func(r rules.Rule, firings []rules.Firing) string {
			lines := make([]telegram.Line, 0, len(firings))
			for _, f := range firings {
				lines = append(lines,
					telegram.RenderLine(f.Event.Change, subjectFor(named, w, f.Event.Change)))
			}
			return telegram.Summarise(r.Name, lines)
		},
		// The inline button, on the one kind of message where a person can
		// correct us from where they read it. The predecessor travels in the
		// change's Was column, which is where the window producer put it.
		Attach: func(c store.ChangeRow) string {
			if c.Kind != track.KindListingReposted || c.Was == "" {
				return ""
			}
			predecessor, err := strconv.ParseInt(c.Was, 10, 64)
			if err != nil || predecessor <= 0 {
				return ""
			}
			return telegram.Attachment{
				ButtonText: "Это не перепубликация",
				Callback:   telegram.NotARepost(c.ItemID, predecessor),
			}.Encode()
		},
		Suppressor: rules.Suppressor{
			Now: time.Now,
			LastFired: func(ruleID int64, p rules.SubjectPair) (time.Time, bool) {
				at, ok, err := s.LastFired(context.Background(), ruleID,
					string(p.Kind), p.Key)
				if err != nil || !ok {
					return time.Time{}, false
				}
				return time.Unix(at, 0).UTC(), true
			},
			SeenKey: func(ruleID int64, key string) bool {
				seen, err := s.SeenDedupKey(context.Background(), ruleID, key)
				return err == nil && seen
			},
		},
	}
}

func subjectFor(named map[int64]store.ListingName, w store.Watch, c store.ChangeRow) telegram.Subject {
	n := named[c.ItemID]
	return telegram.Subject{
		WatchName: w.Name, Title: n.Title, URLPath: n.URLPath,
	}
}

// replayAppearance re-announces a listing whose new-listing message a repost
// link withheld.
//
// The spec calls it «the suppressed alert is replayed, marked as a corrected
// suppression». The mark is the change's own producer: 'user' says a person
// caused this rather than a pass, which is what distinguishes it in the log
// from an ordinary appearance arriving hours late for no stated reason.
func replayAppearance(ctx context.Context, s *store.Store, itemID int64) (int, error) {
	watchID, err := s.FirstWatchOf(ctx, itemID)
	if err != nil {
		return 0, err
	}
	w, err := s.Watch(ctx, watchID)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	if err := s.RecordUserChange(ctx, store.ChangeRow{
		Kind: track.KindListingAppeared, ItemID: itemID, WatchID: watchID,
		TS: now, HasNow: true, Confidence: 1.0, Producer: "user",
	}); err != nil {
		return 0, err
	}
	return applyRules(ctx, s, w, now)
}
