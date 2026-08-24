// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/avito"
)

func addTestWatch(t *testing.T, s *Store, name string, intervalSec int) int64 {
	t.Helper()
	now := time.Now().Unix()
	id, err := s.AddWatch(context.Background(), Watch{
		Name: name, Kind: "search", URL: "https://m.avito.ru/all?q=" + name,
		Surface: avito.SurfaceMobile, MaxPages: 1, IntervalSec: intervalSec,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		NextDueAt: now, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestWatchesListsThemAllIncludingTheOnesNotDue.
//
// DueWatches answers the scheduler's question and is useless to the screen: a
// panel that showed only what is due would show an empty list for most of every
// interval, which reads as "my watches are gone".
func TestWatchesListsThemAllIncludingTheOnesNotDue(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	addTestWatch(t, s, "первое", 300)
	second := addTestWatch(t, s, "второе", 600)

	// Push the second far into the future, so nothing is due about it.
	if err := s.SetNextDue(ctx, second, time.Now().Unix()+100_000); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Watches returned %d, want both", len(got))
	}
	if got[0].Name != "первое" || got[1].Name != "второе" {
		t.Errorf("order = %q, %q; want by id so two loads list them the same way",
			got[0].Name, got[1].Name)
	}
}

// TestADisabledWatchIsStillListed. Turning one off is not deleting it, and a
// screen that hid it would leave no way to turn it back on.
func TestADisabledWatchIsStillListed(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "выключено", 300)

	w, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	w.Enabled = false
	if err := s.UpdateWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("got %d watches, first enabled=%v", len(got), got[0].Enabled)
	}
}

// TestUpdateWatchDoesNotResetWhatTheCollectorOwns.
//
// The form posts a name, a URL and an interval. seeded_at, state and
// halted_reason belong to the collector, and a save that wrote the form's zero
// values over them would un-seed a watch every time somebody renamed it — which
// would make its next pass a seeding pass and silence a day of alerts.
func TestUpdateWatchDoesNotResetWhatTheCollectorOwns(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "наблюдение", 300)

	if err := s.MarkHalted(ctx, id, "проблема с ip", 12_345); err != nil {
		t.Fatal(err)
	}
	before, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	edit := Watch{ID: id, Name: "переименовано", Kind: before.Kind,
		URL: before.URL, Surface: before.Surface, MaxPages: 1,
		IntervalSec: 600, AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		Enabled: true}
	if err := s.UpdateWatch(ctx, edit); err != nil {
		t.Fatal(err)
	}
	after, err := s.Watch(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "переименовано" || after.IntervalSec != 600 {
		t.Errorf("the edit did not land: %+v", after)
	}
	if after.State != before.State || after.HaltedReason != before.HaltedReason {
		t.Errorf("renaming a watch cleared its halt: state=%q reason=%q",
			after.State, after.HaltedReason)
	}
}

// TestPanelSettingsRoundTrip.
func TestPanelSettingsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	got, err := s.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != (PanelSettings{}) {
		t.Errorf("a fresh database holds %+v; the panel must start closed", got)
	}

	want := PanelSettings{PasswordHash: "pbkdf2$…", LAN: true, Autostart: true}
	if err := s.SetPanelSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err = s.PanelSettings(ctx); err != nil || got != want {
		t.Errorf("read back %+v (%v), want %+v", got, err, want)
	}
}

// TestRetentionSurvivesARestart.
//
// SetRetention holds it in an atomic pointer, which is right for the process
// and wrong for the product: a person who lowered their archive to a week found
// it back at a year on the next start, with no way to tell that had happened.
func TestRetentionSurvivesARestart(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	got, err := s.StoredRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultRetention() {
		t.Errorf("a fresh database does not hold the defaults: %+v", got)
	}

	want := DefaultRetention()
	want.BodyMaxAge = 7 * 24 * time.Hour
	want.BodyMaxBytes = 256 << 20
	want.SnapshotDailyAfter = 48 * time.Hour
	if err := s.SetStoredRetention(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, err = s.StoredRetention(ctx); err != nil || got != want {
		t.Errorf("read back %+v (%v), want %+v", got, err, want)
	}
}

// TestAnUnsetRetentionMeansTheDefaultsAndNeverUnbounded.
//
// A zero in one of these columns is what a hand-edited database or a restored
// backup leaves. Read as "no limit" it is an archive that grows without bound
// on exactly the install that is already broken.
func TestAnUnsetRetentionMeansTheDefaultsAndNeverUnbounded(t *testing.T) {
	s := openTemp(t)
	if _, err := s.DB().Exec(
		`UPDATE settings SET retention_body_max_bytes = 0, retention_anchor_sec = 0`); err != nil {
		t.Fatal(err)
	}
	got, err := s.StoredRetention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyMaxBytes != DefaultRetention().BodyMaxBytes {
		t.Errorf("BodyMaxBytes = %d, want the default", got.BodyMaxBytes)
	}
	if got.AnchorEvery != DefaultRetention().AnchorEvery {
		t.Errorf("AnchorEvery = %v, want the default", got.AnchorEvery)
	}
}

// TestDeletingAWatchLeavesItsHistory.
//
// passes.watch_id has no cascade for the same reason changes.pass_id has none:
// «why was I told this was gone» has to stay answerable after somebody tidied
// up the watch that said it.
func TestDeletingAWatchLeavesItsHistory(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := addTestWatch(t, s, "уйдёт", 300)

	if err := s.DeleteWatch(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Watches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the watch is still listed")
	}
}

// TestTheThreeSettingsThePanelWritesRoundTrip. requests_per_minute and the
// quiet hours are read through the store like everything else, so a schema
// change has one place to hide.
func TestTheThreeSettingsThePanelWritesRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	rpm, err := s.RequestsPerMinute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rpm != 6 {
		t.Errorf("requests per minute = %d on a fresh database, want the schema's 6", rpm)
	}
	if err := s.SetRequestsPerMinute(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if rpm, err = s.RequestsPerMinute(ctx); err != nil || rpm != 3 {
		t.Errorf("read back %d (%v)", rpm, err)
	}

	// -1 in either column is «no window», which is the schema's default. Not
	// zero: zero is midnight, and a window from midnight is a real request.
	from, to, err := s.QuietHours(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if from != -1 || to != -1 {
		t.Errorf("quiet hours = %d..%d on a fresh database, want -1..-1", from, to)
	}
	if err := s.SetQuietHours(ctx, 22*60+30, 7*60); err != nil {
		t.Fatal(err)
	}
	if from, to, err = s.QuietHours(ctx); err != nil || from != 1350 || to != 420 {
		t.Errorf("read back %d..%d (%v)", from, to, err)
	}

	floor, err := s.SpaceFloorBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if floor != 0 {
		t.Errorf("space floor = %d on a fresh database, want unset", floor)
	}
	if err := s.SetSpaceFloorBytes(ctx, 5<<30); err != nil {
		t.Fatal(err)
	}
	if floor, err = s.SpaceFloorBytes(ctx); err != nil || floor != 5<<30 {
		t.Errorf("read back %d (%v)", floor, err)
	}
}
