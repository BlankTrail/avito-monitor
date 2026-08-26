// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"strings"

	"github.com/BlankTrail/avito-monitor/avito"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// The arithmetic that decides whether a schedule can be kept.
//
// In its own file because it is the one thing on the watches screen that can be
// wrong without anybody noticing. A schedule accepted that quietly exceeds the
// address ceiling is a ban three days later, and by then nobody connects the
// two — so this is testable as arithmetic rather than through a form post.

// hardCeilingPerMin is the measured per-address ceiling, a minute at a time.
//
// transport.DefaultCeiling requests in transport.DefaultWindow — sixty in ten
// minutes. It is the binding limit and not the per-identity floor of 3.5 s,
// which would allow seventeen a minute: quoting the looser of two limits is how
// a budget gets overspent.
func hardCeilingPerMin() float64 {
	return float64(transport.DefaultCeiling) / transport.DefaultWindow.Minutes()
}

// Budget is what the addresses allow against what the watches ask for.
type Budget struct {
	// Addresses is how many distinct egress addresses are configured, never
	// fewer than one — the host's own counts.
	Addresses int
	// PerAddressPerMin is the effective ceiling for one address.
	PerAddressPerMin float64
	CapacityPerMin   float64
	UsedPerMin       float64
}

func (b Budget) FreePerMin() float64 { return b.CapacityPerMin - b.UsedPerMin }

// Fits reports whether one more cost can be added.
//
// Inclusive of the ceiling: a person who filled their budget exactly meant to,
// and telling them their own arithmetic is wrong at the last request would be
// the product arguing with them over a rounding.
func (b Budget) Fits(costPerMin float64) bool {
	return b.UsedPerMin+costPerMin <= b.CapacityPerMin+1e-9
}

// CostPerMinute is what one watch asks of the budget.
//
// One request per page per pass. A zero or negative interval costs nothing
// rather than infinity: zero is what an unsaved form and a hand-edited row both
// hold, and sixty divided by it is +Inf — which would make every budget full and
// every save refused, a product that cannot be configured because one row is
// malformed.
func CostPerMinute(w store.Watch) float64 {
	if !w.Enabled || w.IntervalSec <= 0 {
		return 0
	}
	pages := w.MaxPages
	if pages <= 0 {
		// A pass that fetched nothing would not be a pass.
		pages = 1
	}
	return float64(pages*requestsPerPage(w.Surface)) * 60 / float64(w.IntervalSec)
}

// requestsPerPage is what one page of a surface actually costs.
//
// Two on desktop and one on mobile, and this is not a detail. The desktop
// search page answers with a shell carrying a canonical URL; the listings are
// on the page that URL names, so the transport fetches both. A budget that
// counted one would let somebody schedule twice what their address can carry
// and learn the arithmetic as a ban.
func requestsPerPage(surface avito.Surface) int {
	if surface == avito.SurfaceDesktop {
		return 2
	}
	return 1
}

// BudgetFor is the arithmetic for a set of watches.
//
// exclude is the id of the watch being edited, whose current cost must not be
// counted against the schedule replacing it. Without it every edit looks like an
// addition, and tightening an interval from ten minutes to five is refused for
// exceeding a budget the watch's own old cost was filling.
func BudgetFor(requestsPerMinute, addresses int, watches []store.Watch, exclude int64) Budget {
	if addresses < 1 {
		// The host's own address. A budget of nothing would refuse every watch
		// on an install that has not configured a proxy, which is most of them.
		addresses = 1
	}
	per := hardCeilingPerMin()
	if v := float64(requestsPerMinute); v > 0 && v < per {
		// Lower than the measurement is the user choosing to be gentler, and
		// that direction is theirs to choose. Higher is not.
		per = v
	}

	b := Budget{
		Addresses:        addresses,
		PerAddressPerMin: per,
		CapacityPerMin:   per * float64(addresses),
	}
	for _, w := range watches {
		if w.ID == exclude {
			continue
		}
		b.UsedPerMin += CostPerMinute(w)
	}
	return b
}

// Explain is the sum, written out.
//
// The spec says a schedule over the ceiling is refused «with the sum shown».
// «Слишком часто» tells a person nothing about which of their numbers to
// change, so this names every term: what is already spent, what this schedule
// asks for, and what the addresses allow.
func (b Budget) Explain(costPerMin float64) string {
	var s strings.Builder
	fmt.Fprintf(&s, "Уже занято %s запр./мин, это наблюдение просит %s, ",
		num(b.UsedPerMin), num(costPerMin))
	fmt.Fprintf(&s, "потолок %s — %d %s × %s запр./мин.",
		num(b.CapacityPerMin), b.Addresses,
		ru.Plural(b.Addresses, "адрес", "адреса", "адресов"),
		num(b.PerAddressPerMin))
	return s.String()
}

// num renders a rate without trailing zeroes, because «0,20 запр./мин» reads as
// a measurement and this is a division.
func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	// A comma, like every other number this product shows a Russian reader.
	return strings.Replace(s, ".", ",", 1)
}
