// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/track"
)

// kindsFor is every change kind this watch produced, in id order.
func kindsFor(t *testing.T, s *Store, watchID int64) []track.Kind {
	t.Helper()
	rows, err := s.Changes(context.Background(), watchID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]track.Kind, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

func countKind(t *testing.T, s *Store, watchID int64, k track.Kind) int {
	t.Helper()
	n := 0
	for _, got := range kindsFor(t, s, watchID) {
		if got == k {
			n++
		}
	}
	return n
}

// TestTheSeedingPassWritesArrivalsMarkedBackfill.
//
// A watch's first pass has nothing behind it. Its members are recorded and its
// arrivals are real, but they are marked backfill so a rule written on
// listing-appeared does not fire thirty times the moment somebody adds a watch.
func TestTheSeedingPassWritesArrivalsMarkedBackfill(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("the seeding pass produced %d changes, want 3 arrivals: %v",
			len(rows), kindsFor(t, s, wid))
	}
	for _, r := range rows {
		if r.Kind != track.KindListingAppeared {
			t.Errorf("kind = %q, want listing-appeared", r.Kind)
		}
		if !r.Backfill {
			t.Error("a seeding arrival is not marked backfill; a rule on " +
				"listing-appeared would fire thirty times the moment somebody " +
				"adds a watch")
		}
		if r.Producer != "window" {
			t.Errorf("producer = %q, want window", r.Producer)
		}
	}
}

// TestASeedingPassConfirmsNothingGone.
func TestASeedingPassConfirmsNothingGone(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("the seeding pass produced %d vanished rows", n)
	}
}

// TestAListingPushedOffTheBottomProducesNoVanished.
//
// In a date-sorted window of k slots an item at rank r is pushed out by k − r
// arrivals. This is the case that separates "gone" from "we only look at the
// first page", and it must not produce a removal alert.
func TestAListingPushedOffTheBottomProducesNoVanished(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// A window of three; item 3 sits at the bottom with zero headroom.
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	// One arrival, and item 3 is gone: exactly what the arithmetic predicts.
	for at := int64(1300); at <= 2600; at += int64(aWatch().IntervalSec) {
		if _, err := s.SavePass(ctx, aPass(wid, at), listingsFor(9, 1, 2)); err != nil {
			t.Fatal(err)
		}
	}

	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("a displaced listing produced %d vanished rows", n)
	}
	if n := countKind(t, s, wid, track.KindListingLeftWindow); n != 1 {
		t.Errorf("left-window rows = %d, want 1 — it did leave the window, and "+
			"that is a different sentence from having been removed", n)
	}
	// The member row records why, so the panel can say so.
	var displaced int
	if err := s.DB().QueryRow(
		`SELECT displaced FROM watch_members WHERE watch_id = ? AND item_id = 3`,
		wid).Scan(&displaced); err != nil {
		t.Fatal(err)
	}
	if displaced != 1 {
		t.Error("the member was not marked displaced")
	}
}

// TestAListingGoneFromTwoCleanPassesTwentyMinutesApartIsVanishedAtConfidence03.
//
// 0.3 and not 1.0, and the axis is structural: a page one is a window and not a
// census, so a conclusion drawn from something not being in it is weaker than a
// fact an item page stated.
func TestAListingGoneFromTwoCleanPassesTwentyMinutesApartIsVanishedAtConfidence03(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	// Rank 1 of three, so displacement can never explain its absence.
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(2, 3)); err != nil {
		t.Fatal(err)
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Fatal("one confirmation was enough; both gates are required")
	}
	// Then poll at the watch's own cadence until the span gate opens. The
	// cadence is not decoration: passes further apart than three intervals are
	// a gap in collection, which Admissible correctly refuses as a re-seed, so
	// a test that jumped straight to the boundary would be measuring the gap
	// gate rather than the span one.
	for at := int64(1600); at <= 1300+1201; at += int64(aWatch().IntervalSec) {
		if _, err := s.SavePass(ctx, aPass(wid, at), listingsFor(2, 3)); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.Changes(ctx, wid, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *ChangeRow
	for i := range rows {
		if rows[i].Kind == track.KindListingVanished {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("no vanished row after two confirmations twenty minutes apart; "+
			"kinds were %v", kindsFor(t, s, wid))
	}
	if found.ItemID != 1 {
		t.Errorf("vanished item = %d, want 1", found.ItemID)
	}
	if found.Confidence != 0.3 {
		t.Errorf("confidence = %v, want 0.3 — a page one is a window and not a "+
			"census", found.Confidence)
	}
	// And the listing's own state moved, from a search-shaped source only.
	var state, source string
	if err := s.DB().QueryRow(
		`SELECT state, state_source FROM listings WHERE item_id = 1`).
		Scan(&state, &source); err != nil {
		t.Fatal(err)
	}
	if state != "vanished" || source != "search" {
		t.Errorf("state=%q source=%q; a window conclusion may write vanished and "+
			"never closed", state, source)
	}
}

// TestAVanishedListingIsAnnouncedOnce. The gates keep passing on every later
// pass, and a producer that re-emitted would send the same alert every tick
// until the person turned notifications off.
func TestAVanishedListingIsAnnouncedOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	// Polled at the watch's cadence, well past the span gate: the gates keep
	// passing on every one of these.
	for at := int64(1300); at <= 4000; at += int64(aWatch().IntervalSec) {
		if _, err := s.SavePass(ctx, aPass(wid, at), listingsFor(2, 3)); err != nil {
			t.Fatal(err)
		}
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 1 {
		t.Errorf("vanished rows = %d, want exactly 1 — the gates keep passing, "+
			"and re-emitting would send the same alert every tick", n)
	}
}

// TestTwoConsecutiveDroppedStatePassesProduceNoVanishedRowsAtAll.
//
// This is the failure the whole target is shaped around. A soft block is HTTP
// 200 with no container, and differenced as a real reading it fires a removal
// alert for every listing in the watch.
func TestTwoConsecutiveDroppedStatePassesProduceNoVanishedRowsAtAll(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	for at := int64(1600); at <= 2900; at += int64(aWatch().IntervalSec) {
		p := aPass(wid, at)
		p.Class, p.Path = "soft-block", ""
		if _, err := s.SavePass(ctx, p, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("two soft blocks produced %d vanished rows; that is a removal "+
			"alert for every listing in the watch, and it is the failure this "+
			"whole design exists to prevent", n)
	}
}

// TestAListingThatFloatedBackIsReturnedAndNotAppeared.
//
// listing-appeared requires membership now, absence before, AND
// listings.first_seen_at equal to this pass. Without that third clause a
// recovered soft block announces thirty new listings that have been there all
// week — the cheapest correctness guard in the design.
func TestAListingThatFloatedBackIsReturnedAndNotAppeared(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1300), listingsFor(2, 3)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1600), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}

	if n := countKind(t, s, wid, track.KindListingReturned); n != 1 {
		t.Errorf("returned rows = %d, want 1: %v", n, kindsFor(t, s, wid))
	}
	if n := countKind(t, s, wid, track.KindListingAppeared); n != 3 {
		t.Errorf("appeared rows = %d, want the seed's 3 and no more — a listing "+
			"that floated back is not news, and first_seen_at is what says so", n)
	}
}

