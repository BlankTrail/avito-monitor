// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// mutateFixture loads a real capture and hands its bytes to fn.
func mutateFixture(t *testing.T, name string, fn func([]byte) []byte) (Page, error) {
	t.Helper()
	doc, m := loadFixture(t, name)
	doc.Body = fn(doc.Body)
	return Extract(doc, Options{Now: m.FrozenNow})
}

// TestMutationRehashEveryClass. Avito ships CSS modules: every class is a stem
// plus a build hash and both change without announcement. An extractor that
// depended on one would return zero cards after an ordinary deploy — and zero
// cards is what a new-listing monitor reports on a quiet market, so the failure
// would be invisible.
func TestMutationRehashEveryClass(t *testing.T) {
	for _, name := range []string{"mobile-search-30", "desktop-canonical-p1"} {
		t.Run(name, func(t *testing.T) {
			before, m := loadFixture(t, name)
			base, err := Extract(before, Options{Now: m.FrozenNow})
			if err != nil {
				t.Fatal(err)
			}
			after, err := mutateFixture(t, name, rehashClasses)
			if err != nil {
				t.Fatalf("rehashing every class broke extraction: %v", err)
			}
			if len(after.Listings) != len(base.Listings) {
				t.Fatalf("listings %d -> %d after rehashing every class; no "+
					"extractor may depend on a CSS class",
					len(base.Listings), len(after.Listings))
			}
			for i := range base.Listings {
				if base.Listings[i].ItemID != after.Listings[i].ItemID {
					t.Fatalf("listing %d changed identity after a rehash", i)
				}
			}
		})
	}
}

// TestMutationRenameScriptType. The type is a deliberate non-MIME so no browser
// executes the block; it is presentation trivia and the likeliest thing to be
// renamed. Selection is on data-mfe-state, so a rename must change nothing but
// the recorded value.
func TestMutationRenameScriptType(t *testing.T) {
	after, err := mutateFixture(t, "desktop-canonical-p1", func(b []byte) []byte {
		return bytes.ReplaceAll(b,
			[]byte(`type="mime/invalid"`), []byte(`type="application/x-renamed"`))
	})
	if err != nil {
		t.Fatalf("renaming the script type broke extraction: %v", err)
	}
	if after.Report.Rule != RuleDesktopCatalog || after.Report.Path != PathState {
		t.Errorf("Rule/Path = %q/%q after a rename; selection must not depend on the type",
			after.Report.Rule, after.Report.Path)
	}
	if len(after.Listings) != 50 {
		t.Errorf("Listings = %d, want 50", len(after.Listings))
	}
	if after.Report.ScriptType != "application/x-renamed" {
		t.Errorf("ScriptType = %q; the new value must be recorded so a drift "+
			"report can say it moved", after.Report.ScriptType)
	}
}

// TestMutationEntityInTitle. x/net/html returns raw bytes for script data. A
// second unescaping pass rewrites a literal &amp; inside a JSON string to &,
// corrupting a seller's title with no error anywhere.
func TestMutationEntityInTitle(t *testing.T) {
	const injected = `Ремонт &amp; отделка &lt;срочно&gt;`
	after, err := mutateFixture(t, "desktop-canonical-p1", func(b []byte) []byte {
		// Anchor on the first urlPath, not on the first title: the earliest
		// "title" in this document is «Показать большие фото», a control's
		// label, and injecting there would test nothing while looking as
		// though it had.
		u := bytes.Index(b, []byte(`"urlPath":"`))
		if u < 0 {
			t.Fatal(`no "urlPath": in the fixture`)
		}
		i := bytes.Index(b[u:], []byte(`"title":"`))
		if i < 0 {
			t.Fatal(`no "title": after the first listing's urlPath`)
		}
		i += u
		start := i + len(`"title":"`)
		end := bytes.IndexByte(b[start:], '"')
		if end < 0 {
			t.Fatal("unterminated title")
		}
		out := make([]byte, 0, len(b))
		out = append(out, b[:start]...)
		out = append(out, injected...)
		out = append(out, b[start+end:]...)
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range after.Listings {
		if l.Title == injected {
			found = true
		}
		if strings.Contains(l.Title, "Ремонт & отделка") {
			t.Fatal("the entity was decoded; every seller whose title contains " +
				"an ampersand is now silently corrupted")
		}
	}
	if !found {
		t.Errorf("the injected title did not survive verbatim")
	}
}

// TestMutationDeleteStateBlock. Avito can serve a desktop page with its state
// removed — loaderData.toggles carries runtime_catalog_drop_state_for_bots. The
// run must come back on the DOM path, with card fidelity, and it must say so.
func TestMutationDeleteStateBlock(t *testing.T) {
	after, err := mutateFixture(t, "desktop-canonical-p1", deleteMFEState)
	if err != nil {
		t.Fatalf("deleting the state block failed the page; it must degrade, not fail: %v", err)
	}
	if after.Report.Path != PathDOM {
		t.Fatalf("Path = %q, want dom", after.Report.Path)
	}
	if !after.Report.HasWarning(WarnDegraded) {
		t.Error("no degradation warning; that transition is the earliest " +
			"available signal that this install has been classified as a bot")
	}
	if len(after.Listings) != 50 {
		t.Errorf("Listings = %d, want 50 — the fallback must keep card fidelity",
			len(after.Listings))
	}
}

// rehashClasses rewrites the value of every class attribute, leaving every tag,
// every other attribute and all text exactly as served.
func rehashClasses(body []byte) []byte {
	var out bytes.Buffer
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out.Bytes()
		}
		raw := z.Raw()
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(raw)
			continue
		}
		s := string(raw)
		i := strings.Index(s, ` class="`)
		if i < 0 {
			out.WriteString(s)
			continue
		}
		start := i + len(` class="`)
		end := strings.IndexByte(s[start:], '"')
		if end < 0 {
			out.WriteString(s)
			continue
		}
		out.WriteString(s[:start])
		out.WriteString("zzz-rehashed-9QwErT")
		out.WriteString(s[start+end:])
	}
}

// deleteMFEState removes the whole data-mfe-state script, leaving the
// server-rendered cards untouched.
func deleteMFEState(body []byte) []byte {
	open := bytes.Index(body, []byte(`data-mfe-state="true"`))
	if open < 0 {
		return body
	}
	tagStart := bytes.LastIndex(body[:open], []byte("<script"))
	closeIdx := bytes.Index(body[open:], []byte("</script>"))
	if tagStart < 0 || closeIdx < 0 {
		return body
	}
	end := open + closeIdx + len("</script>")
	out := make([]byte, 0, len(body))
	out = append(out, body[:tagStart]...)
	out = append(out, body[end:]...)
	return out
}
