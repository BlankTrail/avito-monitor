// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/track"
)

// Subject is what the change is about, in the words a person uses.
//
// The change row carries numbers; this carries the names. They are separate
// because a change is written once by a producer that has no reason to know
// what a watch is called, and read later by a message that has no reason to
// know what a snapshot is.
type Subject struct {
	WatchName string
	Title     string
	// URLPath is the listing's path on avito.ru, with its leading slash.
	URLPath string
	// PreviousID is, for a repost, the number this listing replaces — so the
	// reader can check rather than take our word for it.
	PreviousID int64
}

// Render turns one change into the message a person reads.
func Render(c store.ChangeRow, s Subject) string {
	l := RenderLine(c, s)

	var b strings.Builder
	b.WriteString(l.Headline)
	if l.Title != "" {
		b.WriteString("\n")
		b.WriteString(l.Title)
	}
	if s.WatchName != "" {
		b.WriteString("\n")
		b.WriteString("Наблюдение: ")
		b.WriteString(s.WatchName)
	}
	if l.URL != "" {
		b.WriteString("\n")
		b.WriteString(l.URL)
	}
	return b.String()
}

// RenderLine is the same change as the three pieces a summary needs.
//
// Render is this plus the watch's name, which a summary states once at the top
// rather than forty times down the message.
func RenderLine(c store.ChangeRow, s Subject) Line {
	l := Line{Headline: lead(c.Confidence, headline(c, s)), URL: listingURL(c, s)}
	if !isHealth(c.Kind) {
		l.Title = s.Title
	}
	return l
}

// lead puts the hedge in front, or capitalises when there is nothing to hedge.
//
// Decision 5 ships the removal alert enabled, and 0.3 means the listing left a
// window rather than a page stating it was closed. The prefix is the whole of
// what makes that honest: an enabled-by-default removal alert that did not say
// how sure it was would be sloppy, and turning the alert off instead would lose
// the feature people asked for.
func lead(confidence float64, headline string) string {
	if confidence > 0 && confidence < 1.0 {
		return "Предположительно " + headline
	}
	return upperFirst(headline)
}

func upperFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

// headline is the one line that says what happened, in lower case so lead can
// decide how the sentence starts.
//
// One switch over a closed catalogue, and deliberately no default that prints
// the kind. A missing case would reach somebody as "listing-left-window", which
// tells them nothing — so the fallback names the shape of the problem instead,
// and a test walks track.Kinds() to make sure nothing reaches it.
func headline(c store.ChangeRow, s Subject) string {
	switch c.Kind {
	// The window producer.
	case track.KindListingAppeared:
		return "новое объявление"
	case track.KindListingReturned:
		return "объявление вернулось в выдачу"
	case track.KindListingLeftWindow:
		return "объявление ушло из отслеживаемой части выдачи"
	case track.KindListingVanished:
		return "объявление пропало"
	case track.KindListingClosed:
		return "объявление снято с публикации"
	case track.KindListingReposted:
		if s.PreviousID != 0 {
			return fmt.Sprintf("объявление выложено заново, вместо %d", s.PreviousID)
		}
		return "объявление выложено заново"
	case track.KindRepostRetracted:
		if s.PreviousID != 0 {
			return fmt.Sprintf("связь с объявлением %d отменена", s.PreviousID)
		}
		return "связь между объявлениями отменена"

	// The series producer.
	case track.KindPriceChanged:
		return priceMove(c)
	case track.KindPriceAppeared:
		return "у объявления появилась цена: " + money(c.Now)
	case track.KindPriceWithdrawn:
		return "цену убрали, была " + money(c.Was)
	case track.KindDiscountChanged:
		return fmt.Sprintf("скидка: %s%% → %s%%", plain(c.Was), plain(c.Now))
	case track.KindTitleChanged:
		return fmt.Sprintf("заголовок изменён\nбыло: %s\nстало: %s",
			plain(c.Was), plain(c.Now))
	case track.KindPromotionChanged:
		if c.Now == "true" {
			return "объявление продвигают"
		}
		return "продвижение объявления прекращено"
	case track.KindReservedChanged:
		if c.Now == "true" {
			return "объявление забронировано"
		}
		return "бронь с объявления снята"
	case track.KindListingBumped:
		return "объявление подняли в выдаче"

	// The transport producer: the collector observing itself. These are about
	// the install rather than about a listing, which is why isHealth keeps the
	// title and the link off them.
	case track.KindExtractionFellBack:
		return "разбор страницы пошёл по запасному пути — данных будет меньше"
	case track.KindTransportBlocked:
		return "сбор заблокирован площадкой"
	case track.KindCollectorShapeUnknown:
		return "разметка страницы изменилась, разбор больше не совпадает"
	case track.KindCollectorSilent:
		return "сбор молчит: за отведённое время не пришло ни одного ответа"
	case track.KindWorkerPanicked:
		return "рабочий процесс упал"
	case track.KindWatchUndersampled:
		return "наблюдение опрашивается реже, чем меняется выдача — часть " +
			"изменений будет пропущена"
	case track.KindWatchTooBroad:
		return "наблюдение слишком широкое: выдача не помещается в отслеживаемое окно"
	}
	// Not the identifier. A kind this build does not know how to describe is a
	// defect on our side, and the person reading it should be told that rather
	// than handed our vocabulary.
	return "изменение, которое эта сборка не умеет описать"
}

