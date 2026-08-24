// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// TestReclaimReachesFailedPassesLast.
//
// The obvious emergency reclaim — an age cut over passes — is wrong twice over.
// It deletes precisely the failed passes retention keeps for ever, and it
// deletes them at the worst possible moment: a disk filling is CORRELATED with
// collection being broken, because a blocked install is what writes both the
// failure rows and the archived bodies. So the naive sweep destroys the health
// history exactly when it is the only thing that explains what happened.
func TestReclaimReachesFailedPassesLast(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	r := DefaultRetention()
	r.BodyMaxBytes = 1 << 20
	s.SetRetention(r)

	insertBody(t, s, "b1", testNow-40*day, 8<<20)
	for i := 0; i < 40; i++ {
		p := aPass(wid, testNow-60*day+int64(i)*300)
		p.Class = "ok"
		insertBarePass(t, s, p)
	}
	for i := 0; i < 5; i++ {
		p := aPass(wid, testNow-60*day+int64(i)*300)
		p.Class, p.Evidential = "throttled", false
		insertBarePass(t, s, p)
	}

	// A target the body sweep alone can satisfy.
	rep, err := s.Reclaim(ctx, testNow, 1<<20)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if rep.ReachedFailedPasses {
		t.Error("Reclaim reported reaching failed passes on a target the body " +
			"sweep alone could satisfy; they are what it reaches last")
	}
	if len(rep.Steps) == 0 || rep.Steps[0] != "bodies" {
		t.Errorf("Steps = %v, want bodies first", rep.Steps)
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes WHERE class = 'throttled'`); n != 5 {
		t.Errorf("failed passes = %d, want all 5 kept", n)
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes WHERE class = 'ok'`); n != 40 {
		t.Errorf("healthy passes = %d; the first step reached the target, so the "+
			"second must not have run", n)
	}
}

// TestReclaimStopsAtTheFirstStepThatReachesTheTarget.
func TestReclaimStopsAtTheFirstStepThatReachesTheTarget(t *testing.T) {
	s := openTemp(t)
	r := DefaultRetention()
	r.BodyMaxBytes = 1 << 10
	s.SetRetention(r)
	insertBody(t, s, "big", testNow-40*day, 8<<20)

	rep, err := s.Reclaim(context.Background(), testNow, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Steps) != 1 {
		t.Errorf("Steps = %v, want exactly one — the target was reached by the first",
			rep.Steps)
	}
	if rep.Freed < 1<<20 {
		t.Errorf("Freed = %d, want at least the target", rep.Freed)
	}
}

// TestReclaimSaysSoWhenItCouldNotFreeEnough.
//
// It stops short of deleting failed passes and reports that it had to. That
// deletion is a person's decision, and the flag is what puts the question in
// front of them instead of quietly answering it.
func TestReclaimSaysSoWhenItCouldNotFreeEnough(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, _ := s.AddWatch(ctx, aWatch())
	for i := 0; i < 5; i++ {
		p := aPass(wid, testNow-60*day+int64(i)*300)
		p.Class = "throttled"
		insertBarePass(t, s, p)
	}

	// Nothing thinnable exists, so no step can free a gigabyte.
	rep, err := s.Reclaim(ctx, testNow, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ReachedFailedPasses {
		t.Error("Reclaim ran out of steps without saying so; the panel has " +
			"nothing to show and the person is never asked")
	}
	if n := countRows(t, s, `SELECT count(*) FROM passes`); n != 5 {
		t.Errorf("passes = %d; Reclaim deleted failed passes on its own", n)
	}
	if len(rep.Steps) != 3 {
		t.Errorf("Steps = %v, want all three to have run before giving up", rep.Steps)
	}
}
