// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

func aProxyList(t *testing.T, s *Server, name, list string) int64 {
	t.Helper()
	rec := post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {name}, "list": {list},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /channels = %d: %s", rec.Code, rec.Body.String())
	}
	chs, err := s.Store.Channels(context.Background())
	if err != nil || len(chs) == 0 {
		t.Fatalf("channels = %+v (%v)", chs, err)
	}
	return chs[len(chs)-1].ID
}

// TestAListIsReadAndCountedWithTheFirstBadLineQuoted.
//
// WB_GO's rule about what a free test means, kept: a list is read and counted,
// and the first line that would not parse is shown. «2 адреса, 1 строка не
// разобрана» without saying which line leaves somebody scrolling a file.
func TestAListIsReadAndCountedWithTheFirstBadLineQuoted(t *testing.T) {
	s := newServer(t)
	aProxyList(t, s, "провайдер", "10.0.0.1:1080\nне адрес\n10.0.0.2:1080\n")

	page := get(t, s, "/channels").Body.String()
	if !strings.Contains(page, "2 адреса") {
		t.Errorf("the address count is not shown: %s", page)
	}
	if !strings.Contains(page, "не адрес") {
		t.Error("the bad line is not quoted")
	}
}

// TestTheScreenStatesWhatTheMixMeans.
//
// Not left to be inferred: this is the sentence that answers «я добавил порты, и
// забанило быстрее».
func TestTheScreenStatesWhatTheMixMeans(t *testing.T) {
	s := newServer(t)
	blankTrailWithPorts(t, s, 10)
	aProxyList(t, s, "провайдер", "10.0.0.1:1080\n10.0.0.2:1080\n10.0.0.3:1080\n")

	page := get(t, s, "/channels").Body.String()
	for _, want := range []string{"3 адреса", "отолок", "орты без своих адресов"} {
		if !strings.Contains(page, want) {
			t.Errorf("the screen does not carry %q", want)
		}
	}
}

