// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// pass writes one pass row directly, because these tests are about reading and
// SavePass would drag a whole extraction in with it.
func pass(t *testing.T, s *Store, watchID, at int64, class, path string, evidential bool, itemsSeen int) {
	t.Helper()
	_, err := s.DB().Exec(`
		INSERT INTO passes
		  (watch_id, started_at, finished_at, page, surface, class, path,
		   mode, evidential, items_seen)
		VALUES (?, ?, ?, 1, 'mobile', ?, ?, 'direct', ?, ?)`,
		watchID, at, at, class, path, evidential, itemsSeen)
	if err != nil {
		t.Fatal(err)
	}
}

// TestPathSharesAreTheEarliestWarningThereIs.
//
// Spec §Being seen as a bot: on the desktop surface a silent switch from state
// to dom is the earliest available sign that this install has been flagged —
// earlier than a 429 and much earlier than a captcha.
func TestPathSharesAreTheEarliestWarningThereIs(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for i := 0; i < 7; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "state", true, 30)
	}
	for i := 0; i < 3; i++ {
		pass(t, s, id, 20_000+int64(i), "ok", "dom", true, 30)
	}
	got, err := s.PathShares(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for _, p := range got {
		byPath[p.Path] = p.Count
	}
	if byPath["state"] != 7 || byPath["dom"] != 3 {
		t.Errorf("shares = %+v, want state 7 and dom 3", byPath)
	}
}

// TestTheWindowIsRespected, because «the share over the last day» is a different
// number from «the share ever», and the second hides a switch that happened this
// morning behind a month of health.
func TestTheWindowIsRespected(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	pass(t, s, id, 1_000, "ok", "state", true, 30) // old
	pass(t, s, id, 90_000, "ok", "dom", true, 30)  // inside

	got, err := s.PathShares(context.Background(), 50_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "dom" {
		t.Errorf("shares = %+v, want only the recent one", got)
	}
}

// TestTheBlockPictureIsByClassAndHour.
func TestTheBlockPictureIsByClassAndHour(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	// Two in one hour, one in the next.
	pass(t, s, id, 3600, "throttled", "", false, 0)
	pass(t, s, id, 3601, "throttled", "", false, 0)
	pass(t, s, id, 7200, "burned", "", false, 0)

	got, err := s.ClassByHour(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want two buckets", got)
	}
	if got[0].Hour != 3600 || got[0].Class != "throttled" || got[0].Count != 2 {
		t.Errorf("first bucket = %+v", got[0])
	}
	if got[1].Hour != 7200 || got[1].Class != "burned" || got[1].Count != 1 {
		t.Errorf("second bucket = %+v", got[1])
	}
}

// TestTheAgeOfTheLastEvidentialPassIsSeparateFromTheAgeOfTheLastPass.
//
// The spec calls this the one line that separates a quiet market from a dead
// collector. A watch polling every five minutes and answering soft-block every
// time has a recent last pass and an ancient last evidential one, and only the
// second says anything is wrong.
func TestTheAgeOfTheLastEvidentialPassIsSeparateFromTheAgeOfTheLastPass(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	pass(t, s, id, 1_000, "ok", "state", true, 30)
	pass(t, s, id, 90_000, "soft-block", "", false, 0)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	h := got[0]
	if h.LastPassAt != 90_000 {
		t.Errorf("LastPassAt = %d, want 90000", h.LastPassAt)
	}
	if h.LastEvidentialAt != 1_000 {
		t.Errorf("LastEvidentialAt = %d, want 1000", h.LastEvidentialAt)
	}
	if h.Passes != 2 || h.Evidential != 1 {
		t.Errorf("passes %d of which evidential %d, want 2 and 1", h.Passes, h.Evidential)
	}
}

// TestAWatchThatHasNeverBeenReadSaysSoRatherThanReportingNineteenSeventy.
func TestAWatchThatHasNeverBeenReadSaysSoRatherThanReportingNineteenSeventy(t *testing.T) {
	s := openTemp(t)
	addTestWatch(t, s, "новое", 300)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("a watch with no passes is missing from the health list: %+v", got)
	}
	if got[0].LastEvidentialAt != 0 || got[0].LastPassAt != 0 {
		t.Errorf("%+v; zero means «never», and the screen renders that as a word", got[0])
	}
}

// TestCardYieldIsNotArmedUntilThereAreTenPasses.
//
// Spec §Being seen as a bot: it needs ten ok passes before it means anything, so
// until a watch has them the alarm is not armed. A ratio computed from three
// passes is a number that means nothing dressed as one that means something.
func TestCardYieldIsNotArmedUntilThereAreTenPasses(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for i := 0; i < 9; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	got, err := s.WatchHealth(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CardYieldOK {
		t.Errorf("the alarm is armed on nine passes: %+v", got[0])
	}

	pass(t, s, id, 20_000, "ok", "dom", true, 30)
	got, err = s.WatchHealth(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].CardYieldOK {
		t.Error("ten passes did not arm the alarm")
	}
	if got[0].CardYield < 0.99 || got[0].CardYield > 1.01 {
		t.Errorf("CardYield = %v on a steady watch, want about 1", got[0].CardYield)
	}
}

// TestCardYieldFallsWhenThePageStartsCarryingFewerCards.
//
// Spec: a fall below half on a 200 with no wall marker means the page is being
// served to us differently. This checks the ratio moves; the screen decides what
// to say about it.
func TestCardYieldFallsWhenThePageStartsCarryingFewerCards(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	pass(t, s, id, 20_000, "ok", "dom", true, 9)

	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].CardYieldOK {
		t.Fatal("the alarm is not armed")
	}
	if got[0].CardYield > 0.5 {
		t.Errorf("CardYield = %v after the page halved, want under 0.5", got[0].CardYield)
	}
}

// TestOnlyOkPassesCountTowardsCardYield. A soft block returns no cards at all,
// and counting its zero would drag the median to nothing and disarm the one
// alarm that would have caught it.
func TestOnlyOkPassesCountTowardsCardYield(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 30)
	}
	for i := 0; i < 5; i++ {
		pass(t, s, id, 20_000+int64(i), "soft-block", "", false, 0)
	}
	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CardYield < 0.99 || got[0].CardYield > 1.01 {
		t.Errorf("CardYield = %v; the soft blocks were counted", got[0].CardYield)
	}
}

// TestATotallyEmptyPageIsNotARatio.
//
// Ten ok passes that each returned nothing is a real state — a query whose
// results all vanished — and it is not a division. The median is zero and the
// alarm stays unarmed rather than reporting +Inf or NaN.
func TestATotallyEmptyPageIsNotARatio(t *testing.T) {
	s := openTemp(t)
	id := addTestWatch(t, s, "наблюдение", 300)
	for i := 0; i < 10; i++ {
		pass(t, s, id, 10_000+int64(i), "ok", "dom", true, 0)
	}
	got, err := s.WatchHealth(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CardYieldOK {
		t.Errorf("CardYield = %v on a page that has been empty ten times running",
			got[0].CardYield)
	}
}
