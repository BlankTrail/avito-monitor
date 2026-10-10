// SPDX-License-Identifier: AGPL-3.0-or-later

package transport

import (
	"strings"
	"testing"
)

func TestAnExitIsRecordedWithoutItsPassword(t *testing.T) {
	got := PublicEgress("socks5://user--sid-7:s3cr3t@gw.example:8080")
	if strings.Contains(got, "s3cr3t") {
		t.Fatalf("the password survived: %s", got)
	}
	if !strings.Contains(got, "user--sid-7") || !strings.Contains(got, "gw.example:8080") {
		t.Errorf("the session or the address was lost: %s", got)
	}
	for _, keep := range []string{"", "direct", "gateway:NL-1", "socks5://10.0.0.1:1080"} {
		if PublicEgress(keep) != keep {
			t.Errorf("%q changed to %q", keep, PublicEgress(keep))
		}
	}
}
