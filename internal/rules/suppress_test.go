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

// TestARepostIsNotANewFind.
//
// Until increment 7 links reposts as they happen, a seller reposting the same
// phone produces a listing-appeared under a new number. Announced, it is the
// same find twice — and the person cannot tell, because everything about it is
// genuinely new.
func TestARepostIsNotANewFind(t *testing.T) {
	r := priceRule()
	r.Kind = track.KindListingAppeared

	ev := priceEvent()
	ev.Change.Kind = track.KindListingAppeared

	if d := passthrough().Decide(r, ev); !d.Send {
		t.Fatalf("a genuine arrival was suppressed: %s", d.Reason)
	}

	was := int64(7800000001)
	ev.RepostOf = &was
	d := passthrough().Decide(r, ev)
	if d.Send {
		t.Error("a repost was announced as a new listing")
	}
	if d.Reason != ReasonRepost {
		t.Errorf("Reason = %q, want %q", d.Reason, ReasonRepost)
	}
}

// TestAPriceMoveOnARepostIsStillNews. The suppression is about appearances and
// nothing else: a price fall is news about the market, not about which
// incarnation happened to carry it.
func TestAPriceMoveOnARepostIsStillNews(t *testing.T) {
	ev := priceEvent()
	was := int64(7800000001)
	ev.RepostOf = &was
	if d := passthrough().Decide(priceRule(), ev); !d.Send {
		t.Errorf("a price move was suppressed as a repost: %s", d.Reason)
	}
}

// TestQuietFromNineToNineIsNoWindowAtAll. "Тихо с 9 до 9" as a synonym for
// "silence forever" is a foot-gun in a settings screen; somebody who wants
// silence disables the rule.
func TestQuietFromNineToNineIsNoWindowAtAll(t *testing.T) {
	q := QuietHours{FromMin: 9 * 60, ToMin: 9 * 60}
	for _, h := range []int{0, 9, 15, 23} {
		if q.Active(time.Date(2026, 8, 24, h, 0, 0, 0, time.UTC)) {
			t.Errorf("Active(%02d:00) = true for an empty window", h)
		}
	}
}

// TestQuietHoursAreSomebodysEvening. A nil Location is UTC, which is nobody's
// evening — a caller that leaves it nil in production gives its user quiet
// hours three time zones from where they asked for them.
func TestQuietHoursAreSomebodysEvening(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	q := QuietHours{FromMin: 23 * 60, ToMin: 7 * 60, Location: msk}
	// 21:30 UTC is 00:30 in Moscow: quiet there, not quiet in UTC.
	at := time.Date(2026, 8, 24, 21, 30, 0, 0, time.UTC)
	if !q.Active(at) {
		t.Error("00:30 Moscow time was not inside 23:00–07:00 Moscow time")
	}
	q.Location = nil
	if q.Active(at) {
		t.Error("the window was read in UTC but the test expected it not to be")
	}
}

// TestQuietHoursAreMinutesAndNotHours. 22:30 is a thing a person asks for, and
// 22 is not the same request.
func TestQuietHoursAreMinutesAndNotHours(t *testing.T) {
	q := QuietHours{FromMin: 22*60 + 30, ToMin: 7 * 60}
	if q.Active(time.Date(2026, 8, 24, 22, 15, 0, 0, time.UTC)) {
		t.Error("22:15 fell inside a window that starts at 22:30")
	}
	if !q.Active(time.Date(2026, 8, 24, 22, 45, 0, 0, time.UTC)) {
		t.Error("22:45 fell outside a window that starts at 22:30")
	}
}

// TestAThresholdInKopecksDoesNotFilterOutWhatIsNotMoney. A threshold in minor
// units against a bump time would compare two different things and quietly
// silence every rule whose kind is not about money.
func TestAThresholdInKopecksDoesNotFilterOutWhatIsNotMoney(t *testing.T) {
	ev := priceEvent()
	ev.Change.Kind, ev.Change.Field, ev.Change.Unit =
		track.KindListingBumped, "sort_ts_ms", track.UnitSeconds
	ev.Change.Was, ev.Change.Now = "1700000000", "1700000600"

	r := priceRule()
	r.Kind, r.ThresholdMinor = track.KindListingBumped, 100_000_000

	if d := passthrough().Decide(r, ev); !d.Send {
		t.Errorf("a bump was silenced by a threshold in kopecks: %s", d.Reason)
	}
}

// TestAnAppearanceClearsAPercentageFloor. An appearance is not a small move; it
// is a move whose size has no percentage, and failing it there would silently
// drop every arrival for every rule that set one.
func TestAnAppearanceClearsAPercentageFloor(t *testing.T) {
	ev := priceEvent()
	ev.Change.Kind = track.KindPriceAppeared
	ev.Change.Was, ev.Change.HadBefore = "", false

	r := priceRule()
	r.Kind, r.ThresholdPct = track.KindPriceAppeared, 20

	if d := passthrough().Decide(r, ev); !d.Send {
		t.Errorf("an appearance was refused by a percentage floor: %s", d.Reason)
	}
}

// TestTheRateLimitCountsFromWhenItLastSpoke.
func TestTheRateLimitCountsFromWhenItLastSpoke(t *testing.T) {
	r := priceRule()
	r.MinInterval = time.Hour

	s := passthrough()
	s.LastFired = func(int64, SubjectPair) (time.Time, bool) {
		return time.Unix(10_000-600, 0).UTC(), true // ten minutes ago
	}
	if d := s.Decide(r, priceEvent()); d.Send || d.Reason != ReasonRateLimit {
		t.Errorf("Send=%v Reason=%q ten minutes into an hourly limit", d.Send, d.Reason)
	}

	s.LastFired = func(int64, SubjectPair) (time.Time, bool) {
		return time.Unix(10_000-7200, 0).UTC(), true // two hours ago
	}
	if d := s.Decide(r, priceEvent()); !d.Send {
		t.Errorf("two hours into an hourly limit: %s", d.Reason)
	}
}
