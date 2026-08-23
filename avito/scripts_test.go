// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"strings"
	"testing"
)

func TestScriptTextByAttr(t *testing.T) {
	body := []byte(`<html><head>` +
		`<script src="/x.js"></script>` +
		`<script type="mime/invalid" data-mfe-state="true">{"a":1}</script>` +
		`</head></html>`)

	text, typ, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("the state block was not found by its data attribute")
	}
	if text != `{"a":1}` {
		t.Errorf("text = %q, want %q", text, `{"a":1}`)
	}
	if typ != "mime/invalid" {
		t.Errorf("scriptType = %q, want %q — it is recorded, never required", typ, "mime/invalid")
	}
}

// TestScriptTypeIsNotRequired pins the selection rule. The type is a deliberate
// non-MIME chosen so no browser executes the block; it is presentation trivia
// and the likeliest thing to be renamed. Requiring it would turn a cosmetic
// change into a total parse failure.
func TestScriptTypeIsNotRequired(t *testing.T) {
	body := []byte(`<script type="application/some-new-thing" data-mfe-state="true">{"a":1}</script>`)
	text, typ, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("renaming the script type broke selection; it must not")
	}
	if text != `{"a":1}` {
		t.Errorf("text = %q, want %q", text, `{"a":1}`)
	}
	if typ != "application/some-new-thing" {
		t.Errorf("scriptType = %q; the new name must be recorded so a drift "+
			"report can say it moved", typ)
	}
}

// TestScriptTextIsNeverUnescaped is the property whose absence corrupts a
// seller's title with no error anywhere. x/net/html implements the HTML5
// script-data state: Text() returns the server's exact bytes for a raw tag.
func TestScriptTextIsNeverUnescaped(t *testing.T) {
	body := []byte(`<script data-mfe-state="true">{"title":"Ремонт &amp; отделка &lt;срочно&gt;"}</script>`)
	text, _, ok := scriptTextByAttr(body, "data-mfe-state", "true")
	if !ok {
		t.Fatal("not found")
	}
	if !strings.Contains(text, `&amp;`) || !strings.Contains(text, `&lt;`) {
		t.Errorf("text = %q; the entities were decoded. A JSON string holding a "+
			"literal &amp; must survive verbatim, or every seller whose title "+
			"contains an ampersand is silently corrupted", text)
	}
}

func TestScriptTextByPrefix(t *testing.T) {
	body := []byte(`<script>var noise = 1;</script>` +
		`<script>window.__initialData__ = "{}";window.other = 2;</script>`)
	text, ok := scriptTextByPrefix(body, "window.__initialData__")
	if !ok {
		t.Fatal("the prefix-selected block was not found")
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "window.__initialData__") {
		t.Errorf("text = %q, want it to start with the assignment", text)
	}
	if strings.Contains(text, "var noise") {
		t.Error("the wrong script was returned")
	}
}

// TestSubtreeTextTakesNestedText pins the mistake this helper exists to
// prevent: taking "the next N characters after the marker", which returns the
// class attribute rather than the text and is exactly the fragility the
// no-regex rule is about.
func TestSubtreeTextTakesNestedText(t *testing.T) {
	body := []byte(`<div data-marker="priceLabelGrid" class="_71fa93 _8416d9">` +
		`<span><b>49</b>&nbsp;490</span> ₽</div><div>NOT THIS</div>`)
	got := textOfMarker(body, "priceLabelGrid")
	if strings.Contains(got, "_71fa93") {
		t.Fatalf("got %q — the class attribute was returned instead of the text", got)
	}
	if strings.Contains(got, "NOT THIS") {
		t.Fatalf("got %q — the walk ran past the closing tag", got)
	}
	if !strings.Contains(got, "49") || !strings.Contains(got, "490") {
		t.Errorf("got %q, want the nested text of the whole element", got)
	}
}
