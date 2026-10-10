// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the .want.json files from the current output")

type fixtureMeta struct {
	URL       string  `json:"url"`
	Surface   Surface `json:"surface"`
	FrozenNow int64   `json:"frozen_now"`
	Status    int     `json:"status"`
}

func loadFixture(t *testing.T, name string) (Document, fixtureMeta) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".html.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m fixtureMeta
	if mb, err := os.ReadFile(filepath.Join("testdata", name+".meta.json")); err == nil {
		if err := json.Unmarshal(mb, &m); err != nil {
			t.Fatalf("meta: %v", err)
		}
	}
	if m.Surface == "" {
		m.Surface = SurfaceDesktop
		if strings.Contains(name, "mobile") {
			m.Surface = SurfaceMobile
		}
	}
	return Document{Surface: m.Surface, URL: m.URL, Body: body}, m
}

var goldenFixtures = []string{
	"desktop-spa-shell",
	"desktop-canonical-p1",
	"desktop-seller",
	"desktop-item",
	"mobile-search-30",
	"mobile-search-empty",
	"mobile-search-snippet",
	"mobile-search-rich",
}

// TestGolden pins the whole Page for every captured page.
func TestGolden(t *testing.T) {
	for _, name := range goldenFixtures {
		t.Run(name, func(t *testing.T) {
			doc, m := loadFixture(t, name)
			page, err := Extract(doc, Options{Now: m.FrozenNow})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			got, err := json.MarshalIndent(page, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", name+".want.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", path)
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run: go test ./avito/ -update): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("%s: the extracted Page changed.\n"+
					"If this is intended, run: go test ./avito/ -update\n"+
					"and READ THE DIFF — a change of Report.Rule or Report.Path "+
					"with the same listings is the regression this fixture exists "+
					"to catch, not a formatting change.", name)
			}
		})
	}
}

// TestGoldenExpectations pins the handful of facts a diff would let slide,
// because a golden file only says "the same as last time".
func TestGoldenExpectations(t *testing.T) {
	cases := []struct {
		name     string
		kind     EnvelopeKind
		rule     string
		path     Path
		listings int
		total    int64 // -1 when the surface reports none
	}{
		{"desktop-spa-shell", EnvelopeRedirect, RuleDesktopRedirect, PathState, 0, -1},
		{"desktop-canonical-p1", EnvelopeCatalog, RuleDesktopCatalog, PathState, 50, 34692},
		// A seller page is a DIFFERENT envelope: no loaderData at all, items at
		// state.searchData.profileCatalog. Measured, and the reason the resolver
		// is an ordered table of named rules rather than one rule and a probe.
		{"desktop-seller", EnvelopeCatalog, RuleDesktopProfile, PathState, 15, 772},
		{"desktop-item", EnvelopeItem, RuleItemMarkers, PathDOM, 1, -1},
		{"mobile-search-30", EnvelopeCatalog, RuleMobileCards, PathDOM, 30, 59959},
		{"mobile-search-empty", EnvelopeCatalog, RuleMobileCards, PathDOM, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, m := loadFixture(t, c.name)
			page, err := Extract(doc, Options{Now: m.FrozenNow})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if page.Kind != c.kind {
				t.Errorf("Kind = %q, want %q", page.Kind, c.kind)
			}
			if page.Report.Rule != c.rule {
				t.Errorf("Rule = %q, want %q", page.Report.Rule, c.rule)
			}
			if page.Report.Path != c.path {
				t.Errorf("Path = %q, want %q", page.Report.Path, c.path)
			}
			if len(page.Listings) != c.listings {
				t.Errorf("Listings = %d, want %d", len(page.Listings), c.listings)
			}
			got := int64(-1)
			if page.TotalCount != nil {
				got = *page.TotalCount
			}
			if got != c.total {
				t.Errorf("TotalCount = %d, want %d", got, c.total)
			}
			// A healthy fixture warning about anything needs explaining before
			// it is committed, so the suite says so rather than leaving it in
			// a golden file nobody re-reads.
			if n := len(page.Report.Warnings); n != 0 {
				t.Errorf("Warnings = %d on a healthy capture: %+v", n, page.Report.Warnings)
			}
			if page.Report.Dropped != 0 {
				t.Errorf("Dropped = %d; every item the page named must extract",
					page.Report.Dropped)
			}
		})
	}
}

// TestDesktopPathsAgree is the calibration the audit sample exists to keep
// honest: on a real search page the two independent extractors must find the
// same ids.
func TestDesktopPathsAgree(t *testing.T) {
	doc, m := loadFixture(t, "desktop-canonical-p1")
	page, err := Extract(doc, Options{Now: m.FrozenNow, AuditDOM: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Audit == nil {
		t.Fatal("Audit = nil")
	}
	if page.Audit.Agreed != 50 || page.Audit.OnlyState != 0 || page.Audit.OnlyDOM != 0 {
		t.Errorf("Audit = %+v, want 50 agreed and nothing exclusive. A "+
			"disagreement here means one of the two paths has drifted, and the "+
			"whole point of having two is that they are checked against each "+
			"other before an incident.", *page.Audit)
	}
}

// TestSellerPageHasNoSecondOpinion states a known limit rather than leaving it
// to be discovered. The desktop DOM path selects on data-marker="item", which a
// seller page does not use, so a seller watch has no fallback if Avito ever
// withholds the state block from it.
func TestSellerPageHasNoSecondOpinion(t *testing.T) {
	doc, m := loadFixture(t, "desktop-seller")
	page, err := Extract(doc, Options{Now: m.FrozenNow, AuditDOM: true})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if page.Audit == nil {
		t.Fatal("Audit = nil")
	}
	if page.Audit.Agreed != 0 || page.Audit.OnlyDOM != 0 {
		t.Errorf("Audit = %+v — the DOM path started answering on a seller page. "+
			"That is good news, not a failure: update this test and the note in "+
			"docs/measurements, because the seller watch now has a fallback it "+
			"did not have.", *page.Audit)
	}
	if page.Audit.OnlyState != 15 {
		t.Errorf("OnlyState = %d, want 15", page.Audit.OnlyState)
	}
}
