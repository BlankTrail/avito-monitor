// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"fmt"
	"strings"
	"testing"
)

func lines(n int) []Line {
	out := make([]Line, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Line{
			Headline: fmt.Sprintf("Цена упала: %d ₽", 40000-i),
			Title:    fmt.Sprintf("iPhone 15, объявление %d", i),
			URL:      fmt.Sprintf("https://www.avito.ru/78000000%02d", i),
		})
	}
	return out
}

// TestFortyChangesFitInOneMessageWorthReading.
//
// The point of aggregating is that forty messages are unreadable. A summary
// that lists all forty is the same forty messages in one envelope, which solves
// nothing.
func TestFortyChangesFitInOneMessageWorthReading(t *testing.T) {
	got := Summarise("Изменилась цена", lines(40))
	if strings.Count(got, "https://") > summaryDetail {
		t.Errorf("the summary lists %d listings; it should sample, not enumerate",
			strings.Count(got, "https://"))
	}
	if truncate(got) != got {
		t.Error("the summary is longer than one Telegram message")
	}
}

// TestTheCountLeads. What a person needs from an aggregated alert is how much
// happened, and only then a sample of what.
func TestTheCountLeads(t *testing.T) {
	got := Summarise("Изменилась цена", lines(40))
	head := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(head, "40") {
		t.Errorf("first line = %q; it does not say how many", head)
	}
	if !strings.HasPrefix(head, "Изменилась цена") {
		t.Errorf("first line = %q; it does not name the rule", head)
	}
}

// TestTheRestAreNotSilentlyDropped.
//
// A person who wants all forty must know there are more to go and look at,
// rather than believing they have seen everything.
func TestTheRestAreNotSilentlyDropped(t *testing.T) {
	got := Summarise("Изменилась цена", lines(40))
	if !strings.Contains(got, "и ещё") {
		t.Errorf("summary = %q; it does not say that some were left out", got)
	}
	if !strings.Contains(got, fmt.Sprintf("%d", 40-summaryDetail)) {
		t.Errorf("summary = %q; it does not say how many were left out", got)
	}
}

// TestOneChangeIsJustThatChange. A summary of one that says «1 изменение» and
// then shows the one is a machine padding its output.
func TestOneChangeIsJustThatChange(t *testing.T) {
	got := Summarise("Изменилась цена", lines(1))
	if strings.Contains(got, "1 изменение") {
		t.Errorf("summary = %q; a summary of one should read as the change", got)
	}
	if !strings.Contains(got, "Цена упала") {
		t.Errorf("summary = %q", got)
	}
}

// TestSixChangesShowFiveAndSayOneMore, the boundary either side of the sample.
func TestSixChangesShowFiveAndSayOneMore(t *testing.T) {
	if got := Summarise("Цена", lines(summaryDetail)); strings.Contains(got, "и ещё") {
		t.Errorf("summary = %q; nothing was left out", got)
	}
	got := Summarise("Цена", lines(summaryDetail+1))
	if !strings.Contains(got, "и ещё 1 изменение") {
		t.Errorf("summary = %q; want «и ещё 1 изменение»", got)
	}
}

// TestTheNounAgreesWithTheNumber.
//
// Russian has three forms and getting them wrong is the clearest possible sign
// that nobody read the output: «5 изменение» reads as a machine talking.
func TestTheNounAgreesWithTheNumber(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1 изменение"},
		{2, "2 изменения"},
		{4, "4 изменения"},
		{5, "5 изменений"},
		{11, "11 изменений"}, // the teens are all the many-form…
		{12, "12 изменений"},
		{14, "14 изменений"},
		{21, "21 изменение"}, // …and otherwise the last digit decides
		{22, "22 изменения"},
		{25, "25 изменений"},
		{101, "101 изменение"},
		{111, "111 изменений"},
	}
	for _, c := range cases {
		if got := changeCount(c.n); got != c.want {
			t.Errorf("changeCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestASummaryOfNothingIsNotAnEmptyMessage. Flush never calls it with none, but
// a body of "" would be a message a person cannot interpret at all.
func TestASummaryOfNothingIsNotAnEmptyMessage(t *testing.T) {
	if got := Summarise("Изменилась цена", nil); got == "" {
		t.Error("a summary of nothing is empty")
	}
}

// TestASummaryStatesTheWatchOnceRatherThanFortyTimes, which is why RenderLine
// exists beside Render.
func TestASummaryStatesTheWatchOnceRatherThanFortyTimes(t *testing.T) {
	got := Summarise("Изменилась цена", lines(40))
	if n := strings.Count(got, "Наблюдение:"); n != 0 {
		t.Errorf("the summary repeats the watch name %d times", n)
	}
}
