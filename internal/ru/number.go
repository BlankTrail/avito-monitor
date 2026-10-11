// SPDX-License-Identifier: AGPL-3.0-or-later

package ru

import "strconv"

// Thousands writes a whole number the way Russian text does: groups of three
// separated by a narrow no-break space, «47 990». The panel's own prices are
// written so; a sentence elsewhere in the product that said «15500 ₽» beside
// them read like a different program.
func Thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3*3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, " "...)
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
