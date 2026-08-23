// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "errors"

// ErrNoExtractor means no rule recognised the document. It is not the same as
// an empty result set, and the caller's classifier must not conflate them: one
// is our failure to read, the other is the site's answer.
var ErrNoExtractor = errors.New("avito: no extractor recognised this document")

// Options is everything Extract needs from the outside world. The clock is a
// parameter because this package must not read one.
type Options struct {
	// Now is Unix seconds. Extraction does not use it today; fixtures carry a
	// frozen value so a page with a validity window cannot make the suite fail
	// on a day nobody changed anything.
	Now int64

	// AuditDOM runs the second path alongside a successful first one and
	// records the comparison. The caller samples it — once every fifty pages,
	// plus the first page of each watch — because a fallback whose first
	// production execution is during an incident has no baseline to be judged
	// against.
	AuditDOM bool
}

// Extract turns one document into one Page.
//
// Order matters and is per surface. On desktop the state block is the primary
// path and a fall back to the DOM is the bot-classification tell, so it is
// recorded as a degradation. On mobile there is no state catalogue at all, so
// the DOM is the primary path and nothing about it is a degradation.
func Extract(d Document, opt Options) (Page, error) {
	if err := d.Check(); err != nil {
		return Page{}, err
	}
	switch d.Surface {
	case SurfaceMobile:
		if page, ok := extractMobile(d); ok {
			return page, nil
		}
		if page, ok := extractItem(d); ok {
			return page, nil
		}
	default:
		if page, ok := extractDesktopState(d); ok {
			if opt.AuditDOM && page.Kind == EnvelopeCatalog {
				page.Audit = auditAgainstDOM(d, page)
			}
			return page, nil
		}
		if page, ok := extractItem(d); ok {
			return page, nil
		}
		if page, ok := extractDesktopDOM(d); ok {
			page.Report.Warn(WarnDegraded,
				"the desktop state block was absent; the DOM path answered")
			return page, nil
		}
	}
	return Page{}, ErrNoExtractor
}

// auditAgainstDOM runs the second path and compares the id sets. It never
// changes which path answered, and its own rule never reaches the report.
func auditAgainstDOM(d Document, primary Page) *AuditResult {
	other, ok := extractDesktopDOM(d)
	if !ok {
		return &AuditResult{OnlyState: len(primary.Listings)}
	}
	inState := make(map[int64]bool, len(primary.Listings))
	for _, l := range primary.Listings {
		inState[l.ItemID] = true
	}
	var res AuditResult
	inDOM := make(map[int64]bool, len(other.Listings))
	for _, l := range other.Listings {
		inDOM[l.ItemID] = true
		if inState[l.ItemID] {
			res.Agreed++
		} else {
			res.OnlyDOM++
		}
	}
	for id := range inState {
		if !inDOM[id] {
			res.OnlyState++
		}
	}
	return &res
}
