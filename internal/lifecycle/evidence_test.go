// SPDX-License-Identifier: AGPL-3.0-or-later

package lifecycle

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func admissiblePass() PassFacts {
	return PassFacts{
		Class: "ok", Surface: "mobile", Path: "dom",
		Session: "s1", PrevSession: "s1",
		StartedAt: 10_000, PrevEvidenceAt: 9_700, IntervalSec: 300,
	}
}

func TestAnOrdinaryHealthyPassIsEvidence(t *testing.T) {
	if v := Admissible(admissiblePass()); !v.Admissible {
		t.Errorf("a clean mobile pass was refused: %s", v.Why)
	}
}

// TestEmptyIsEvidence.
//
// Offer Ledger's own evidential demanded items_seen > 0, so a watch whose
// result set legitimately emptied could never confirm the removal of its last
// listing. ClassEmpty means the surface's own container was present and
// reported zero, which is a real answer.
func TestEmptyIsEvidence(t *testing.T) {
	p := admissiblePass()
	p.Class = "empty"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("an empty answer was refused: %s — a query that legitimately "+
			"returned nothing is as healthy as one that returned fifty", v.Why)
	}
}

// TestEveryTransportRefusalIsNotEvidence.
func TestEveryTransportRefusalIsNotEvidence(t *testing.T) {
	for _, class := range []string{
		"throttled", "burned", "soft-block", "challenge", "cold", "mangled",
		"shape", "other", "",
	} {
		p := admissiblePass()
		p.Class = class
		if v := Admissible(p); v.Admissible {
			t.Errorf("class %q was admitted as evidence that something is gone", class)
		}
	}
}

// TestADesktopDomPassIsPresenceEvidenceOnly.
//
// On desktop the primary path is 'state'. loaderData.toggles carries
// runtime_catalog_drop_state_for_bots, so a silent fall back to 'dom' is the
// bot-classification warning itself — the earliest one available, earlier than
// a 429 and much earlier than a captcha.
func TestADesktopDomPassIsPresenceEvidenceOnly(t *testing.T) {
	p := admissiblePass()
	p.Surface, p.Path = "desktop", "dom"
	if v := Admissible(p); v.Admissible {
		t.Error("a desktop pass answered by the DOM fallback was admitted; the " +
			"fall back is the bot warning, not a reading")
	}
}

// TestAMobileDomPassIsEvidence.
//
// The mobile document carries no data-mfe-state block of any kind — measured
// 2026-08-23 — so on that surface 'dom' is the primary path and nothing about
// it is a degradation. Reading the rule as "state or nothing" would make every
// mobile pass inadmissible and no disappearance could ever be confirmed.
func TestAMobileDomPassIsEvidence(t *testing.T) {
	p := admissiblePass()
	p.Surface, p.Path = "mobile", "dom"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("the mobile primary path was refused: %s", v.Why)
	}
}

// TestAMobileStatePassIsAlsoEvidence. The mobile reader parses
// __initialData__ for the count, so a pass attributed to 'state' there is not
// a degradation either.
func TestAMobileStatePassIsAlsoEvidence(t *testing.T) {
	p := admissiblePass()
	p.Surface, p.Path = "mobile", "state"
	if v := Admissible(p); !v.Admissible {
		t.Errorf("a mobile state pass was refused: %s", v.Why)
	}
}

func TestADegradedExtractionIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.Degraded = true
	if v := Admissible(p); v.Admissible {
		t.Error("a pass carrying a degradation warning was admitted")
	}
}

func TestASeedIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.IsSeed = true
	if v := Admissible(p); v.Admissible {
		t.Error("a seeding pass was admitted; it has no predecessor to differ from")
	}
}

// TestACollapsedCountIsNotEvidence.
//
// A fall to below a quarter of the last admissible count is far outside
// ordinary churn on a page of thirty to fifty and well inside what a truncated
// or partially served page looks like.
func TestACollapsedCountIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.LastTotal, p.TotalCount = ptr(int64(40000)), ptr(int64(300))
	if v := Admissible(p); v.Admissible {
		t.Error("a count that fell from 40000 to 300 was admitted")
	}
}

