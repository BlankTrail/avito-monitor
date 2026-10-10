// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"
)

// This file is the escaping discipline, named in one place.
//
// Every handler in this package assembles its own HTML rather than rendering a
// template per screen — the donor's reasoning, and it holds: a screen is a
// function, and a function is easier to read than a template plus the struct it
// is given. The cost is that escaping is manual, so it gets three named helpers
// and a rule: no user value reaches a page except through one of them.

// esc is a value inside an element.
func esc(s string) string { return html.EscapeString(s) }

// attr is a value inside a double-quoted attribute.
//
// Separate from esc so the call site says which context it is in. They do the
// same thing today — html.EscapeString escapes the quote as well — and a future
// reader changing one must be able to see that the other exists.
func attr(s string) string { return html.EscapeString(s) }

// rawHTML marks a string as already-escaped markup.
//
// This is where the discipline is named, so that a future caller passing a raw
// user string through it has one obvious line to have gone past.
func rawHTML(s string) template.HTML { return template.HTML(s) }

// field renders one labelled input.
func field(label, name, value, kind, hint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<label class="field"><span class="label">%s</span>`, esc(label))
	fmt.Fprintf(&b, `<input class="input" type="%s" name="%s" value="%s">`,
		attr(kind), attr(name), attr(value))
	if hint != "" {
		fmt.Fprintf(&b, `<span class="hint">%s</span>`, esc(hint))
	}
	b.WriteString(`</label>`)
	return b.String()
}

// checkbox renders one labelled tick.
//
// The hidden companion is not decoration: an unticked checkbox posts nothing at
// all, so a form read with r.FormValue cannot tell "unticked" from "this build
// does not have that field" — and the second must never turn a setting off.
func checkbox(label, name string, on bool, hint string) string {
	checked := ""
	if on {
		checked = " checked"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<input type="hidden" name="%s" value="0">`, attr(name))
	fmt.Fprintf(&b, `<label class="check"><input type="checkbox" name="%s" value="1"%s>`,
		attr(name), checked)
	fmt.Fprintf(&b, `<span>%s</span>`, esc(label))
	if hint != "" {
		fmt.Fprintf(&b, `<span class="hint">%s</span>`, esc(hint))
	}
	b.WriteString(`</label>`)
	return b.String()
}

// ticked reads a checkbox that checkbox() wrote, and it is the other half of
// that function.
//
// Last value and not the first, which is the whole point. checkbox() emits a
// hidden "0" before the box so that unticking posts something — otherwise a
// form read field by field cannot tell «unticked» from «this build has no such
// field», and the second must never turn a setting off. But a ticked box then
// posts both, in that order, and r.FormValue returns the *first* — so every
// checkbox on this panel read that way was permanently off, whatever the person
// clicked. Last-wins is the convention every other form processor uses for
// exactly this pair, and it is what makes the hidden companion work as
// intended rather than as a floor.
func ticked(r *http.Request, name string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	v := r.Form[name]
	if len(v) == 0 {
		return false
	}
	return v[len(v)-1] == "1"
}

// tickedOr is ticked with a default for a form that was never sent.
//
// checkbox() always posts its hidden «0», so a sent form carries the field
// whether ticked or not; a page opened fresh carries nothing. The results
// screen's «Только те, что сейчас в выдаче» had to default to on — its own hint
// says «снимите, чтобы увидеть и то, что пропало» — and opened unticked,
// because «absent» read as «off».
func tickedOr(r *http.Request, name string, def bool) bool {
	if err := r.ParseForm(); err != nil {
		return def
	}
	if len(r.Form[name]) == 0 {
		return def
	}
	return ticked(r, name)
}

// note renders one line of explanation, in a tone.
//
// tone is "neutral", "warning" or "bad". Three and not more: the panel says
// «всё в порядке», «обратите внимание» or «сломано», and a fourth shade would be
// a distinction the reader has to learn.
func note(tone, text string) string {
	return fmt.Sprintf(`<p class="note %s">%s</p>`, attr(tone), esc(text))
}

// writeFragment answers one of app.js's data-load requests.
//
// The charset is on it for the same reason it is on a full page: a Russian
// fragment served without one is a row of question marks in whatever browser
// guesses wrong.
func writeFragment(w http.ResponseWriter, markup string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(markup))
}
