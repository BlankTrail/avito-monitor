// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

// TestPageOk pins the one assertion no caller may replace with a status check.
// A 200 that carried no listings is the most expensive silent failure in this
// system: it reads as "the query matched nothing" and it is indistinguishable
// from a wall, from a dropped hydration state, and from our own parser having
// gone blind — which is exactly why the classifier is given the extraction and
// not just the body.
func TestPageOk(t *testing.T) {
	cases := []struct {
		name string
		page Page
		want bool
	}{
		{"a catalogue with listings", Page{Kind: EnvelopeCatalog, Listings: []Listing{{ItemID: 1}}}, true},
		{"a catalogue with none is not ok", Page{Kind: EnvelopeCatalog}, false},
		{"an item envelope is never ok", Page{Kind: EnvelopeItem, Listings: []Listing{{ItemID: 1}}}, false},
		{"an unknown envelope is never ok", Page{Kind: EnvelopeUnknown, Listings: []Listing{{ItemID: 1}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.page.Ok(); got != c.want {
				t.Errorf("Ok() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestReportWarn(t *testing.T) {
	var r ExtractionReport
	r.Warn(WarnShapeProbe, "items reached by structure, not by name")
	r.Warn(WarnShapeProbe, "items reached by structure, not by name")
	if len(r.Warnings) != 2 {
		t.Fatalf("Warnings = %d, want 2 — a repeated warning is two facts, "+
			"not one, and the count is what a drift report reads", len(r.Warnings))
	}
	if !r.HasWarning(WarnShapeProbe) {
		t.Error("HasWarning(WarnShapeProbe) = false after two were recorded")
	}
	if r.HasWarning(WarnDegraded) {
		t.Error("HasWarning(WarnDegraded) = true, but none was recorded")
	}
}
