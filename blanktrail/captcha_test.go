// SPDX-License-Identifier: AGPL-3.0-or-later

package blanktrail

import (
	"encoding/json"
	"testing"
)

// TestPortSpecCarriesCaptchaPolicy pins the one extension this project makes to
// the vendored SDK. A live port reports both controls; without them every port
// opened here inherits whatever default the proxy happens to hold, which is a
// policy decision made where the caller cannot see it.
func TestPortSpecCarriesCaptchaPolicy(t *testing.T) {
	spec := DefaultPortSpec()
	spec.CaptchaAction = "rotate_retry"
	spec.CaptchaMaxAttempts = 3

	body, err := json.Marshal(spec.request(20001, Egress{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["captcha_action"] != "rotate_retry" {
		t.Errorf("captcha_action = %v, want rotate_retry", got["captcha_action"])
	}
	if got["captcha_max_attempts"] != float64(3) {
		t.Errorf("captcha_max_attempts = %v, want 3", got["captcha_max_attempts"])
	}
}

// TestPortSpecOmitsUnsetCaptchaPolicy keeps the extension additive. A caller
// that never touches these fields must send exactly what it sent before, or
// vendoring this package into a second project silently changes the first.
func TestPortSpecOmitsUnsetCaptchaPolicy(t *testing.T) {
	body, err := json.Marshal(DefaultPortSpec().request(20001, Egress{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"captcha_action", "captcha_max_attempts"} {
		if _, present := got[k]; present {
			t.Errorf("%s was sent although it was never set; the extension must "+
				"be additive", k)
		}
	}
}
