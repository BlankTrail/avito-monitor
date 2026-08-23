// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "strings"

// Labels for what a wall turned out to be. The transport decides what to do
// about each; this package only says which one it saw.
const (
	LabelIPBlock     = "ip-block"
	LabelHardIPBlock = "hard-ip-block"
	LabelSlider      = "slider"
	LabelGeetest     = "geetest"
	LabelRestricted  = "access-restricted"
	LabelWAF         = "waf"
	LabelRateLimit   = "rate-limit"
)

// WallHit is the marker that matched and what it means.
type WallHit struct {
	Marker string
	Label  string
}

// wallMarkers is ordered most specific first: "подключиться к другой сети" is a
// hard block and also appears on pages that carry the softer IP-block wording,
// so it must be tested before it.
var wallMarkers = []WallHit{
	{"подключиться к другой сети", LabelHardIPBlock},
	{"проблема с ip", LabelIPBlock},
	{"продолжить для решения", LabelSlider},
	{"gcaptcha4", LabelGeetest},
	{"geetest", LabelGeetest},
	{"доступ ограничен", LabelRestricted},
	{"qrator", LabelWAF},
	{"слишком много запросов", LabelRateLimit},
}

// Wall reports whether the body is a challenge or block page rather than
// content. It is deliberately independent of the HTTP status: the measured
// direct baseline is a 429 whose body carries a wall marker, and a challenge
// served as a 200 is ordinary on this target.
func (d Document) Wall() (WallHit, bool) {
	low := strings.ToLower(string(d.Body))
	for _, m := range wallMarkers {
		if strings.Contains(low, m.Marker) {
			return m, true
		}
	}
	return WallHit{}, false
}
