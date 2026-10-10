// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// TestAPanicIsWrittenWhereThePanelReadsIt.
//
// track.KindWorkerPanicked has existed since increment 1 with nothing writing
// it. A crash in a background goroutine that only reached a log file is a crash
// nobody sees — which on a tray build is the icon vanishing with an explanation
// nobody has open.
func TestAPanicIsWrittenWhereThePanelReadsIt(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.RecordWorkerPanic(ctx, "сборщик",
		"боль\ngoroutine 1 [running]:", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	changes, err := s.Changes(ctx, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("%d changes", len(changes))
	}
	got := changes[0]
	if got.Kind != track.KindWorkerPanicked {
		t.Errorf("kind = %q", got.Kind)
	}
	if got.Field != "сборщик" {
		t.Errorf("the row does not name the goroutine: %q", got.Field)
	}
	if got.Now == "" {
		t.Error("the row carries no stack")
	}
	// A background goroutine belongs to the installation and not to one watch.
	// Attributing the crash to whichever watch happened to be next would be a
	// lie in the one row that has to be trustworthy.
	if got.WatchID != 0 {
		t.Errorf("the crash was attributed to watch %d", got.WatchID)
	}
}

// TestAnExitAddedAfterStartChangesTheFingerprint, which is what makes the
// supervisor reopen the collector instead of collecting on without it.
func TestAnExitAddedAfterStartChangesTheFingerprint(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := transportFingerprint(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveChannel(ctx, store.Channel{Name: "эта машина", Kind: "direct",
		Weight: 100, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := transportFingerprint(ctx, s)
	if before == after {
		t.Error("adding an exit left the fingerprint unchanged")
	}
	if err := s.SetBlankTrailConfig(ctx, store.BlankTrailConfig{APIKey: "k", Ports: 2}); err != nil {
		t.Fatal(err)
	}
	if again, _ := transportFingerprint(ctx, s); again == after {
		t.Error("a new key left the fingerprint unchanged")
	}
}
