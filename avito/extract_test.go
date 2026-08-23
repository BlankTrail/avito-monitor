// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"errors"
	"strings"
	"testing"
)

func TestExtractDesktopPrefersState(t *testing.T) {
	body := `<html><body>` +
		`<script data-mfe-state="true">{"loaderData":{"data":{"catalog":{"items":[` +
		`{"id":1,"urlPath":"/a_1","title":"A"}]}}}}</script>` + desktopCard + `</body></html>`
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(body)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathState || page.Report.Rule != RuleDesktopCatalog {
		t.Errorf("Path/Rule = %q/%q, want state/%s — the named state rule must "+
			"win when both paths are available", page.Report.Path, page.Report.Rule,
			RuleDesktopCatalog)
	}
	if page.Report.HasWarning(WarnDegraded) {
		t.Error("a healthy state parse was marked degraded")
	}
}

// TestExtractDesktopFallsBackAndSaysSo is the bot-classification tell. Deleting
// the state block must not fail the page — it must come back on the DOM path,
// with card fidelity, carrying a degradation warning.
func TestExtractDesktopFallsBackAndSaysSo(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceDesktop,
		Body: []byte(`<html><body>` + desktopCard + `</body></html>`)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathDOM {
		t.Fatalf("Path = %q, want dom", page.Report.Path)
	}
	if !page.Report.HasWarning(WarnDegraded) {
		t.Error("the state block was gone and nothing warned; that transition is " +
			"the earliest warning available that this install has been flagged")
	}
	if len(page.Listings) != 1 {
		t.Errorf("Listings = %d, want 1 — fidelity must survive the fallback",
			len(page.Listings))
	}
}

func TestExtractMobileDispatch(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceMobile,
		Body: []byte(mobileState + mobileCard)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Rule != RuleMobileCards {
		t.Errorf("Rule = %q, want %q", page.Report.Rule, RuleMobileCards)
	}
}

func TestExtractItemDispatch(t *testing.T) {
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(itemPage)}, Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Kind != EnvelopeItem {
		t.Errorf("Kind = %q, want %q", page.Kind, EnvelopeItem)
	}
}

func TestExtractRefusesAMangledBody(t *testing.T) {
	_, err := Extract(Document{Surface: SurfaceMobile, Body: []byte{0xff, 0xfe}}, Options{})
	if !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("err = %v, want ErrNotUTF8 — a body that cannot be what it "+
			"claims to be must never reach an extractor", err)
	}
}

func TestExtractNoExtractor(t *testing.T) {
	_, err := Extract(Document{Surface: SurfaceMobile,
		Body: []byte("<html><body>Телефоны и ничего больше</body></html>")}, Options{})
	if !errors.Is(err, ErrNoExtractor) {
		t.Fatalf("err = %v, want ErrNoExtractor", err)
	}
}

// TestExtractAuditRunsBothPaths pins the audit sample. A fallback that only
// runs when the primary fails has its first production execution during an
// incident, with no known-good disagreement baseline.
func TestExtractAuditRunsBothPaths(t *testing.T) {
	body := `<html><body>` +
		`<script data-mfe-state="true">{"loaderData":{"data":{"catalog":{"items":[` +
		`{"id":7775028313,"urlPath":"/a_7775028313","title":"A"}]}}}}</script>` +
		desktopCard + `</body></html>`
	page, err := Extract(Document{Surface: SurfaceDesktop, Body: []byte(body)},
		Options{AuditDOM: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Report.Path != PathState {
		t.Fatalf("the audit changed which path answered; it must not")
	}
	if page.Audit == nil {
		t.Fatal("Audit = nil with AuditDOM set")
	}
	if page.Audit.Agreed != 1 || page.Audit.OnlyState != 0 || page.Audit.OnlyDOM != 0 {
		t.Errorf("Audit = %+v, want the two paths to agree on one id", *page.Audit)
	}
	if strings.Contains(page.Report.Rule, "microdata") {
		t.Error("the audit's rule leaked into the report")
	}
}