// TestAnOrdinaryCountMoveIsStillEvidence. The guard is for a collapse, not for
// churn: a page of thirty on a busy query moves by a few percent every tick.
func TestAnOrdinaryCountMoveIsStillEvidence(t *testing.T) {
	p := admissiblePass()
	p.LastTotal, p.TotalCount = ptr(int64(40000)), ptr(int64(38000))
	if v := Admissible(p); !v.Admissible {
		t.Errorf("a five percent move was read as a collapse: %s", v.Why)
	}
}

// TestAMissingCountSkipsTheCollapseTestRatherThanFailingIt. A missing number
// must never become arithmetic on zero.
func TestAMissingCountSkipsTheCollapseTestRatherThanFailingIt(t *testing.T) {
	p := admissiblePass()
	p.LastTotal, p.TotalCount = ptr(int64(40000)), nil
	if v := Admissible(p); !v.Admissible {
		t.Errorf("a pass whose surface reported no count was refused: %s", v.Why)
	}
}

// TestAGapInCollectionIsNotEvidence.
//
// For a laptop this is a weekly event. A machine that slept from Friday to
// Monday wakes up, sees a page one that has turned over completely, and would
// report every listing it was watching as gone — one weekend producing a
// hundred false removals is how a person learns to ignore the notifications.
func TestAGapInCollectionIsNotEvidence(t *testing.T) {
	p := admissiblePass()
	p.PrevEvidenceAt = p.StartedAt - int64(4*p.IntervalSec)
	if v := Admissible(p); v.Admissible {
		t.Error("a pass more than three intervals after the last evidential one " +
			"was admitted; it is a re-seed, not a reading")
	}
}

// TestOneMissedTickIsNotAGap. Two intervals is inside ordinary jitter for a
// laptop that slept through a poll, and refusing there would make an install
// that is merely slow look like one that is broken.
func TestOneMissedTickIsNotAGap(t *testing.T) {
	p := admissiblePass()
	p.PrevEvidenceAt = p.StartedAt - int64(2*p.IntervalSec)
	if v := Admissible(p); !v.Admissible {
		t.Errorf("one missed tick was called a gap: %s", v.Why)
	}
}

// TestNoPreviousEvidentialPassSkipsTheGapGateRatherThanFailingIt.
//
// The gate is circular if read the other way: a pass would need an admissible
// predecessor to be admissible, and after a seed there is none — so the second
// pass of every watch would be refused, and then the third, for ever. Zero
// means "no gap is known", not "the gap is infinite".
func TestNoPreviousEvidentialPassSkipsTheGapGateRatherThanFailingIt(t *testing.T) {
	p := admissiblePass()
	p.PrevEvidenceAt = 0
	if v := Admissible(p); !v.Admissible {
		t.Errorf("the pass after a seed was refused: %s — no watch would ever "+
			"get its first admissible pass", v.Why)
	}
}

// TestEveryRefusalSaysWhy. This lands in passes.degraded_why and is read by
// somebody asking why their watch never tells them anything.
func TestEveryRefusalSaysWhy(t *testing.T) {
	refusals := []struct {
		name string
		edit func(*PassFacts)
	}{
		{"class", func(p *PassFacts) { p.Class = "soft-block" }},
		{"path", func(p *PassFacts) { p.Surface, p.Path = "desktop", "dom" }},
		{"degraded", func(p *PassFacts) { p.Degraded = true }},
		{"seed", func(p *PassFacts) { p.IsSeed = true }},
		{"collapse", func(p *PassFacts) {
			p.LastTotal, p.TotalCount = ptr(int64(40000)), ptr(int64(1))
		}},
		{"gap", func(p *PassFacts) {
			p.PrevEvidenceAt = p.StartedAt - int64(9*p.IntervalSec)
		}},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			p := admissiblePass()
			c.edit(&p)
			v := Admissible(p)
			if v.Admissible {
				t.Fatal("expected a refusal")
			}
			if strings.TrimSpace(v.Why) == "" {
				t.Error("the refusal carries no reason")
			}
		})
	}
}