// priceMove is both numbers and the direction.
//
// A message saying only «цена изменилась» makes the reader open the app to find
// out which way, which is the one thing a notification is supposed to save them.
func priceMove(c store.ChangeRow) string {
	was, okWas := minor(c.Was)
	now, okNow := minor(c.Now)
	if !okWas || !okNow {
		return "цена изменилась"
	}
	verb := "выросла"
	if now < was {
		verb = "упала"
	}
	// The verb and not also an arrow. «Цена упала ↓» says one thing twice, and
	// the percentage below says it a third time.
	out := fmt.Sprintf("цена %s: %s → %s", verb, rubles(was), rubles(now))
	if pct, ok := track.PercentChange(was, now); ok {
		out += " (" + percent(pct) + ")"
	}
	return out
}

// percent is a signed percentage in Russian punctuation.
//
// A comma for the decimal, because every other number in these messages uses
// one and «-12.5%» beside «40 000 ₽» reads as a copy-paste from somewhere else.
func percent(v float64) string {
	return strings.Replace(fmt.Sprintf("%+.1f%%", v), ".", ",", 1)
}

// money renders one side of a change that holds a price.
func money(v string) string {
	if m, ok := minor(v); ok {
		return rubles(m)
	}
	return "не указана"
}

func minor(v string) (int64, bool) {
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// plain is a stored value shown as-is, for the fields that are not money.
func plain(v string) string {
	if v == "" {
		return "—"
	}
	return v
}

// rubles renders integer minor units.
//
// Never from a float, and this is the single highest-consequence line in the
// codebase: Avito states prices in whole roubles, Wildberries states them in
// kopecks, and code here was lifted from a Wildberries project. A hundredfold
// error still sorts correctly and still renders as a plausible number, so it is
// tested at the point a person reads it.
func rubles(minor int64) string {
	whole, frac := minor/100, minor%100
	if frac < 0 {
		frac = -frac
	}
	// The kopecks only when there are any — «40 000 ₽» and not «40 000,00 ₽»,
	// because the second reads as a spreadsheet. On this site there almost
	// never are any, which is precisely why printing them would look wrong.
	out := group(whole)
	if frac != 0 {
		out += fmt.Sprintf(",%02d", frac)
	}
	return out + " ₽"
}

// group inserts a separator every three digits from the right.
//
// The separator is U+00A0, a non-breaking space: a price broken across two
// lines reads as two numbers, and «40 000» wrapped after the 40 is a listing
// that appears to cost forty roubles.
const thinSpace = " "

func group(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")

	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(thinSpace)
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// listingURL is where to open it. Empty for a health change, which is about the
// install rather than about any one listing.
func listingURL(c store.ChangeRow, s Subject) string {
	if isHealth(c.Kind) {
		return ""
	}
	if s.URLPath != "" {
		return "https://www.avito.ru" + s.URLPath
	}
	if c.ItemID != 0 {
		// Avito resolves this form for any listing, so a message is still
		// openable when the path was never recorded.
		return "https://www.avito.ru/" + strconv.FormatInt(c.ItemID, 10)
	}
	return ""
}

func isHealth(k track.Kind) bool {
	switch k {
	case track.KindExtractionFellBack, track.KindTransportBlocked,
		track.KindCollectorShapeUnknown, track.KindCollectorSilent,
		track.KindWorkerPanicked, track.KindWatchUndersampled,
		track.KindWatchTooBroad:
		return true
	}
	return false
}
