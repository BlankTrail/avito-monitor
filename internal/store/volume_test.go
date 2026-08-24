// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestVolumeCountsEveryTable. Nobody administers this product, and without a
// number they can look at, the first time they learn the history is growing is
// the day the disk fills.
func TestVolumeCountsEveryTable(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	wid, err := s.AddWatch(ctx, aWatch())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePass(ctx, aPass(wid, 1000), listingsFor(1, 2, 3)); err != nil {
		t.Fatal(err)
	}

	v, err := s.Volume(ctx)
	if err != nil {
		t.Fatalf("Volume: %v", err)
	}
	if v.FileBytes <= 0 {
		t.Errorf("FileBytes = %d, want the real file size", v.FileBytes)
	}
	for table, want := range map[string]int64{
		"watches": 1, "passes": 1, "pass_items": 3,
		"listings": 3, "listing_snapshots": 3, "watch_members": 3,
	} {
		if v.Rows[table] != want {
			t.Errorf("Rows[%s] = %d, want %d", table, v.Rows[table], want)
		}
	}
	// An empty table must still be reported. A table that only appears once it
	// has rows is a table nobody notices starting to grow.
	if _, ok := v.Rows["changes"]; !ok {
		t.Error("the empty changes table is missing from the report")
	}
	if str := v.String(); !strings.Contains(str, "passes") {
		t.Errorf("String() = %q; it is what the volume command prints", str)
	}
}

// TestCheckSpaceRefusesBelowTheFloor. Collection stops below 256 MB free or
// below two percent of the volume, whichever is larger — before the write, not
// after SQLITE_FULL says so from the other side.
func TestCheckSpaceRefusesBelowTheFloor(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	// The real filesystem under a temp dir will not be near full, so the floor
	// is raised rather than the disk being filled.
	s.SetSpaceFloor(1 << 62)
	err := s.CheckSpace(ctx)
	if !errors.Is(err, ErrDiskLow) {
		t.Errorf("CheckSpace with the floor above the whole volume = %v, want ErrDiskLow", err)
	}

	s.SetSpaceFloor(0)
	if err := s.CheckSpace(ctx); err != nil {
		t.Errorf("CheckSpace at the default floor failed: %v", err)
	}
}

// TestCheckSpaceSaysHowMuchIsLeft. The error is read by a person deciding what
// to delete, and "not enough space" without a number tells them nothing.
func TestCheckSpaceSaysHowMuchIsLeft(t *testing.T) {
	s := openTemp(t)
	s.SetSpaceFloor(1 << 62)
	err := s.CheckSpace(context.Background())
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "MB free") {
		t.Errorf("err = %q; it must carry the numbers", err)
	}
}
