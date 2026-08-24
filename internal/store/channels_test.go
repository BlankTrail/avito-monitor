// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"
	"testing"
)

func aList(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	id, err := s.SaveChannel(context.Background(), Channel{
		Name: name, Kind: "proxy-list", Source: "proxies.txt",
		Weight: 100, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestReplacingAListIsAtomicInBothDirections.
//
// A refreshed list is the whole list, not a merge: an address the provider
// removed must stop being used, and the only way to say that is to replace the
// set. Doing it in one transaction is what keeps a failed refresh from leaving
// the channel with no addresses at all — which reads to the pool as "this
// channel is empty" and silently drops it out of the mix.
func TestReplacingAListIsAtomicInBothDirections(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "провайдер")

	first := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("stored %d, want 2", len(got))
	}

	// The provider dropped .1 and added .3.
	second := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.3", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, second); err != nil {
		t.Fatal(err)
	}
	got, err = s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("after the refresh: %d, want 2", len(got))
	}
	for _, p := range got {
		if p.Host == "10.0.0.1" {
			t.Error("an address the provider removed is still stored")
		}
	}
}

// TestARefreshThatFailsHalfwayChangesNothing.
//
// The second entry has a scheme the CHECK constraint refuses. Without one
// transaction the first would land, the second would fail, and the channel
// would be left holding one address out of two — a quiet halving of the budget.
func TestARefreshThatFailsHalfwayChangesNothing(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "провайдер")

	good := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, good); err != nil {
		t.Fatal(err)
	}

	bad := []Proxy{
		{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.9", Port: 1080, Enabled: true},
		{ChannelID: ch, Scheme: "carrier-pigeon", Host: "10.0.0.8", Port: 1080, Enabled: true},
	}
	if err := s.ReplaceProxies(ctx, ch, bad); err == nil {
		t.Fatal("a scheme the schema refuses was accepted")
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != "10.0.0.1" {
		t.Errorf("a failed refresh changed the stored list: %+v", got)
	}
}

// TestDeletingAChannelTakesItsAddressesWithIt, which the ON DELETE CASCADE in
// migration 0001 is there for; this asserts it is actually in force, because
// SQLite enforces foreign keys only when the pragma is on.
func TestDeletingAChannelTakesItsAddressesWithIt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "уйдёт")
	if err := s.ReplaceProxies(ctx, ch, []Proxy{
		{ChannelID: ch, Scheme: "http", Host: "10.0.0.1", Port: 8080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM proxies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d orphaned addresses; the cascade is not in force", n)
	}
}

// TestTheSameAddressTwiceInOneListIsStoredOnce.
//
// A provider's file repeats. The UNIQUE constraint is what makes the address
// count on the screen mean "how many addresses", which is the number the budget
// arithmetic multiplies — a duplicate counted twice doubles a person's stated
// ceiling and earns them a ban.
func TestTheSameAddressTwiceInOneListIsStoredOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "с повторами")

	dup := Proxy{ChannelID: ch, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true}
	if err := s.ReplaceProxies(ctx, ch, []Proxy{dup, dup}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Proxies(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("stored %d copies of one address", len(got))
	}
}

// TestCountProxiesAnswersForEveryChannelAtOnce. The screen asks it of every row
// it draws, and one query per row is one query per row.
func TestCountProxiesAnswersForEveryChannelAtOnce(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	a, b := aList(t, s, "первый"), aList(t, s, "второй")
	if err := s.ReplaceProxies(ctx, a, []Proxy{
		{ChannelID: a, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
		{ChannelID: a, Scheme: "socks5", Host: "10.0.0.2", Port: 1080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	counts, err := s.CountProxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[a] != 2 {
		t.Errorf("channel %d counted %d, want 2", a, counts[a])
	}
	if _, ok := counts[b]; ok && counts[b] != 0 {
		t.Errorf("channel %d counted %d, want nothing", b, counts[b])
	}
}

// TestSaveChannelUpdatesRatherThanDuplicating, so editing a channel's weight
// does not orphan the addresses hanging off its id.
func TestSaveChannelUpdatesRatherThanDuplicating(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id := aList(t, s, "провайдер")
	if err := s.ReplaceProxies(ctx, id, []Proxy{
		{ChannelID: id, Scheme: "socks5", Host: "10.0.0.1", Port: 1080, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	again, err := s.SaveChannel(ctx, Channel{
		ID: id, Name: "переименован", Kind: "proxy-list", Source: "proxies.txt",
		Weight: 50, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("the edit took a new id %d, orphaning %d's addresses", again, id)
	}
	chs, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 1 || chs[0].Name != "переименован" || chs[0].Weight != 50 {
		t.Errorf("channels = %+v", chs)
	}
	ps, err := s.Proxies(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Errorf("the edit lost %d addresses", 1-len(ps))
	}
}

// TestACredentialIsNotPutIntoAnError.
//
// A refresh that fails names the address so somebody can find the line, and
// stops there: the password is on the same struct, and an error goes to a log,
// a screen and sometimes an issue report.
func TestACredentialIsNotPutIntoAnError(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	ch := aList(t, s, "провайдер")

	err := s.ReplaceProxies(ctx, ch, []Proxy{
		{ChannelID: ch, Scheme: "carrier-pigeon", Host: "10.0.0.1", Port: 1080,
			Username: "user", Password: "s3cr3t", Enabled: true},
	})
	if err == nil {
		t.Fatal("a scheme the schema refuses was accepted")
	}
	if got := err.Error(); !strings.Contains(got, "10.0.0.1") {
		t.Errorf("err = %q; it does not name the address", got)
	}
	if got := err.Error(); strings.Contains(got, "s3cr3t") {
		t.Errorf("a password leaked into an error: %q", got)
	}
}
