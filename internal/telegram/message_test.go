// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

func subject() Subject {
	return Subject{
		WatchName: "iPhone 15", Title: "iPhone 15, 128 ГБ",
		URLPath: "/m/x_7800000001",
	}
}

// TestAVanishedMessageIsPrefixedPredpolozhitelno.
//
// Decision 5 enables this alert by default, and 0.3 is a conclusion drawn from
// something not being in a window rather than a fact a page stated. The prefix
// is what makes an enabled-by-default removal alert honest rather than sloppy.
func TestAVanishedMessageIsPrefixedPredpolozhitelno(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingVanished, ItemID: 7800000001, Confidence: 0.3,
	}, subject())

	if !strings.HasPrefix(got, "Предположительно") {
		t.Errorf("message = %q; a 0.3 removal must say how sure it is", got)
	}
	if !strings.Contains(got, "iPhone 15") {
		t.Error("the message does not say which watch stopped seeing it")
	}
}

// TestAClosedMessageStatesItAsFact, because an item page said so and the
// confidence is 1.0.
func TestAClosedMessageStatesItAsFact(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingClosed, Confidence: 1.0,
	}, subject())
	if strings.Contains(strings.ToLower(got), "предположительно") {
		t.Errorf("a stated fact was hedged: %q", got)
	}
}

// TestAPriceMessageCarriesBothNumbersAndTheDirection.
//
// A message saying only «цена изменилась» makes the reader open the app to find
// out which way, which is the one thing a notification is supposed to save them.
func TestAPriceMessageCarriesBothNumbersAndTheDirection(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindPriceChanged, Field: "price",
		Was: "4000000", Now: "3500000", Unit: track.UnitMinor,
		HadBefore: true, HasNow: true, Confidence: 1.0,
	}, subject())

	// A non-breaking space between the thousands. The plan's test wrote a plain
	// one, which contradicts its own implementation comment — and the comment is
	// right: «40 000» wrapped after the 40 is a listing that appears to cost
	// forty roubles.
	for _, want := range []string{"40 000", "35 000"} {
		if !strings.Contains(got, want) {
			t.Errorf("message = %q; it does not carry %q", got, want)
		}
	}
	if !strings.Contains(got, "↓") && !strings.Contains(got, "упала") {
		t.Errorf("message = %q; it does not say which way", got)
	}
}

// TestMoneyIsRenderedFromMinorUnitsAndNeverFromAFloat.
//
// The single highest-consequence line in this codebase is the unit boundary. A
// hundredfold error still renders as a plausible number, which is exactly why
// it has to be tested at the point a person reads it.
func TestMoneyIsRenderedFromMinorUnitsAndNeverFromAFloat(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindPriceChanged, Was: "100", Now: "199",
		Unit: track.UnitMinor, HadBefore: true, HasNow: true,
	}, subject())
	if !strings.Contains(got, "1,00") && !strings.Contains(got, "1 ₽") {
		t.Errorf("message = %q; 100 minor units is one rouble", got)
	}
	if strings.Contains(got, "100 ₽") || strings.Contains(got, "199 ₽") {
		t.Errorf("message = %q; minor units were printed as roubles", got)
	}
}

// TestARepostNamesTheNumberItReplaces, so the reader can check.
func TestARepostNamesTheNumberItReplaces(t *testing.T) {
	s := subject()
	s.PreviousID = 7700000001
	got := Render(store.ChangeRow{
		Kind: track.KindListingReposted, Confidence: 0.8,
	}, s)
	if !strings.Contains(got, "7700000001") {
		t.Errorf("message = %q; a repost must name the old number", got)
	}
}

// TestEveryKindRenders.
//
// A kind with no case falls through to its own identifier, which reaches a
// person as "listing-left-window" and tells them nothing. The catalogue is
// closed, so this can be exhaustive.
func TestEveryKindRenders(t *testing.T) {
	for _, k := range track.Kinds() {
		got := Render(store.ChangeRow{Kind: k, Confidence: 1.0}, subject())
		if got == "" {
			t.Errorf("%q renders as nothing", k)
			continue
		}
		if strings.Contains(got, string(k)) {
			t.Errorf("%q renders as its own identifier: %q — that reaches a "+
				"person as jargon", k, got)
		}
	}
}

// TestTheListingIsLinkedSoTheReaderCanOpenIt.
func TestTheListingIsLinkedSoTheReaderCanOpenIt(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindListingAppeared, ItemID: 7800000001, Confidence: 1.0,
	}, subject())
	if !strings.Contains(got, "avito.ru/m/x_7800000001") {
		t.Errorf("message = %q; it carries no link", got)
	}
}

