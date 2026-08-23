// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

// WarnCode names a condition worth counting. Codes exist rather than free text
// because the panel aggregates these, and an aggregate over prose is a grep.
type WarnCode string

const (
	// WarnShapeProbe: the items array was reached by matching on structure
	// rather than by a named rule. The page's shape has moved, and this is
	// reported before anyone notices the counts are wrong.
	WarnShapeProbe WarnCode = "shape-probe"
	// WarnDegraded: the surface's primary extractor failed and a fallback
	// answered. On desktop this is the bot-classification tell.
	WarnDegraded WarnCode = "degraded"
	// WarnScriptTypeMoved: the state block was found but under a script type
	// other than the one last seen. Cosmetic, recorded so a drift report can
	// say it moved.
	WarnScriptTypeMoved WarnCode = "script-type-moved"
	// WarnIDMismatch: a card was refused because its two identifiers disagreed.
	WarnIDMismatch WarnCode = "id-mismatch"
	// WarnPriceMismatch: the advisory price cross-check disagreed. Never
	// rejects a row; a collapse in the agreement rate is the unit having
	// changed.
	WarnPriceMismatch WarnCode = "price-mismatch"
	// WarnItemDropped: an item named by the page failed extraction. A partial
	// drop, not a failed page.
	WarnItemDropped WarnCode = "item-dropped"
)

// Warning is one occurrence, with enough context to act on.
type Warning struct {
	Code   WarnCode
	Detail string
}

// ExtractionReport is how the extraction went, and it is part of the golden
// fixture rather than a debugging aid. The same fifty listings reached by the
// DOM path instead of the state path is exactly the regression this design
// exists to catch, and a fixture pinning only the items would let it pass.
type ExtractionReport struct {
	Rule       string // "desktop.catalog.v1", "mobile.cards.v1", ...
	Path       Path
	ScriptType string // recorded, never required
	Found      int    // items the document named
	Dropped    int    // items named but not extractable
	Warnings   []Warning
}

// Warn records an occurrence. Repeats are kept: two are two facts, and the
// count is what a drift report reads.
func (r *ExtractionReport) Warn(code WarnCode, detail string) {
	r.Warnings = append(r.Warnings, Warning{Code: code, Detail: detail})
}

// HasWarning reports whether the code occurred at least once.
func (r *ExtractionReport) HasWarning(code WarnCode) bool {
	for _, w := range r.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
