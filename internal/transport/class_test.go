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
	for _, c := range []Class{ClassBurned, ClassSoftBlock} {
		if c.Remedy() != RemedyRenewIdentity {
			t.Errorf("%q.Remedy() = %v, want RemedyRenewIdentity", c, c.Remedy())
		}
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
