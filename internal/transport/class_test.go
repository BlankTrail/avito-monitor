// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlankTrail/avito-monitor/avito"
)

func doc(body string) avito.Document {
	return avito.Document{Surface: avito.SurfaceMobile, Body: []byte(body)}
}

const wallBody = `<html><body><h1>Возникла проблема с IP-адресом</h1></body></html>`

func TestClassify(t *testing.T) {
	full := avito.Page{Kind: avito.EnvelopeCatalog, Listings: []avito.Listing{{ItemID: 1}}}
	zero := int64(0)
	many := int64(59959)
	empty := avito.Page{Kind: avito.EnvelopeCatalog, TotalCount: &zero, EmptyDeclared: true}
	shape := avito.Page{Kind: avito.EnvelopeCatalog, TotalCount: &many}

	cases := []struct {
		name string
		in   Input
		want Class
	}{
		{"a page with listings", Input{Status: 200, Page: full, Doc: doc("x")}, ClassOK},

		// The three 200s that only the parse tells apart.
		{"the site said nothing matched", Input{Status: 200, Page: empty, Doc: doc("x")}, ClassEmpty},
		{"the container said 59959 and we read none",
			Input{Status: 200, Page: shape, Doc: doc("x")}, ClassShape},
		{"no container at all",
			Input{Status: 200, ExtractErr: avito.ErrNoExtractor, Doc: doc("x")}, ClassSoftBlock},

		// 439 is Avito's own challenge status and appears in no RFC.
		{"439", Input{Status: 439, Doc: doc("x")}, ClassChallenge},
		{"a captcha marker at 200", Input{Status: 200,
			Doc: doc(`<html>gcaptcha4</html>`), ExtractErr: avito.ErrNoExtractor}, ClassChallenge},

		// The measured cold baseline. Routing it to ClassChallenge would halt
		// mode (b) permanently before it made a second request.
		{"429 with a wall on an unwarmed identity",
			Input{Status: 429, Doc: doc(wallBody), Warm: false}, ClassCold},
		{"429 with a wall on a warm identity",
			Input{Status: 429, Doc: doc(wallBody), Warm: true}, ClassThrottled},
		{"a bare 429", Input{Status: 429, Doc: doc("x"), Warm: true}, ClassThrottled},

		{"403", Input{Status: 403, Doc: doc("x"), Warm: true}, ClassBurned},
		{"a body that is not UTF-8",
			Input{Status: 200, ExtractErr: avito.ErrNotUTF8, Doc: doc("x")}, ClassMangled},
		{"a 500 is nothing we have a remedy for",
			Input{Status: 500, Doc: doc("x"), Warm: true}, ClassOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.in).Class; got != c.want {
				t.Errorf("Classify() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestColdOnlyAppliesBeforeAWarmUp is the row whose absence kills mode (b).
// The same response means two different things depending on whether this
// identity has ever completed a warm-up, and only the caller knows which.
func TestColdOnlyAppliesBeforeAWarmUp(t *testing.T) {
	in := Input{Status: 429, Doc: doc(wallBody)}
	in.Warm = false
	if got := Classify(in).Class; got != ClassCold {
		t.Errorf("unwarmed: %q, want %q", got, ClassCold)
	}
	in.Warm = true
	if got := Classify(in).Class; got == ClassCold {
		t.Error("a warm identity was classified cold; the row would then never " +
			"stop applying and the ladder would warm up for ever")
	}
}

// TestChallengeOutranksThrottling. A captcha marker at any status is a
// challenge, but a plain wall marker at 429 is not — the measured cold baseline
// carries exactly that, and conflating them halts mode (b) on request one,
// permanently, because halted_reason is never cleared by the program.
func TestChallengeOutranksThrottling(t *testing.T) {
	captchaAt429 := Input{Status: 429, Warm: true,
		Doc: doc(`<html>Продолжить для решения gcaptcha4</html>`)}
	if got := Classify(captchaAt429).Class; got != ClassChallenge {
		t.Errorf("captcha marker at 429 = %q, want %q", got, ClassChallenge)
	}
}

// TestRemedies pins what each class asks for, because the ladder reads this and
// nothing else. Rotation is reachable from exactly one class.
func TestRemedies(t *testing.T) {
	all := []Class{ClassOK, ClassEmpty, ClassShape, ClassSoftBlock, ClassCold,
		ClassThrottled, ClassBurned, ClassChallenge, ClassMangled, ClassOther}
	rotates := 0
	for _, c := range all {
		if c.Remedy() == RemedyRotateEgress {
			rotates++
			if c != ClassThrottled {
				t.Errorf("%q asks to rotate the egress; only ClassThrottled may, "+
					"because rotation destroys a clearance worth 19-23 seconds", c)
			}
		}
	}
	if rotates != 1 {
		t.Errorf("%d classes rotate the egress, want exactly 1", rotates)
	}
	if ClassShape.Remedy() != RemedyNone {
		t.Errorf("ClassShape.Remedy() = %v; repeating a request whose payload we "+
			"have stopped understanding spends the budget on our own bug",
			ClassShape.Remedy())
	}
	if ClassEmpty.Remedy() != RemedyNone {
		t.Errorf("ClassEmpty.Remedy() = %v; it is the site's answer, not a fault",
			ClassEmpty.Remedy())
	}
	// Measured: a 403 heals with time. An arm whose RenewIdentity was a no-op
	// recovered from four consecutive 403s on one unchanged srv_id, and an arm
	// that did renew was no faster — recovery tracked the waits and nothing
	// else. Eighteen of eighteen cleared by waiting; none failed to. Renewing
	// discards a clearance worth 19-23 s to fix what the identity did not cause.
	if ClassBurned.Remedy() != RemedyWait {
		t.Errorf("ClassBurned.Remedy() = %v, want RemedyWait", ClassBurned.Remedy())
	}
	// NOT measured. A 200 with no container is a different signal from a 403
	// and keeps its remedy until an arm separates them the way direct-norenew
	// separated the 403.
	if ClassSoftBlock.Remedy() != RemedyRenewIdentity {
		t.Errorf("ClassSoftBlock.Remedy() = %v, want RemedyRenewIdentity",
			ClassSoftBlock.Remedy())
	}
	// Rotation stays reachable from exactly one class. The 429 is what a
	// different address fixes; nothing else is.
	for _, c := range []Class{
		ClassOK, ClassEmpty, ClassShape, ClassBurned, ClassSoftBlock,
		ClassCold, ClassChallenge, ClassMangled, ClassOther,
	} {
		if c.Remedy() == RemedyRotateEgress {
			t.Errorf("%q.Remedy() = RemedyRotateEgress; only a 429 is fixed by "+
				"a different address", c)
		}
	}
	if ClassThrottled.Remedy() != RemedyRotateEgress {
		t.Errorf("ClassThrottled.Remedy() = %v, want RemedyRotateEgress",
			ClassThrottled.Remedy())
	}
}

// TestClassifyRealCaptures runs the classifier over increment 1's fixtures. A
// classifier fed only what its author imagined agrees with its author.
func TestClassifyRealCaptures(t *testing.T) {
	cases := []struct {
		fixture string
		surface avito.Surface
		want    Class
	}{
		{"desktop-canonical-p1", avito.SurfaceDesktop, ClassOK},
		{"desktop-seller", avito.SurfaceDesktop, ClassOK},
		{"desktop-spa-shell", avito.SurfaceDesktop, ClassOK}, // a redirect is an answer
		{"desktop-item", avito.SurfaceDesktop, ClassOK},
		{"mobile-search-30", avito.SurfaceMobile, ClassOK},
		{"mobile-search-empty", avito.SurfaceMobile, ClassEmpty},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			d := avito.Document{Surface: c.surface, Body: readFixture(t, c.fixture)}
			page, err := avito.Extract(d, avito.Options{})
			got := Classify(Input{Status: 200, Doc: d, Page: page, ExtractErr: err, Warm: true})
			if got.Class != c.want {
				t.Errorf("Class = %q, want %q (detail: %s)", got.Class, c.want, got.Detail)
			}
		})
	}
}

// readFixture reads increment 1's captures from the avito package's testdata.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "avito", "testdata", name+".html.gz"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// readLocalFixture reads a capture this package keeps for itself, as opposed to
// increment 1's extraction fixtures.
func readLocalFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".html.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestALive429IsAChallenge is the regression for the most expensive
// misclassification this design has made.
//
// The body is a real 429 captured from m.avito.ru. It carries three markers at
// once — "проблема с ip", "geetest" and "firewallCaptcha" — and Document.Wall
// returns them in table order, so the IP one comes back. The classifier used to
// test that single label for captcha-ness, find none, and file a page whose
// whole purpose was to present a Geetest slider as a rate limit.
//
// The cost of that was not academic. A rate limit is answered by waiting, so
// the ladder waited: sixty-six consecutive 429s, eleven fetches, about fifteen
// minutes each, none of which cleared, because no amount of waiting solves a
// captcha.
func TestALive429IsAChallenge(t *testing.T) {
	body := readLocalFixture(t, "live-429-captcha")

	doc := avito.Document{
		Surface: avito.SurfaceMobile,
		URL:     "https://m.avito.ru/all?q=iphone+15&s=104",
		Body:    body,
	}

	// The premise: the page really does carry both markers.
	labels := map[string]bool{}
	for _, w := range doc.Walls() {
		labels[w.Label] = true
	}
	if !labels[avito.LabelIPBlock] || !labels[avito.LabelGeetest] {
		t.Fatalf("fixture no longer carries both markers: %v — this test is "+
			"about a page that is two things at once", labels)
	}
	if first, ok := doc.Wall(); !ok || first.Label != avito.LabelIPBlock {
		t.Fatalf("Wall() = %+v; the trap is that the IP marker is returned "+
			"first and it is not the captcha one", first)
	}

	page, extractErr := avito.Extract(doc, avito.Options{Now: 0})
	v := Classify(Input{Status: 429, Doc: doc, Page: page, ExtractErr: extractErr, Warm: true})

	if v.Class != ClassChallenge {
		t.Fatalf("class = %q, want %q — this is a Geetest page, and waiting "+
			"does not solve a captcha", v.Class, ClassChallenge)
	}
	if v.Class.Remedy() != RemedyRetrySame {
		t.Errorf("remedy = %v; a challenge is repeated on the same identity "+
			"where there is a solver, and halted where there is not",
			v.Class.Remedy())
	}
}

// TestAColdRefusalStillWarmsUpEvenIfItLooksLikeACaptcha. The captcha scan runs
// after the cold check on purpose: an identity that has never warmed up is
// expected to be refused, and reading that as a challenge would leave direct
// mode unable to start at all.
func TestAColdRefusalStillWarmsUpEvenIfItLooksLikeACaptcha(t *testing.T) {
	body := readLocalFixture(t, "live-429-captcha")
	doc := avito.Document{Surface: avito.SurfaceMobile, URL: "https://m.avito.ru/all?q=x", Body: body}
	page, extractErr := avito.Extract(doc, avito.Options{Now: 0})

	v := Classify(Input{Status: 429, Doc: doc, Page: page, ExtractErr: extractErr, Warm: false})
	if v.Class != ClassCold {
		t.Errorf("class = %q on an unwarmed identity, want %q — the warm-up is "+
			"the first thing to try, and it is a root navigation that may "+
			"clear this by itself", v.Class, ClassCold)
	}
}
