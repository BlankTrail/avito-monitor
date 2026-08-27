// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func storeWithTwoSoftBlocks(t *testing.T, kind string, surface avito.Surface) (*store.Store, store.Watch) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	w := store.Watch{
		Name: "n", Kind: kind, URL: "https://m.avito.ru/all?q=iphone",
		Surface: surface, MaxPages: 1, IntervalSec: 300,
		AbsenceConfirmations: 2, AbsenceMinSpanSec: 1200,
		Enabled: true, CreatedAt: 1000, UpdatedAt: 1000,
	}
	id, err := s.AddWatch(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	w.ID = id
	for i := 0; i < 2; i++ {
		if _, err := s.SavePass(ctx, store.Pass{
			WatchID: id, StartedAt: int64(1000 + i*60), FinishedAt: int64(1002 + i*60),
			Page: 1, Surface: surface, Class: "soft-block", HTTPStatus: 200,
			Path: "dom", Mode: "direct", Session: "s#0", Egress: "direct",
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return s, w
}

func lastChange(t *testing.T, s *store.Store) (string, string) {
	t.Helper()
	var kind, now string
	err := s.DB().QueryRow(
		`SELECT kind, now FROM changes ORDER BY id DESC LIMIT 1`).Scan(&kind, &now)
	if err != nil {
		t.Fatalf("ни одной записи: %v", err)
	}
	return kind, now
}

// TestADesktopThatAnswersMeansOurParserBroke.
//
// The whole reason two requests are worth spending. A person reading «стена» is
// told to wait; a person reading «вёрстка поменялась» is told something they
// can act on — and the two look identical from the mobile side.
func TestADesktopThatAnswersMeansOurParserBroke(t *testing.T) {
	s, w := storeWithTwoSoftBlocks(t, "search", avito.SurfaceMobile)
	var asked string
	diagnoseSoftBlock(context.Background(), s,
		func(_ context.Context, target string) (transport.Result, error) {
			asked = target
			return transport.Result{
				Verdict: transport.Verdict{Class: transport.ClassOK},
				Page:    avito.Page{Listings: make([]avito.Listing, 50)},
			}, nil
		}, w, 2000)

	if !strings.HasPrefix(asked, "https://www.avito.ru/") {
		t.Errorf("диагностика спросила не десктопную версию: %s", asked)
	}
	kind, now := lastChange(t, s)
	if kind != string(track.KindCollectorShapeUnknown) {
		t.Errorf("kind = %q", kind)
	}
	if !strings.Contains(now, "не блокировка") {
		t.Errorf("вывод не назван: %q", now)
	}
}

// TestADesktopBehindTheSameWallMeansAWall.
func TestADesktopBehindTheSameWallMeansAWall(t *testing.T) {
	s, w := storeWithTwoSoftBlocks(t, "search", avito.SurfaceMobile)
	diagnoseSoftBlock(context.Background(), s,
		func(context.Context, string) (transport.Result, error) {
			return transport.Result{
				Verdict: transport.Verdict{Class: transport.ClassSoftBlock},
			}, nil
		}, w, 2000)

	kind, now := lastChange(t, s)
	if kind != string(track.KindTransportBlocked) {
		t.Errorf("kind = %q", kind)
	}
	if !strings.Contains(now, "блокировка") {
		t.Errorf("вывод не назван: %q", now)
	}
}

// TestADiagnosticThatCouldNotRunIsStillWrittenDown.
//
// Two walls and no note about the second is worse than either wall: it reads as
// if the diagnostic said nothing, when in fact it never got to speak.
func TestADiagnosticThatCouldNotRunIsStillWrittenDown(t *testing.T) {
	s, w := storeWithTwoSoftBlocks(t, "search", avito.SurfaceMobile)
	diagnoseSoftBlock(context.Background(), s,
		func(context.Context, string) (transport.Result, error) {
			return transport.Result{}, errors.New("сеть недоступна")
		}, w, 2000)

	if _, now := lastChange(t, s); !strings.Contains(now, "не ответила") {
		t.Errorf("вывод = %q", now)
	}
}

// TestASellerWatchIsNeverDiagnosed. It has no measured mobile surface, so there
// is no second opinion to ask for — the spec's own carve-out.
func TestASellerWatchIsNeverDiagnosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    string
		surface avito.Surface
	}{
		{"магазин", "seller", avito.SurfaceDesktop},
		{"наблюдение и так десктопное", "search", avito.SurfaceDesktop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := storeWithTwoSoftBlocks(t, tc.kind, tc.surface)
			diagnoseSoftBlock(context.Background(), s,
				func(context.Context, string) (transport.Result, error) {
					t.Fatal("диагностика потратила запрос там, где спрашивать нечего")
					return transport.Result{}, nil
				}, w, 2000)
			var n int
			if err := s.DB().QueryRow(`SELECT count(*) FROM changes`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("записей = %d", n)
			}
		})
	}
}