// TestAnIdentityChangeProducesArrivalsButNoDepartures.
//
// Measured 2026-08-24: in direct mode the result set is a function of srv_id —
// 100% overlap within an identity, 23% across. The ladder changes the identity
// by itself, on a 403 three waits did not clear and on a soft block. So the
// next pass is a perfectly good reading of a DIFFERENT slice, and differencing
// it against its predecessor would report most of the watch as gone.
func TestAnIdentityChangeProducesArrivalsButNoDepartures(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	first := aPass(wid, 1000)
	first.Session = "identity-A"
	if _, err := s.SavePass(ctx, first, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	second := aPass(wid, 1300)
	second.Session = "identity-A"
	if _, err := s.SavePass(ctx, second, listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}
	// A renewal happened, and the site showed this identity a different slice.
	for at := int64(1600); at <= 2900; at += int64(aWatch().IntervalSec) {
		p := aPass(wid, at)
		p.Session = "identity-B"
		if _, err := s.SavePass(ctx, p, listingsFor(7, 8, 9)); err != nil {
			t.Fatal(err)
		}
	}

	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("an identity change produced %d vanished rows; the two passes "+
			"are readings of different markets and no confirmation count can "+
			"filter that out", n)
	}
	appeared := countKind(t, s, wid, track.KindListingAppeared)
	if appeared < 6 {
		t.Errorf("appeared rows = %d, want the seed's 3 plus the new slice's 3; "+
			"incomparable does not mean unread", appeared)
	}
	// The new slice's arrivals are marked backfill: they are a different part
	// of the market rather than news from the part we were watching.
	rows, _ := s.Changes(ctx, wid, 1600, 100)
	for _, r := range rows {
		if r.Kind == track.KindListingAppeared && !r.Backfill {
			t.Errorf("item %d arrived unmarked across an identity change", r.ItemID)
		}
	}
}

// TestRetiringMembersAcrossAnIdentityChangeAnnouncesNothing.
//
// Suppressing departures for the single boundary pass is not enough, and that
// is the trap this test exists for. From the pass after it the sessions match
// again, the streaks resume climbing, and twenty minutes later three quarters
// of the watch is declared vanished on the word of an identity that was never
// shown them.
//
// The members are retired instead: absent, explained, and silent. Emitting
// nothing is the correct output for "no information".
func TestRetiringMembersAcrossAnIdentityChangeAnnouncesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())

	for _, at := range []int64{1000, 1300} {
		p := aPass(wid, at)
		p.Session = "identity-A"
		if _, err := s.SavePass(ctx, p, listingsFor(1, 2, 3)); err != nil {
			t.Fatal(err)
		}
	}
	// Long enough after the change that both gates would have opened.
	for at := int64(1600); at <= 4000; at += int64(aWatch().IntervalSec) {
		p := aPass(wid, at)
		p.Session = "identity-B"
		if _, err := s.SavePass(ctx, p, listingsFor(7, 8, 9)); err != nil {
			t.Fatal(err)
		}
	}

	if n := countKind(t, s, wid, track.KindListingVanished); n != 0 {
		t.Errorf("vanished rows = %d well past both gates; the new identity was "+
			"never shown those listings", n)
	}
	if n := countKind(t, s, wid, track.KindListingLeftWindow); n != 0 {
		t.Errorf("left-window rows = %d; we did not observe a departure, we lost "+
			"the ability to observe, and that is not a message", n)
	}
	// The member rows survive, because they are the evidence behind whatever
	// is said about those listings later.
	if n := countRows(t, s, `SELECT count(*) FROM watch_members WHERE watch_id = ?`, wid); n != 6 {
		t.Errorf("watch_members rows = %d, want 6 — three retired and three new", n)
	}
}
