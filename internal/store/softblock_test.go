// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// saveClasses writes one pass per class, a minute apart, oldest first.
func saveClasses(t *testing.T, s *Store, wid int64, classes ...string) {
	t.Helper()
	ctx := context.Background()
	for i, class := range classes {
		p := aPass(wid, int64(1000+i*60))
		p.Class = class
		if _, err := s.SavePass(ctx, p, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTheDiagnosticFiresOnceAndNotEveryTick.
//
// The run reaching exactly two is the whole trigger. One soft block is not yet
// ambiguous enough to spend two requests on; three means the answer was bought
// on the pass before and has not changed since, and buying it again on every
// tick for as long as a block lasts is how a monitor earns a 403 while trying
// to explain one.
func TestTheDiagnosticFiresOnceAndNotEveryTick(t *testing.T) {
	for _, tc := range []struct {
		name    string
		classes []string
		want    int
	}{
		{"чисто", []string{"ok", "ok"}, 0},
		{"первая", []string{"ok", "soft-block"}, 1},
		{"вторая — вот тут", []string{"ok", "soft-block", "soft-block"}, 2},
		{"третья — уже спрашивали", []string{"soft-block", "soft-block", "soft-block"}, 3},
		{"прервалась и началась заново", []string{"soft-block", "ok", "soft-block"}, 1},
		{"пустая выдача — это ответ, а не стена", []string{"soft-block", "empty"}, 0},
		{"наша поломка — не стена", []string{"soft-block", "shape"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			wid, err := s.AddWatch(context.Background(), aWatch())
			if err != nil {
				t.Fatal(err)
			}
			saveClasses(t, s, wid, tc.classes...)
			got, err := s.SoftBlockRun(context.Background(), wid)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("подряд = %d, ожидалось %d (%v)", got, tc.want, tc.classes)
			}
		})
	}
}

// TestAWatchWithNoPassesHasNoRun. The first pass a brand-new watch ever makes
// must not read as the second of anything.
func TestAWatchWithNoPassesHasNoRun(t *testing.T) {
	s := openTemp(t)
	wid, err := s.AddWatch(context.Background(), aWatch())
	if err != nil {
		t.Fatal(err)
	}
	if run, err := s.SoftBlockRun(context.Background(), wid); err != nil || run != 0 {
		t.Fatalf("= %d, %v", run, err)
	}
}
