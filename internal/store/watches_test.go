// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func aWatch() Watch {
	return Watch{
		Name: "iphone", Kind: "search",
		URL:     "https://m.avito.ru/all?q=iphone+15&s=104",
		Surface: avito.SurfaceMobile, MaxPages: 1, IntervalSec: 300,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		Enabled: true, CreatedAt: 1000, UpdatedAt: 1000,
	}
}

func TestAddWatchRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	id, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	got, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if got.URL != aWatch().URL || got.Surface != avito.SurfaceMobile ||
		got.IntervalSec != 300 || !got.Enabled {
		t.Errorf("Watch = %+v; the round trip lost something", got)
	}
	if got.State != "idle" {
		t.Errorf("State = %q, want idle", got.State)
	}
}

// TestMaxPagesIsRefusedAboveTwo. Deep pagination is what escalates a 429 into
// a 403, and a 403 costs the cookie jar and therefore the clearance it was
// carrying. The spec refuses it in three independent places; this is the one
// the database owns, and it is the one that cannot be argued around.
func TestMaxPagesIsRefusedAboveTwo(t *testing.T) {
	s := openTemp(t)
	w := aWatch()
	w.MaxPages = 3
	if _, err := s.AddWatch(context.Background(), w); err == nil {
		t.Error("max_pages 3 was accepted")
	}
}

// TestIntervalIsRefusedBelowTwoMinutes.
func TestIntervalIsRefusedBelowTwoMinutes(t *testing.T) {
	s := openTemp(t)
	w := aWatch()
	w.IntervalSec = 60
	if _, err := s.AddWatch(context.Background(), w); err == nil {
		t.Error("a 60-second interval was accepted; the floor is 120")
	}
}

// TestDueWatchesAsksTheSchedulersOnlyQuestion.
func TestDueWatchesAsksTheSchedulersOnlyQuestion(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	due := aWatch()
	due.Name, due.NextDueAt = "due", 500
	dueID, err := s.AddWatch(ctx, due)
	if err != nil {
		t.Fatal(err)
	}

	later := aWatch()
	later.Name, later.NextDueAt = "later", 5000
	if _, err := s.AddWatch(ctx, later); err != nil {
		t.Fatal(err)
	}

	off := aWatch()
	off.Name, off.NextDueAt, off.Enabled = "disabled", 500, false
	if _, err := s.AddWatch(ctx, off); err != nil {
		t.Fatal(err)
	}

	got, err := s.DueWatches(ctx, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != dueID {
		names := make([]string, len(got))
		for i, w := range got {
			names[i] = w.Name
		}
		t.Errorf("DueWatches = %v, want only the enabled watch whose next_due_at "+
			"has passed", names)
	}
}

// TestAHaltedWatchIsNeverDue, and the reason is never cleared by the program.
// A monitor that decides on its own that the wall it hit has gone away is a
// monitor that resumes into it all night.
func TestAHaltedWatchIsNeverDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	w := aWatch()
	w.NextDueAt = 500
	id, err := s.AddWatch(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHalted(ctx, id, "captcha marker geetest", 900); err != nil {
		t.Fatal(err)
	}
	got, err := s.DueWatches(ctx, 1000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a halted watch came back due: %+v", got)
	}
	after, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.HaltedReason != "captcha marker geetest" || after.State != "halted" {
		t.Errorf("state=%q reason=%q; both must survive for a person to read",
			after.State, after.HaltedReason)
	}
}

// TestSetNextDueMovesTheWatchForward.
func TestSetNextDueMovesTheWatchForward(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	w := aWatch()
	w.NextDueAt = 500
	id, err := s.AddWatch(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNextDue(ctx, id, 2000); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DueWatches(ctx, 1000, 10); len(got) != 0 {
		t.Error("the watch is still due after being moved past now")
	}
	if got, _ := s.DueWatches(ctx, 2500, 10); len(got) != 1 {
		t.Error("the watch never came due again")
	}
}

// TestResumingClearsTheHaltAndPromisesNothing.
//
// A person's act and never the program's: a monitor that decides on its own
// that a wall has gone away is a monitor that resumes into it all night. This
// re-tests nothing — if the wall is still there the next pass halts again with
// a fresh reason, which costs one request and buys a truthful answer.
func TestResumingClearsTheHaltAndPromisesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	if err := s.MarkHalted(ctx, id, "geetest", 1500); err != nil {
		t.Fatal(err)
	}

	ok, err := s.ResumeWatch(ctx, id, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a halted watch reported nothing to resume")
	}
	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "idle" || w.HaltedReason != "" {
		t.Errorf("watch = %q / %q", w.State, w.HaltedReason)
	}
	if w.NextDueAt != 2000 {
		t.Errorf("next_due_at = %d, want now", w.NextDueAt)
	}
}

// TestAResumptionIsRecordedButNeverSent.
//
// An un-halt that sent «your install is blocked» through the global rule would
// be a lie in the one message that has to be trustworthy — so the row is
// written with the reason it cleared and marked backfill, which is what makes
// the notifier skip it.
func TestAResumptionIsRecordedButNeverSent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)
	if err := s.MarkHalted(ctx, id, "доступ ограничен", 1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumeWatch(ctx, id, 2000); err != nil {
		t.Fatal(err)
	}
	changes, err := s.Changes(ctx, id, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Two: the halt said so, and the resumption says it was cleared. The halt
	// is meant to be sent and the resumption is not.
	if len(changes) != 2 {
		t.Fatalf("%d changes", len(changes))
	}
	var halt, resumed ChangeRow
	for _, c := range changes {
		if c.Backfill {
			resumed = c
		} else {
			halt = c
		}
	}
	if halt.Now != "доступ ограничен" {
		t.Errorf("the halt did not carry its reason: %q", halt.Now)
	}
	if halt.Backfill {
		t.Error("the halt was marked backfill; nobody would be told")
	}
	if resumed.Was != "доступ ограничен" {
		t.Errorf("the cleared reason was lost: %q", resumed.Was)
	}
	if !resumed.Backfill {
		t.Error("the resumption was not marked backfill; the notifier would send it")
	}
}

// TestResumingAWatchThatIsNotHaltedIsNotAnError.
//
// «Возобновить всё» over a list where one has already recovered must not fail
// on it.
func TestResumingAWatchThatIsNotHaltedIsNotAnError(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	ok, err := s.ResumeWatch(ctx, id, 2000)
	if err != nil {
		t.Fatalf("resuming a healthy watch failed: %v", err)
	}
	if ok {
		t.Error("a healthy watch reported that it was resumed")
	}
	if n := countRows(t, s, `SELECT count(*) FROM changes`); n != 0 {
		t.Errorf("%d changes written for a watch that was not halted", n)
	}
}

// TestHittingTheSameWallTwiceSaysSoOnce.
//
// A wall is hit on every pass. Writing a change per pass would leave the rule's
// six-hour floor suppressing a flood instead of describing a state — and the
// suppression log would then be the flood.
func TestHittingTheSameWallTwiceSaysSoOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	for _, at := range []int64{1500, 1800, 2100} {
		if err := s.MarkHalted(ctx, id, "geetest", at); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := s.Changes(ctx, id, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Errorf("%d changes for one wall hit three times", len(changes))
	}
}