// TestARotatingChannelsChangeLinkIsNeverPulledByATest.
//
// The provider limits how often it may be pulled, and a test that broke what it
// was testing would be worse than no test at all.
func TestARotatingChannelsChangeLinkIsNeverPulledByATest(t *testing.T) {
	s := newServer(t)
	pulled := false
	s.CheckChannel = func(context.Context, int64) (string, error) {
		pulled = true
		return "", nil
	}
	rec := post(t, s, "/channels", url.Values{
		"kind": {"rotating"}, "name": {"ротация"},
		"rotate_url": {"https://provider.example/change?token=x"},
		"upstream":   {"10.0.0.9:1080"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	chs, err := s.Store.Channels(context.Background())
	if err != nil || len(chs) != 1 {
		t.Fatalf("channels = %+v (%v)", chs, err)
	}
	frag := get(t, s, "/channels/test?id="+strconv.FormatInt(chs[0].ID, 10))
	if pulled {
		t.Error("the change link was pulled by a test")
	}
	if !strings.Contains(frag.Body.String(), "не проверяется") {
		t.Errorf("the fragment does not say why: %s", frag.Body.String())
	}
}

// TestAGatewayIsPickedFromWhatExists, not typed from memory.
func TestAGatewayIsPickedFromWhatExists(t *testing.T) {
	s := newServer(t)
	s.Gateways = func(context.Context) (blanktrail.GatewayList, error) {
		return blanktrail.GatewayList{
			Available: true,
			Gateways:  []blanktrail.Gateway{{Name: "ru-msk"}},
		}, nil
	}
	frag := get(t, s, "/channels/gateways")
	if !strings.Contains(frag.Body.String(), "ru-msk") {
		t.Errorf("the gateway list is not offered: %s", frag.Body.String())
	}
}

// TestAnUnavailableGatewayBackendSaysWhyRatherThanNothing.
//
// Available and an empty list are two different silences: «шлюзы недоступны» is
// something to fix, «шлюзов не настроено» is something to do, and a screen that
// said the same for both would send a person to the wrong place.
func TestAnUnavailableGatewayBackendSaysWhyRatherThanNothing(t *testing.T) {
	s := newServer(t)
	s.Gateways = func(context.Context) (blanktrail.GatewayList, error) {
		return blanktrail.GatewayList{Available: false, Reason: "нет действующей лицензии"}, nil
	}
	if !strings.Contains(get(t, s, "/channels/gateways").Body.String(), "лицензии") {
		t.Error("the reason is not shown")
	}

	s.Gateways = func(context.Context) (blanktrail.GatewayList, error) {
		return blanktrail.GatewayList{Available: true}, nil
	}
	body := get(t, s, "/channels/gateways").Body.String()
	if !strings.Contains(body, "не настроено") {
		t.Errorf("an empty list reads as an outage: %s", body)
	}
}

// TestAChannelCredentialIsNotRenderedBack. A proxy password is a credential
// like any other, and this page is one somebody screenshots.
func TestAChannelCredentialIsNotRenderedBack(t *testing.T) {
	s := newServer(t)
	id := aProxyList(t, s, "с паролем", "socks5://user:s3cr3t@10.0.0.1:1080\n")

	page := get(t, s, "/channels").Body.String()
	if strings.Contains(page, "s3cr3t") {
		t.Error("a proxy password is on the page")
	}
	// It was stored, though — the screen not showing it is not the same as the
	// pool not having it.
	ps, err := s.Store.Proxies(context.Background(), id)
	if err != nil || len(ps) != 1 || ps[0].Password != "s3cr3t" {
		t.Errorf("stored %+v (%v)", ps, err)
	}
}

// TestRefreshingAListReplacesItRatherThanAddingToIt.
func TestRefreshingAListReplacesItRatherThanAddingToIt(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	id := aProxyList(t, s, "провайдер", "10.0.0.1:1080\n10.0.0.2:1080\n")

	post(t, s, "/channels", url.Values{
		"id": {strconv.FormatInt(id, 10)}, "kind": {"proxy-list"},
		"name": {"провайдер"}, "list": {"10.0.0.3:1080\n"},
	})
	ps, err := s.Store.Proxies(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Host != "10.0.0.3" {
		t.Errorf("after the refresh: %+v", ps)
	}
}

// blankTrailWithPorts puts the installation in BlankTrail mode with n ports,
// which is where an address list multiplies the ceiling at all: one port per
// exit at once. Direct mode holds one identity and so one address in use.
func blankTrailWithPorts(t *testing.T, s *Server, n int) {
	t.Helper()
	ctx := context.Background()
	if err := s.Store.SetTransportMode(ctx, transport.ModeBlankTrail); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetBlankTrailConfig(ctx, store.BlankTrailConfig{
		ControlURL: "http://127.0.0.1:8891", Ports: n,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDirectModeUsesOneAddressHoweverLongTheList.
//
// Direct mode holds one identity on purpose — the result set is a function of
// it — so a pasted list of fifteen thousand gives one address in use and the
// rest as spares. The budget used to multiply by all of them.
func TestDirectModeUsesOneAddressHoweverLongTheList(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetTransportMode(context.Background(), transport.ModeDirect); err != nil {
		t.Fatal(err)
	}
	aProxyList(t, s, "провайдер", threeProxies)
	if n, _ := s.addressCount(context.Background()); n != 1 {
		t.Errorf("addresses = %d in direct mode, want 1", n)
	}
}

// TestBlankTrailUsesNoMoreExitsThanPorts. Ten ports over three proxies are
// three addresses, and three ports over a hundred are three.
func TestBlankTrailUsesNoMoreExitsThanPorts(t *testing.T) {
	s := newServer(t)
	blankTrailWithPorts(t, s, 2)
	aProxyList(t, s, "провайдер", threeProxies)
	if n, _ := s.addressCount(context.Background()); n != 2 {
		t.Errorf("addresses = %d with two ports, want 2", n)
	}
}

// TestDeletingAChannelAsksAndThenChangesTheStatedCeiling.
func TestDeletingAChannelAsksAndThenChangesTheStatedCeiling(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	blankTrailWithPorts(t, s, 10)
	id := aProxyList(t, s, "провайдер", "10.0.0.1:1080\n10.0.0.2:1080\n10.0.0.3:1080\n")

	if !strings.Contains(get(t, s, "/channels").Body.String(), "data-confirm") {
		t.Error("the delete button does not ask")
	}
	if n, err := s.addressCount(ctx); err != nil || n != 3 {
		t.Fatalf("addresses = %d (%v), want 3", n, err)
	}
	post(t, s, "/channels/delete", url.Values{"id": {strconv.FormatInt(id, 10)}})

	n, err := s.addressCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("addresses = %d after deleting the only list, want the host's own", n)
	}
}

// TestATestThatFailsSaysWhyWithoutTheCredential.
func TestATestThatFailsSaysWhyWithoutTheCredential(t *testing.T) {
	s := newServer(t)
	id := aProxyList(t, s, "провайдер", "socks5://user:s3cr3t@10.0.0.1:1080\n")

	s.CheckChannel = func(context.Context, int64) (string, error) {
		return "", errors.New("socks5://user:s3cr3t@10.0.0.1:1080 не отвечает")
	}
	body := get(t, s, "/channels/test?id="+strconv.FormatInt(id, 10)).Body.String()
	if strings.Contains(body, "s3cr3t") {
		t.Errorf("a credential leaked through an error: %s", body)
	}
	if !strings.Contains(body, "не отвечает") {
		t.Errorf("the reason was lost with the credential: %s", body)
	}
}

// TestTheHostsOwnAddressIsAChannelLikeAnyOther, so a mix of direct and proxied
// is expressible — which is what «in any mix» in the spec means.
func TestTheHostsOwnAddressIsAChannelLikeAnyOther(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	blankTrailWithPorts(t, s, 10)
	if rec := post(t, s, "/channels", url.Values{
		"kind": {"direct"}, "name": {"эта машина"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	aProxyList(t, s, "провайдер", "10.0.0.1:1080\n10.0.0.2:1080\n")

	n, err := s.addressCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("addresses = %d, want two proxied plus this machine", n)
	}
}

// TestAListOfNothingUsableIsRefusedWithTheFirstLineQuoted.
func TestAListOfNothingUsableIsRefusedWithTheFirstLineQuoted(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"мусор"}, "list": {"совсем не адрес\n"},
	})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("a list with nothing usable was accepted")
	}
	if !strings.Contains(rec.Body.String(), "совсем не адрес") {
		t.Errorf("the refusal does not quote the line: %s", rec.Body.String())
	}
}

// TestAKindThisBuildDoesNotKnowIsRefused, rather than reaching the CHECK.
func TestAKindThisBuildDoesNotKnowIsRefused(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/channels", url.Values{
		"kind": {"carrier-pigeon"}, "name": {"голуби"},
	})
	if rec.Code == http.StatusSeeOther {
		t.Fatal("an unknown kind was accepted")
	}
	chs, _ := s.Store.Channels(context.Background())
	if len(chs) != 0 {
		t.Errorf("it was stored anyway: %+v", chs)
	}
}

// TestARotatingChannelNeedsBothItsHalves.
func TestARotatingChannelNeedsBothItsHalves(t *testing.T) {
	s := newServer(t)
	for _, f := range []url.Values{
		{"kind": {"rotating"}, "name": {"без ссылки"}, "upstream": {"10.0.0.9:1080"}},
		{"kind": {"rotating"}, "name": {"без адреса"},
			"rotate_url": {"https://provider.example/change"}},
	} {
		if rec := post(t, s, "/channels", f); rec.Code == http.StatusSeeOther {
			t.Errorf("%v was accepted", f)
		}
	}
}

// threeProxies is a list of three, one per line.
const threeProxies = "10.0.0.1:1080\n10.0.0.2:1080\n10.0.0.3:1080\n"

// TestAListLinkIsShownWithoutItsKey.
//
// A provider's list link carries the account key in its query
// (…getFile&order=…&key=…). The card showed it whole, so anybody who could open
// the panel, or a screenshot of it, could fetch the list and use the account.
func TestAListLinkIsShownWithoutItsKey(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/channels", url.Values{
		"kind": {"proxy-list"}, "name": {"провайдер"},
		"source": {"https://cabinet.example/api.php?action=getFile&order=42&key=s3cr3tvalue"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /channels = %d: %s", rec.Code, rec.Body.String())
	}
	page := get(t, s, "/channels").Body.String()
	if strings.Contains(page, "s3cr3tvalue") {
		t.Error("the list link is shown with its key")
	}
	if !strings.Contains(page, "action=getFile&amp;order=42&amp;key=***") {
		t.Errorf("the link is not shown with the key masked: %s", page)
	}
}