// TestAHealthMessageIsAboutTheInstallAndNotAboutAListing.
//
// The five health rules are filter-scoped because "my collector has been
// flagged" is not a fact about one listing. A message that carried a title and
// a link would invite the reader to go and look at a listing that has nothing
// wrong with it.
func TestAHealthMessageIsAboutTheInstallAndNotAboutAListing(t *testing.T) {
	s := subject()
	for _, k := range []track.Kind{
		track.KindTransportBlocked, track.KindExtractionFellBack,
		track.KindCollectorShapeUnknown, track.KindCollectorSilent,
		track.KindWorkerPanicked,
	} {
		got := Render(store.ChangeRow{Kind: k, ItemID: 7800000001, Confidence: 1}, s)
		if strings.Contains(got, "avito.ru") {
			t.Errorf("%q links a listing: %q", k, got)
		}
		if strings.Contains(got, s.Title) {
			t.Errorf("%q names a listing title: %q", k, got)
		}
	}
}

// TestAnUndescribableKindSaysThatRatherThanItsName.
//
// A kind this build cannot describe is a defect on our side. The person reading
// it should be told that, not handed our vocabulary — «listing-left-window»
// tells them nothing and looks like a crash.
func TestAnUndescribableKindSaysThatRatherThanItsName(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.Kind("price-doubled-somehow"), Confidence: 1,
	}, subject())
	if strings.Contains(got, "price-doubled-somehow") {
		t.Errorf("message = %q; it hands the reader an identifier", got)
	}
	if !strings.Contains(got, "не умеет описать") {
		t.Errorf("message = %q; it does not say the fault is ours", got)
	}
}

// TestKopecksAppearOnlyWhenThereAreAny. On this site there almost never are,
// which is precisely why printing «40 000,00 ₽» would look like a spreadsheet
// rather than a price.
func TestKopecksAppearOnlyWhenThereAreAny(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{4_000_000, "40\u00a0000 ₽"},
		{100, "1 ₽"},
		{199, "1,99 ₽"},
		{50, "0,50 ₽"},
		{1_234_567_800, "12\u00a0345\u00a0678 ₽"},
	}
	for _, c := range cases {
		if got := rubles(c.minor); got != c.want {
			t.Errorf("rubles(%d) = %q, want %q", c.minor, got, c.want)
		}
	}
}

// TestThePriceSeparatorDoesNotBreakAcrossLines.
//
// «40 000» wrapped after the 40 is a listing that appears to cost forty
// roubles, and a notification is read on a narrow screen.
func TestThePriceSeparatorDoesNotBreakAcrossLines(t *testing.T) {
	got := rubles(4_000_000)
	if strings.Contains(got, "40 000") { // an ordinary space
		t.Errorf("rubles = %q; the thousands separator can wrap", got)
	}
	if !strings.Contains(got, "40\u00a0000") {
		t.Errorf("rubles = %q, want a non-breaking separator", got)
	}
}

// TestNumbersReadAsRussian. Every other number in the message uses a comma, and
// «-12.5%» beside «40 000 ₽» reads as a copy-paste from somewhere else.
func TestNumbersReadAsRussian(t *testing.T) {
	got := Render(store.ChangeRow{
		Kind: track.KindPriceChanged, Was: "4000000", Now: "3500000",
		Unit: track.UnitMinor, HadBefore: true, HasNow: true, Confidence: 1,
	}, subject())
	if strings.Contains(got, "12.5") {
		t.Errorf("message = %q; the percentage uses a decimal point", got)
	}
	if !strings.Contains(got, "12,5") {
		t.Errorf("message = %q; it does not carry the size of the move", got)
	}
}

// TestAPriceChangeWithNoNumbersStillSaysSomethingTrue. The row is what it is;
// a message must not invent «→ 0 ₽» out of a missing field.
func TestAPriceChangeWithNoNumbersStillSaysSomethingTrue(t *testing.T) {
	got := Render(store.ChangeRow{Kind: track.KindPriceChanged, Confidence: 1}, subject())
	if strings.Contains(got, "0 ₽") {
		t.Errorf("message = %q; a missing price was printed as zero", got)
	}
	if !strings.Contains(got, "изменилась") {
		t.Errorf("message = %q", got)
	}
}

// TestEveryRenderedMessageFitsInOne. Render feeds SendMessage, which truncates
// — but a message that needed truncating in ordinary use would be one a person
// reads half of every time.
func TestEveryRenderedMessageFitsInOne(t *testing.T) {
	s := subject()
	s.Title = strings.Repeat("очень длинный заголовок объявления ", 5)
	for _, k := range track.Kinds() {
		got := Render(store.ChangeRow{
			Kind: k, ItemID: 7800000001, Was: "4000000", Now: "3500000",
			Unit: track.UnitMinor, HadBefore: true, HasNow: true, Confidence: 1,
		}, s)
		if truncate(got) != got {
			t.Errorf("%q renders longer than one message", k)
		}
	}
}
