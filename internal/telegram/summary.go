// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"fmt"
	"strings"
)

// Line is one thing that happened, for a summary that covers many.
type Line struct {
	// Headline is the one-line description — what Render puts first.
	Headline string
	Title    string
	URL      string
}

// summaryDetail is how many of the changes a summary spells out.
//
// Five, because the point of aggregating is that forty messages are unreadable
// and a summary that lists all forty is the same forty messages in one. The
// number is small enough that the count at the top stays the thing a person
// reads first.
const summaryDetail = 5

// Summarise turns everything one rule fired on in a pass into one message.
//
// «Вместо сорока сообщений одно» — spec section 6.3. The count leads, because
// that is the fact: what a person needs from an aggregated alert is how much
// happened, and only then a sample of what.
//
// The rest are not silently dropped. The last line says how many were left out,
// so a person who wants all of them knows there are more to go and look at,
// rather than believing they have seen everything. Increment 6's export is
// where the full list belongs; this says plainly that it is not here yet.
func Summarise(ruleName string, lines []Line) string {
	if len(lines) == 0 {
		return ruleName
	}
	if len(lines) == 1 {
		return one(lines[0])
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", ruleName, changeCount(len(lines)))

	shown := lines
	if len(shown) > summaryDetail {
		shown = shown[:summaryDetail]
	}
	for _, l := range shown {
		b.WriteString("\n\n")
		b.WriteString(one(l))
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "\n\n… и ещё %s", changeCount(rest))
	}
	return b.String()
}

func one(l Line) string {
	var b strings.Builder
	b.WriteString(l.Headline)
	if l.Title != "" {
		b.WriteString("\n")
		b.WriteString(l.Title)
	}
	if l.URL != "" {
		b.WriteString("\n")
		b.WriteString(l.URL)
	}
	return b.String()
}

// changeCount agrees the noun with the number.
//
// Russian has three forms and getting them wrong reads as a machine talking:
// «1 изменение», «2 изменения», «5 изменений». The rule is the standard one —
// the teens are all the many-form, and it is the last digit that decides
// otherwise.
func changeCount(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "изменение", "изменения", "изменений"))
}

func plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if n%100 >= 11 && n%100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
