// SPDX-License-Identifier: AGPL-3.0-or-later

package collect

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// recordingPasser reads nothing and remembers who it was asked to read.
type recordingPasser struct {
	seen []int64
	fail map[int64]error
}

func (p *recordingPasser) Pass(_ context.Context, w store.Watch) error {
	p.seen = append(p.seen, w.ID)
	return p.fail[w.ID]
}

func openTemp(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func addWatch(t *testing.T, s *store.Store, name string, dueAt int64) int64 {
	t.Helper()
	id, err := s.AddWatch(context.Background(), store.Watch{
		Name: name, Kind: "search", URL: "https://m.avito.ru/all?q=" + name,
		Surface: avito.SurfaceMobile, MaxPages: 1, IntervalSec: 300,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		NextDueAt: dueAt, Enabled: true, CreatedAt: 1000, UpdatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func at(unix int64) func() time.Time {
	return func() time.Time { return time.Unix(unix, 0).UTC() }
}

// TestOnlyWhatIsDueIsRead.
//
// The scheduler asks DueWatches and nothing else — enabled, idle, and due. A
// scheduler with its own opinion about which watches to take would be a second
// place the answer lives, and the two would disagree the first time somebody
// disabled a watch.
func TestOnlyWhatIsDueIsRead(t *testing.T) {
	s := openTemp(t)
	due := addWatch(t, s, "готово", 1000)
	addWatch(t, s, "ещё-рано", 9999)

	p := &recordingPasser{}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 1 || p.seen[0] != due {
		t.Errorf("read %v, want only %d", p.seen, due)
	}
}

// TestNothingDueIsNotAnError.
//
// Treated as one it would drive the supervisor's backoff to five minutes on a
// quiet installation and keep it there — so a watch coming due would then wait
// up to five minutes past its interval, for no reason a person could see.
func TestNothingDueIsNotAnError(t *testing.T) {
	s := openTemp(t)
	addWatch(t, s, "ещё-рано", 9999)

	p := &recordingPasser{}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(context.Background()); err != nil {
		t.Errorf("an empty turn returned %v", err)
	}
	if len(p.seen) != 0 {
		t.Errorf("read %v on an empty turn", p.seen)
	}
}

// TestOneWatchFailingDoesNotStopTheOthers.
//
// A watch can halt on a wall of its own, and the remaining watches may be on a
// different address entirely. Collection that stopped because one watch hit one
// is collection that never recovers.
func TestOneWatchFailingDoesNotStopTheOthers(t *testing.T) {
	s := openTemp(t)
	first := addWatch(t, s, "первое", 1000)
	second := addWatch(t, s, "второе", 1000)
	third := addWatch(t, s, "третье", 1000)

	p := &recordingPasser{fail: map[int64]error{
		second: errors.New("стена"),
	}}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(context.Background()); err != nil {
		t.Errorf("one failure failed the whole turn: %v", err)
	}
	if len(p.seen) != 3 {
		t.Fatalf("read %v, want all three", p.seen)
	}
	if p.seen[0] != first || p.seen[2] != third {
		t.Errorf("read %v", p.seen)
	}
}

// TestATurnWhereNothingWorkedIsAFailure.
//
// So the supervisor's backoff means «this installation is not collecting»
// rather than «one of ten watches is blocked». The distinction is what decides
// whether the next turn happens in fifteen seconds or in five minutes.
func TestATurnWhereNothingWorkedIsAFailure(t *testing.T) {
	s := openTemp(t)
	first := addWatch(t, s, "первое", 1000)
	second := addWatch(t, s, "второе", 1000)

	p := &recordingPasser{fail: map[int64]error{
		first: errors.New("стена"), second: errors.New("стена"),
	}}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(context.Background()); err == nil {
		t.Error("a turn where every watch failed reported success")
	}
}

// TestTheBatchIsBounded.
//
// The pacer decides the rate, so a bigger batch collects no faster — it only
// makes one turn longer, and a turn that runs for an hour is a scheduler that
// cannot be stopped promptly.
func TestTheBatchIsBounded(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 25; i++ {
		addWatch(t, s, "наблюдение", 1000)
	}
	p := &recordingPasser{}
	sch := &Scheduler{Store: s, Passer: p, Batch: 4, Now: at(2000)}
	if err := sch.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 4 {
		t.Errorf("read %d watches with a batch of 4", len(p.seen))
	}
}

// TestACancelledContextStopsMidTurn.
//
// A turn over ten watches is minutes long. Stopping the process must not mean
// waiting for it to finish reading watches nobody is going to look at.
func TestACancelledContextStopsMidTurn(t *testing.T) {
	s := openTemp(t)
	for i := 0; i < 5; i++ {
		addWatch(t, s, "наблюдение", 1000)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &cancellingPasser{cancel: cancel, after: 2}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}

	if err := sch.Turn(ctx); err == nil {
		t.Error("a cancelled turn reported success")
	}
	if p.seen > 3 {
		t.Errorf("%d watches were read after cancellation", p.seen)
	}
}

type cancellingPasser struct {
	cancel context.CancelFunc
	after  int
	seen   int
}

func (p *cancellingPasser) Pass(context.Context, store.Watch) error {
	p.seen++
	if p.seen == p.after {
		p.cancel()
	}
	return nil
}

// TestADisabledWatchIsNeverRead, even when its due time has passed.
func TestADisabledWatchIsNeverRead(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addWatch(t, s, "выключено", 1000)
	if _, err := s.DB().Exec(`UPDATE watches SET enabled = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	p := &recordingPasser{}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 0 {
		t.Errorf("a disabled watch was read: %v", p.seen)
	}
}

// TestAHaltedWatchIsNeverRead.
//
// A halt is a wall the site put up, and clearing it is a person's act. A
// scheduler that read halted watches would spend the whole budget rediscovering
// the same wall every fifteen seconds.
func TestAHaltedWatchIsNeverRead(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addWatch(t, s, "остановлено", 1000)
	if err := s.MarkHalted(ctx, id, "geetest", 1500); err != nil {
		t.Fatal(err)
	}
	p := &recordingPasser{}
	sch := &Scheduler{Store: s, Passer: p, Now: at(2000)}
	if err := sch.Turn(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 0 {
		t.Errorf("a halted watch was read: %v", p.seen)
	}
}
