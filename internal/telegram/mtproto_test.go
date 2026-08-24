// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

// memSession is a SessionStore that keeps the blob in memory.
type memSession struct{ data []byte }

func (m *memSession) LoadSession(context.Context) ([]byte, error) { return m.data, nil }
func (m *memSession) StoreSession(_ context.Context, d []byte) error {
	m.data = append([]byte(nil), d...)
	return nil
}

// TestCheckFailsClosedWithoutCredentials.
//
// The Bot API rung needs only a token; MTProto's bot login needs the token AND
// an application identity from my.telegram.org. A half-configured rung that
// fails at send time is worse than one that refuses at check time, because the
// ladder would have committed to it.
func TestCheckFailsClosedWithoutCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  MTConfig
	}{
		{"nothing at all", MTConfig{Session: &memSession{}}},
		{"a token but no api id", MTConfig{BotToken: "123:AAH", Session: &memSession{}}},
		{"an api id but no hash", MTConfig{APIID: 1, BotToken: "123:AAH", Session: &memSession{}}},
		{"an api id and hash but no token", MTConfig{APIID: 1, APIHash: "h", Session: &memSession{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewMTProto(c.cfg).Check(context.Background())
			if err == nil {
				t.Fatal("Check passed with incomplete credentials")
			}
			// The message goes on the settings screen, so it must say which
			// field is missing rather than "not configured".
			if !strings.Contains(err.Error(), "api") &&
				!strings.Contains(err.Error(), "token") {
				t.Errorf("err = %q; it does not name what is missing", err)
			}
		})
	}
}

// TestNoCredentialEverReachesAFileThisProgramWrites.
//
// The session is a credential in the strongest sense: anyone holding it is the
// bot. gotd's convenient default is a file beside the binary, which is exactly
// what this must not do — the blob belongs in settings.telegram_mtproto_session,
// a column the panel does not display.
func TestNoCredentialEverReachesAFileThisProgramWrites(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Cleanup(func() { os.Chdir(wd) })

	sess := &memSession{}
	m := NewMTProto(MTConfig{
		APIID: 1, APIHash: "hash", BotToken: "123:AAHsecret", Session: sess,
	})
	// Check will fail — there is no Telegram here — and that is fine. What must
	// not happen is a file appearing.
	_ = m.Check(context.Background())

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("MTProto wrote %q into the working directory", filepath.Join(dir, e.Name()))
	}
}

// TestTheSessionRoundTripsThroughTheStore, so a restart does not re-authorise.
func TestTheSessionRoundTripsThroughTheStore(t *testing.T) {
	sess := &memSession{}
	if err := sess.StoreSession(context.Background(), []byte("blob")); err != nil {
		t.Fatal(err)
	}
	got, err := sess.LoadSession(context.Background())
	if err != nil || string(got) != "blob" {
		t.Errorf("round trip gave %q, %v", got, err)
	}
}

// TestNameIsStableSoTheSettingsScreenCanShowIt.
func TestNameIsStableSoTheSettingsScreenCanShowIt(t *testing.T) {
	if n := NewMTProto(MTConfig{Session: &memSession{}}).Name(); n != "mtproto" {
		t.Errorf("Name() = %q, want mtproto", n)
	}
}

// TestTheChatIdConventionsAreTelegramsOwn.
//
// The address column stores what a person copied out of a bot, so these are
// Telegram's conventions and not ours: a positive id is a user, a -100 prefix
// is a supergroup or channel, and any other negative id is a basic group. Get
// the -100 case wrong and a group's messages go to a channel that does not
// exist — which fails silently enough to be discovered a week later.
func TestTheChatIdConventionsAreTelegramsOwn(t *testing.T) {
	cases := []struct {
		chat string
		want string
		id   int64
	}{
		{"42", "user", 42},
		{"-1001234567890", "channel", 1234567890},
		{"-987654", "chat", 987654},
	}
	for _, c := range cases {
		got, err := inputPeer(c.chat)
		if err != nil {
			t.Fatalf("%s: %v", c.chat, err)
		}
		switch p := got.(type) {
		case *tg.InputPeerUser:
			if c.want != "user" || p.UserID != c.id {
				t.Errorf("%s → user %d, want %s %d", c.chat, p.UserID, c.want, c.id)
			}
		case *tg.InputPeerChannel:
			if c.want != "channel" || p.ChannelID != c.id {
				t.Errorf("%s → channel %d, want %s %d", c.chat, p.ChannelID, c.want, c.id)
			}
		case *tg.InputPeerChat:
			if c.want != "chat" || p.ChatID != c.id {
				t.Errorf("%s → chat %d, want %s %d", c.chat, p.ChatID, c.want, c.id)
			}
		default:
			t.Errorf("%s → %T", c.chat, got)
		}
	}
}

// TestAnAddressThatIsNeitherANumberNorANameIsRefused, rather than being sent to
// Telegram to be refused there — which costs a round trip and reports as a
// network problem.
func TestAnAddressThatIsNeitherANumberNorANameIsRefused(t *testing.T) {
	if _, err := inputPeer("почта@example.com"); err == nil {
		t.Error("an address that is not a chat was accepted")
	}
}

// TestNeitherCredentialAppearsInAnMTProtoError.
//
// gotd quotes request parameters in some of its errors, and the bot token is a
// request parameter of the login call. An error goes to a log, to a screen, and
// sometimes into an issue report.
func TestNeitherCredentialAppearsInAnMTProtoError(t *testing.T) {
	m := NewMTProto(MTConfig{
		APIID: 1, APIHash: "hashhashhash", BotToken: "123456:AAHsecretsecret",
		Session: &memSession{}, Timeout: time.Second,
	})
	err := m.redact(errors.New(
		"rpc: 123456:AAHsecretsecret and hashhashhash were refused"))
	if strings.Contains(err.Error(), "AAHsecret") {
		t.Errorf("the bot token leaked: %v", err)
	}
	if strings.Contains(err.Error(), "hashhashhash") {
		t.Errorf("the api hash leaked: %v", err)
	}
}

// TestARungThatCannotReachTelegramFailsRatherThanHanging.
//
// The ladder is waiting on it and nothing below is being tried, so a rung that
// hangs is worse than one that fails.
func TestARungThatCannotReachTelegramFailsRatherThanHanging(t *testing.T) {
	m := NewMTProto(MTConfig{
		APIID: 1, APIHash: "hash", BotToken: "123:AAH",
		Session: &memSession{}, Timeout: 2 * time.Second,
	})
	done := make(chan error, 1)
	go func() { done <- m.Check(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Check succeeded without Telegram")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Check did not return; the timeout is not bounding the exchange")
	}
}
