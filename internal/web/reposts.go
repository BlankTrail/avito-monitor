// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// Перепубликации: what was joined to what, why, and the one button that says no.
//
// Every decision the linker reached is here, including the refusals and the
// proposals — because the question this screen answers is «почему мне не
// сказали про это объявление», and a decision shown only when it went the
// linker's way answers half of it.
//
// The undo is cheap because nothing was merged. Deleting the belief is the
// whole of it: the link goes to rejected, the pair is pinned so no future
// change to the ladder can re-link it, RecomputeThread splits the chain, and
// the alert that was withheld is replayed.

// repostLimit is how far back the screen goes.
const repostLimit = 200

func (s *Server) repostsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	links, err := s.Store.RepostLinks(ctx, repostLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]int64, 0, len(links)*2)
	for _, l := range links {
		items = append(items, l.SuccessorID, l.PredecessorID)
	}
	named, err := s.Store.ListingNames(ctx, items)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	b.WriteString(`<div class="card">`)
	b.WriteString(note("neutral",
		"Здесь видно, что с чем связано и на каком основании — и что связать "+
			"не удалось. Связь ничего не склеивает: она только говорит, что "+
			"это одно и то же объявление под новым номером, поэтому её можно "+
			"снять, и всё вернётся как было."))
	if len(links) == 0 {
		b.WriteString(note("neutral",
			"Пока ничего. Связи появляются, когда продавец снимает объявление "+
				"и выкладывает его заново. На мобильной выдаче они не "+
				"появятся: она не отдаёт продавца, а без него связывать нельзя."))
	}
	if q := r.URL.Query().Get("done"); q != "" {
		b.WriteString(note("neutral", q))
	}
	for _, l := range links {
		b.WriteString(repostCard(l, named))
	}
	b.WriteString(`</div>`)

	s.render(w, r, page{Title: "Перепубликации", Body: rawHTML(b.String())})
}

func repostCard(l store.RepostLinkRow, named map[int64]store.ListingName) string {
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h3>%s</h3>`, esc(stateSentence(l)))
	fmt.Fprintf(&b, `<p>%s<br><span class="hint">вместо</span><br>%s</p>`,
		listingLink(l.SuccessorID, named), listingLink(l.PredecessorID, named))
	fmt.Fprintf(&b, `<p class="hint">%s · разрыв %s · кандидатов %d</p>`,
		esc(basisWord(l.Basis, l.State)), esc(gapWords(l.GapSec)), l.Candidates)

	// The evidence, present and absent alike. «Описание не совпало» and
	// «описания не было» are different answers, and the second is the more
	// common one — description and image are on twenty rows in fifty.
	b.WriteString(`<table>`)
	for _, term := range l.Evidence {
		mark := `<span class="hint">—</span>`
		if term.Present {
			mark = "да"
		}
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td></tr>`,
			esc(term.Name), mark, esc(term.Detail))
	}
	b.WriteString(`</table>`)

	if l.State == "linked" || l.State == "proposed" {
		fmt.Fprintf(&b, `<form method="post" action="/reposts/reject" `+
			`data-confirm="Сказать, что это разные объявления? Связь снимется, `+
			`история разделится, а сообщение, которое из-за неё не ушло, `+
			`придёт с пометкой об исправлении. Эта пара больше никогда `+
			`не свяжется автоматически.">`+
			`<input type="hidden" name="successor" value="%d">`+
			`<input type="hidden" name="predecessor" value="%d">`+
			`<button class="btn" type="submit">Это не перепубликация</button>`+
			`</form>`, l.SuccessorID, l.PredecessorID)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func listingLink(id int64, named map[int64]store.ListingName) string {
	n, ok := named[id]
	title := n.Title
	if !ok || title == "" {
		title = "объявление " + strconv.FormatInt(id, 10)
	}
	if n.URLPath == "" {
		return esc(title)
	}
	return fmt.Sprintf(`<a href="https://www.avito.ru%s" rel="noreferrer">%s</a>`,
		attr(n.URLPath), esc(title))
}

// stateSentence is what happened, in a sentence rather than a stored word.
func stateSentence(l store.RepostLinkRow) string {
	switch l.State {
	case "linked":
		return "Связано: это одно объявление под новым номером"
	case "proposed":
		return "Похоже на перевыкладку, но связывать не стали"
	case "considered":
		return "Рассмотрено и не связано"
	case "rejected":
		if l.DecidedBy == "user" {
			return "Вы сказали, что это разные объявления"
		}
		return "Отклонено"
	case "retracted":
		return "Связь снята: старое объявление вернулось, значит оно не исчезало"
	}
	return l.State
}

func basisWord(basis, state string) string {
	if state != "linked" {
		return "не связано"
	}
	switch basis {
	case "strong":
		return "по заголовку и точному совпадению фотографии или текста"
	case "weak":
		return "по заголовку, адресу и близкой цене"
	case "manual":
		return "вручную"
	}
	return basis
}

func gapWords(sec int64) string {
	switch {
	case sec < 3600:
		return fmt.Sprintf("%d мин", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d ч", sec/3600)
	}
	return fmt.Sprintf("%d дн", sec/86400)
}

// rejectRepost is the undo, and it is all three acts or none.
//
// A pin without the split is a correction that did not take; a split without
// the pin is one that comes back on the next pass; and either without the
// replay leaves a person never told about a listing they asked to be told
// about. The store does the first two in one transaction; the third is a
// function field, because enqueueing a message means rendering one and this
// package must not learn how a notification is worded.
func (s *Server) rejectRepost(w http.ResponseWriter, r *http.Request) {
	successor, err1 := strconv.ParseInt(r.FormValue("successor"), 10, 64)
	predecessor, err2 := strconv.ParseInt(r.FormValue("predecessor"), 10, 64)
	if err1 != nil || err2 != nil || successor <= 0 || predecessor <= 0 {
		http.Error(w, "нет такой пары", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := s.Store.PinPair(ctx, successor, predecessor, s.now().Unix()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	done := "Связь снята, история разделена. Эта пара больше не свяжется автоматически."
	if s.ReplayAppearance != nil {
		n, err := s.ReplayAppearance(ctx, successor)
		switch {
		case err != nil:
			// The pin and the split have already happened and are correct. Say
			// what did not, rather than rolling back a correction the person
			// asked for because a message could not be queued.
			done += " Сообщение поставить в очередь не удалось: " + err.Error()
		case n > 0:
			done += fmt.Sprintf(" Сообщение об объявлении поставлено в очередь (%d).", n)
		default:
			// No rule matches it, or it was already sent. Both are ordinary
			// and neither is a failure — but silence here would read as one.
			done += " Сообщения не будет: под правила это объявление не подходит."
		}
	}
	http.Redirect(w, r, "/reposts?done="+url.QueryEscape(done), http.StatusSeeOther)
}
