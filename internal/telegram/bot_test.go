// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BlankTrail/avito-monitor/internal/notify"
)

const testToken = "123456:AAHsecretsecretsecret"

func reply(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// TestTheTokenNeverAppearsInAnError.
//
// The token is in the URL of every Bot API call, so any error that quotes the
// request quotes the token — into a log, a panel screen, or a bug report a
// person pastes into an issue. The redaction is lifted from WB_GO and tested
// rather than assumed.
func TestTheTokenNeverAppearsInAnError(t *testing.T) {
	cases := []struct {
		name string
		do   func(*http.Request) (*http.Response, error)
	}{
		{"a transport failure quoting the url", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("Post " + r.URL.String() + ": dial tcp: refused")
		}},
		{"an api error", func(*http.Request) (*http.Response, error) {
			return reply(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`), nil
		}},
		{"a body that is not json", func(*http.Request) (*http.Response, error) {
			return reply(200, "<html>proxy error</html>"), nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewBot(testToken, c.do).SendMessage(context.Background(), "42", "привет")
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testToken) ||
				strings.Contains(err.Error(), "AAHsecret") {
				t.Fatalf("the token leaked: %v", err)
			}
		})
	}
}

// TestAnApiErrorSaysWhatTelegramSaid. "400 Bad Request" alone sends somebody to
// read our source; Telegram's own description is what they need.
func TestAnApiErrorSaysWhatTelegramSaid(t *testing.T) {
	err := NewBot(testToken, func(*http.Request) (*http.Response, error) {
		return reply(400, `{"ok":false,"error_code":400,"description":"chat not found"}`), nil
	}).SendMessage(context.Background(), "42", "привет")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("err = %v; it must carry Telegram's own description", err)
	}
}

// TestCheckUsesGetMe, which is the only call that proves the token works
// without sending anything to anybody.
func TestCheckUsesGetMe(t *testing.T) {
	var path string
	b := NewBot(testToken, func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		return reply(200, `{"ok":true,"result":{"username":"bot"}}`), nil
	})
	if err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/getMe") {
		t.Errorf("Check called %q, want getMe", path)
	}
}

// stubSender is a rung that answers as told.
type stubSender struct {
	name   string
	check  error
	send   error
	sends  int
	checks int
}

func (s *stubSender) Name() string                { return s.name }
func (s *stubSender) Check(context.Context) error { s.checks++; return s.check }
func (s *stubSender) SendMessage(context.Context, string, string) error {
	s.sends++
	return s.send
}

// TestTheLadderRemembersWhatWorked.
//
// A live check costs a round trip, and paying it before every notification
// would spend thirty an hour on a question whose answer changes about never.
func TestTheLadderRemembersWhatWorked(t *testing.T) {
	first := &stubSender{name: "bot-api"}
	l := &Ladder{Rungs: []Sender{first}}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := l.SendMessage(ctx, "42", "привет"); err != nil {
			t.Fatal(err)
		}
	}
	if first.checks != 1 {
		t.Errorf("checked %d times for five messages, want 1", first.checks)
	}
	if first.sends != 5 {
		t.Errorf("sent %d times, want 5", first.sends)
	}
	if l.Current() != "bot-api" {
		t.Errorf("Current() = %q; the settings screen shows it", l.Current())
	}
}

// TestTheLadderFallsThroughToTheNextRung.
func TestTheLadderFallsThroughToTheNextRung(t *testing.T) {
	blocked := &stubSender{name: "bot-api", check: errors.New("blocked")}
	working := &stubSender{name: "mtproto"}
	l := &Ladder{Rungs: []Sender{blocked, working}}

	if err := l.SendMessage(context.Background(), "42", "привет"); err != nil {
		t.Fatal(err)
	}
	if working.sends != 1 {
		t.Error("the second rung was not used")
	}
	if l.Current() != "mtproto" {
		t.Errorf("Current() = %q, want mtproto", l.Current())
	}
}

// TestTheLadderReChecksAfterAFailure, so a rung that stops working does not pin
// the product to it.
func TestTheLadderReChecksAfterAFailure(t *testing.T) {
	flaky := &stubSender{name: "bot-api"}
	spare := &stubSender{name: "mtproto"}
	l := &Ladder{Rungs: []Sender{flaky, spare}}
	ctx := context.Background()

	if err := l.SendMessage(ctx, "42", "первое"); err != nil {
		t.Fatal(err)
	}
	flaky.send = errors.New("now blocked")
	if err := l.SendMessage(ctx, "42", "второе"); err != nil {
		t.Fatal(err)
	}
	if spare.sends != 1 {
		t.Error("a failure on the remembered rung did not fall through")
	}
}

// TestALadderWithNoWorkingRungSaysSoOnce. An error naming every rung and what
// each said is what a person needs; "could not send" is not.
func TestALadderWithNoWorkingRungSaysSoOnce(t *testing.T) {
	a := &stubSender{name: "bot-api", check: errors.New("api.telegram.org blocked")}
	b := &stubSender{name: "mtproto", check: errors.New("no api id configured")}
	err := (&Ladder{Rungs: []Sender{a, b}}).SendMessage(context.Background(), "42", "привет")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"bot-api", "blocked", "mtproto", "api id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q; it does not mention %q", err, want)
		}
	}
}

// TestRedactionDoesNotSwallowTheClassification.
//
// Every error this package returns goes through redact, so if the wrapper did
// not unwrap, notify.ErrPermanent would become invisible and a chat that no
// longer exists would be retried for a day and a half — delaying every message
// behind it. This is the one place two mechanisms cross.
func TestRedactionDoesNotSwallowTheClassification(t *testing.T) {
	err := NewBot(testToken, func(*http.Request) (*http.Response, error) {
		return reply(400, `{"ok":false,"error_code":400,"description":"chat not found"}`), nil
	}).SendMessage(context.Background(), "42", "привет")
	if !errors.Is(err, notify.ErrPermanent) {
		t.Errorf("err = %v; the permanent classification did not survive redaction", err)
	}
}

// TestWhichRefusalsAreForever.
//
// A message retried unnecessarily arrives late; a message abandoned wrongly
// never arrives. So the list is short and everything else is temporary — and
// 429 is deliberately not on it, being the one 4xx that means "later" rather
// than "never".
func TestWhichRefusalsAreForever(t *testing.T) {
	cases := []struct {
		code      int
		permanent bool
	}{
		{400, true}, {401, true}, {403, true}, {404, true},
		{429, false}, {500, false}, {502, false},
	}
	for _, c := range cases {
		code := c.code
		err := NewBot(testToken, func(*http.Request) (*http.Response, error) {
			return reply(code, fmt.Sprintf(
				`{"ok":false,"error_code":%d,"description":"nope"}`, code)), nil
		}).SendMessage(context.Background(), "42", "привет")
		if got := errors.Is(err, notify.ErrPermanent); got != c.permanent {
			t.Errorf("%d: permanent = %v, want %v", code, got, c.permanent)
		}
	}
}

// TestABotWithNoTokenSaysSoWithoutDialling. A first run that has not been
// configured must not spend a round trip to learn what it already knows.
func TestABotWithNoTokenSaysSoWithoutDialling(t *testing.T) {
	dialled := false
	err := NewBot("", func(*http.Request) (*http.Response, error) {
		dialled = true
		return reply(200, `{"ok":true}`), nil
	}).SendMessage(context.Background(), "42", "привет")
	if !errors.Is(err, ErrNoToken) {
		t.Errorf("err = %v, want ErrNoToken", err)
	}
	if dialled {
		t.Error("an unconfigured bot made a request")
	}
}

// TestAMessageIsSentAsPlainText.
//
// Markdown and HTML both make Telegram refuse a message whose body contains an
// unbalanced bracket or a stray underscore, and titles on this site contain
// both. A notification that fails to arrive because of punctuation in a title
// is the worst possible trade for bold text.
func TestAMessageIsSentAsPlainText(t *testing.T) {
	var body string
	b := NewBot(testToken, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		return reply(200, `{"ok":true}`), nil
	})
	title := "iPhone 15 (128 ГБ) *почти новый* _торг_"
	if err := b.SendMessage(context.Background(), "42", title); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "parse_mode") {
		t.Errorf("the request set parse_mode: %s", body)
	}
	form, err := url.ParseQuery(body)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("text") != title {
		t.Errorf("text = %q, want the title unchanged", form.Get("text"))
	}
	if form.Get("chat_id") != "42" {
		t.Errorf("chat_id = %q", form.Get("chat_id"))
	}
}

// TestALongMessageIsShortenedRatherThanRefused.
//
// Telegram refuses one over the limit outright rather than shortening it, so
// the choice is between most of the notification and none of it. The marker is
// there because without it a truncated notification listing forty listings
// looks exactly like one listing five.
func TestALongMessageIsShortenedRatherThanRefused(t *testing.T) {
	long := strings.Repeat("объявление подешевело. ", 400)
	got := truncate(long)
	if n := utf8.RuneCountInString(got); n > maxMessage {
		t.Errorf("truncate left %d runes, over the limit of %d", n, maxMessage)
	}
	if !strings.Contains(got, "обрезано") {
		t.Error("a shortened message does not say it was shortened")
	}
	short := "коротко"
	if truncate(short) != short {
		t.Error("a short message was touched")
	}
}

// TestTruncationCountsRunesAndNotBytes. Every message this product sends is in
// Russian, so a byte-counting limit would cut them at half their allowance —
// and could split a character in two.
func TestTruncationCountsRunesAndNotBytes(t *testing.T) {
	text := strings.Repeat("щ", maxMessage) // one rune, two bytes each
	if got := truncate(text); got != text {
		t.Errorf("a message of exactly %d runes was truncated at %d",
			maxMessage, utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(truncate(strings.Repeat("щ", maxMessage+50))) {
		t.Error("truncation split a character")
	}
}

// TestTheLadderSaysNothingIsConfigured. An empty ladder is a real state on a
// first run, and "index out of range" is not an answer.
func TestTheLadderSaysNothingIsConfigured(t *testing.T) {
	err := (&Ladder{}).SendMessage(context.Background(), "42", "привет")
	if err == nil {
		t.Fatal("an empty ladder claimed to have sent something")
	}
	if !strings.Contains(err.Error(), "не настроен") {
		t.Errorf("err = %v", err)
	}
}

// TestARungThatChecksButThenRefusesIsNotRemembered.
//
// Check passing and SendMessage failing is the shape of a bot that is alive but
// removed from the chat. Remembering it would pin the product to a rung that
// answers every message with a refusal.
func TestARungThatChecksButThenRefusesIsNotRemembered(t *testing.T) {
	broken := &stubSender{name: "bot-api", send: errors.New("bot was kicked")}
	working := &stubSender{name: "mtproto"}
	l := &Ladder{Rungs: []Sender{broken, working}}

	if err := l.SendMessage(context.Background(), "42", "привет"); err != nil {
		t.Fatal(err)
	}
	if l.Current() != "mtproto" {
		t.Errorf("Current() = %q, want mtproto", l.Current())
	}
	if working.sends != 1 {
		t.Error("the second rung was not used")
	}
}
