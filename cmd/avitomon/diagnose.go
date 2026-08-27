// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// The desktop diagnostic, and the one question it exists to answer.
//
// ClassSoftBlock on mobile means there was no `__initialData__` at all. That is
// equally the shape of a soft block and the shape of Avito rewriting the mobile
// page, and the two want opposite things from whoever is reading the panel: one
// waits, the other files a bug. Every other mobile outcome is unambiguous and
// never comes here — ClassEmpty is an answer corroborated by the site's own
// «Ничего не найдено», ClassShape has already proved the container intact.
//
// So: fetch the same watch once on desktop. Desktop hydration is a different
// document served by a different renderer. If it answers, the block is not on
// this installation and our reading of the mobile page is what broke. If it is
// walled too, we are blocked, and no amount of parser work will help.
//
// Two requests to tell a code change from a ban is a bargain. Two requests
// every tick is not, which is why this fires on the run reaching exactly two
// and never again for the same episode: at three the answer has been bought
// already and has not changed.

// diagnoseSoftBlock spends up to two requests to name what a mobile wall is.
//
// Errors are reported and swallowed. The pass is already written and the
// schedule already advanced; failing the collection because a diagnostic could
// not run would turn an explanation into an outage.
// The fetch is a parameter and not a *transport.Fetcher, because what this
// function decides is what two outcomes mean — and a decision that can only be
// exercised by making live requests to a blocked site is a decision nobody
// tests.
func diagnoseSoftBlock(ctx context.Context, s *store.Store,
	fetch func(context.Context, string) (transport.Result, error),
	w store.Watch, at int64,
) {
	// A seller watch has no measured mobile surface, so it can never be here on
	// mobile and there is no second surface to ask. Named rather than left to
	// fall out of the surface check, because it is the spec's own carve-out.
	if w.Kind == string(avito.KindSeller) || w.Surface == avito.SurfaceDesktop {
		return
	}
	run, err := s.SoftBlockRun(ctx, w.ID)
	if err != nil {
		fmt.Println("  диагностику не запустить:", err)
		return
	}
	if run != 2 {
		return
	}

	target, err := avito.SearchURL(w.URL, avito.SurfaceDesktop, 1)
	if err != nil {
		fmt.Println("  диагностику не запустить:", err)
		return
	}
	fmt.Println("  вторая мягкая блокировка подряд — спрашиваю десктопную версию")

	res, ferr := fetch(ctx, target)

	kind, field, now := track.KindTransportBlocked, "soft-block", ""
	switch {
	case ferr != nil:
		// Not an answer either way: the desktop attempt did not complete, so
		// it says nothing about which of the two explanations is true. Written
		// down anyway, because a diagnostic that silently fails leaves a person
		// looking at two walls and no note about the second.
		now = "десктопная версия не ответила: " + ferr.Error()
	case len(res.Page.Listings) > 0:
		// Desktop reads. The wall is not on this installation, and what broke
		// is our reading of the mobile page — which is what
		// collector-shape-unknown means everywhere else in this program.
		kind = track.KindCollectorShapeUnknown
		now = fmt.Sprintf("мобильная страница перестала читаться, "+
			"а десктопная отдала %d объявлений: это не блокировка, "+
			"а изменение мобильной вёрстки", len(res.Page.Listings))
	default:
		now = fmt.Sprintf("десктопная версия тоже за стеной (%s): "+
			"это блокировка, а не изменение вёрстки", res.Verdict.Class)
	}
	fmt.Println("  " + now)

	if err := s.RecordUserChange(ctx, store.ChangeRow{
		Kind: kind, WatchID: w.ID, TS: at,
		Field: field, Now: now, HasNow: true,
		// A stated fact about what two fetches returned, not a windowed
		// conclusion about the market.
		Confidence: 1.0, Producer: "transport",
	}); err != nil {
		fmt.Println("  диагностику не записать:", err)
	}
}
