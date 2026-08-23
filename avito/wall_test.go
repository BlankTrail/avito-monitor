// SPDX-License-Identifier: AGPL-3.0-or-later

package avito

import "testing"

func TestDocumentWall(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantLabel string
		wantHit   bool
	}{
		{"the measured IP block", "<h1>Возникла проблема с IP-адресом</h1>", LabelIPBlock, true},
		{"the hard block", "Попробуйте подключиться к другой сети", LabelHardIPBlock, true},
		{"the slider", "Продолжить для решения", LabelSlider, true},
		{"geetest by vendor name", "<script src='/gcaptcha4.js'>", LabelGeetest, true},
		{"access restricted", "Доступ ограничен", LabelRestricted, true},
		{"the WAF names itself", "powered by QRATOR", LabelWAF, true},
		{"rate limit", "Слишком много запросов", LabelRateLimit, true},
		{"an ordinary results page is not a wall", "<div>Телефоны в Москве</div>", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hit, ok := Document{Body: []byte(c.body)}.Wall()
			if ok != c.wantHit {
				t.Fatalf("Wall() ok = %v, want %v", ok, c.wantHit)
			}
			if ok && hit.Label != c.wantLabel {
				t.Errorf("Wall() label = %q, want %q", hit.Label, c.wantLabel)
			}
		})
	}
}

// TestWallMarkersAreCaseInsensitive pins the property that cost the Python
// tester a debugging session by its absence: markers are matched against a
// body whose case the site does not guarantee.
func TestWallMarkersAreCaseInsensitive(t *testing.T) {
	if _, ok := (Document{Body: []byte("PROBLEMA... Проблема С IP")}).Wall(); !ok {
		t.Error("a marker differing only in case was missed")
	}
}
