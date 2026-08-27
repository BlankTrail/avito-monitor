// SPDX-License-Identifier: AGPL-3.0-or-later

package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BlankTrail/avito-monitor/internal/notify"
)

// fakeAPI answers Bot API calls from a script and records the forms it was
// sent, so a test can assert what actually went over the wire.
type fakeAPI struct {
	replies map[string]string
	seen    map[string]url.Values
	calls   []string
}

func newFakeAPI(replies map[string]string) *fakeAPI {
	return &fakeAPI{replies: replies, seen: map[string]url.Values{}}
}

func (f *fakeAPI) do(req *http.Request) (*http.Response, error) {
	method := req.URL.Path[strings.LastIndexByte(req.URL.Path, '/')+1:]
	f.calls = append(f.calls, method)

	raw, _ := io.ReadAll(req.Body)
	if form, err := url.ParseQuery(string(raw)); err == nil {
		f.seen[method] = form
	}
	body, ok := f.replies[method]
	if !ok {
		body = `{"ok":true,"result":true}`
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (f *fakeAPI) bot() *Bot { return NewBot("токен", f.do).At("https://api.test") }

func allowing(ids ...int64) func(context.Context) (map[int64]bool, error) {
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(context.Context) (map[int64]bool, error) { return set, nil }
}

// offsetStore is the persisted watermark, in memory for a test.
type offsetStore struct{ at int64 }

func (o *offsetStore) get(context.Context) (int64, error)   { return o.at, nil }
func (o *offsetStore) set(_ context.Context, v int64) error { o.at = v; return nil }

func callbackUpdate(id int64, chat int64, data string) string {
	return `{"update_id":` + itoa(id) + `,"callback_query":{"id":"cb` + itoa(id) +
		`","data":"` + data + `","message":{"chat":{"id":` + itoa(chat) + `}}}}`
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestAMessageCarriesItsButton.
//
// The spec asks for «Это не перепубликация» on the message itself, so a person
// can correct the product from where they read it rather than having to find
// the panel.
func TestAMessageCarriesItsButton(t *testing.T) {
	f := newFakeAPI(nil)
	a := Attachment{ButtonText: "Это не перепубликация", Callback: NotARepost(200, 100)}

	if err := f.bot().Send(context.Background(), notifyMessage("100500",
		"объявление выложено заново", a.Encode())); err != nil {
		t.Fatal(err)
	}
	form, ok := f.seen["sendMessage"]
	if !ok {
		t.Fatalf("calls = %v", f.calls)
	}
	markup := form.Get("reply_markup")
	if !strings.Contains(markup, "Это не перепубликация") {
		t.Errorf("reply_markup = %q", markup)
	}
	if !strings.Contains(markup, "norepost:200:100") {
		t.Errorf("the callback payload is missing: %q", markup)
	}
}

// TestAMessageWithNoAttachmentIsPlain, which is every message but one kind.
func TestAMessageWithNoAttachmentIsPlain(t *testing.T) {
	f := newFakeAPI(nil)
	if err := f.bot().Send(context.Background(),
		notifyMessage("100500", "новое объявление", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.seen["sendMessage"].Get("reply_markup"); got != "" {
		t.Errorf("a plain message carried markup: %q", got)
	}
}

// TestAnAttachmentFromAnotherReleaseIsIgnoredAndTheMessageStillGoes.
//
// A notification that did not arrive because a button could not be drawn is the
// worst possible trade, and it is the same reasoning that keeps these messages
// plain text.
func TestAnAttachmentFromAnotherReleaseIsIgnoredAndTheMessageStillGoes(t *testing.T) {
	f := newFakeAPI(nil)
	if err := f.bot().Send(context.Background(),
		notifyMessage("100500", "текст", `{"какая-то":"будущая форма"}`)); err != nil {
		t.Fatal(err)
	}
	if f.seen["sendMessage"].Get("text") != "текст" {
		t.Error("the message did not go out")
	}
	if got := f.seen["sendMessage"].Get("reply_markup"); got != "" {
		t.Errorf("an unreadable attachment produced markup: %q", got)
	}
}

// TestTheCallbackRoundTrips, so a press names the same pair the message did.
func TestTheCallbackRoundTrips(t *testing.T) {
	successor, predecessor, ok := ParseNotARepost(NotARepost(7800000001, 7700000002))
	if !ok || successor != 7800000001 || predecessor != 7700000002 {
		t.Errorf("= %d, %d, %v", successor, predecessor, ok)
	}
	for _, bad := range []string{
		"", "norepost", "norepost:1", "norepost:1:2:3",
		"somethingelse:1:2", "norepost:a:2", "norepost:0:2", "norepost:1:-2",
	} {
		if _, _, ok := ParseNotARepost(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestAPressFromAConfiguredChatActs.
func TestAPressFromAConfiguredChatActs(t *testing.T) {
	f := newFakeAPI(map[string]string{
		"getUpdates": `{"ok":true,"result":[` +
			callbackUpdate(11, 100500, "norepost:200:100") + `]}`,
	})
	var gotSuccessor, gotPredecessor int64
	off := &offsetStore{}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get, Save: off.set,
		Handle: func(_ context.Context, s, pr int64) (string, error) {
			gotSuccessor, gotPredecessor = s, pr
			return "Связь снята.", nil
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotSuccessor != 200 || gotPredecessor != 100 {
		t.Errorf("handled (%d, %d)", gotSuccessor, gotPredecessor)
	}
	if f.seen["answerCallbackQuery"].Get("text") != "Связь снята." {
		t.Errorf("the toast said %q", f.seen["answerCallbackQuery"].Get("text"))
	}
	// The watermark advances past what was handled, or a restart replays it.
	if off.at != 12 {
		t.Errorf("offset = %d, want 12", off.at)
	}
}

// TestAPressFromAnUnconfiguredChatDoesNothing.
//
// The whole of the authentication. Telegram states which chat a callback came
// from, and this build acts only on chats that are configured recipients — so a
// stranger who finds the bot's username can make it answer politely and nothing
// else.
func TestAPressFromAnUnconfiguredChatDoesNothing(t *testing.T) {
	f := newFakeAPI(map[string]string{
		"getUpdates": `{"ok":true,"result":[` +
			callbackUpdate(11, 999, "norepost:200:100") + `]}`,
	})
	acted := false
	off := &offsetStore{}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get, Save: off.set,
		Handle: func(context.Context, int64, int64) (string, error) {
			acted = true
			return "", nil
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if acted {
		t.Error("a press from an unconfigured chat was acted on")
	}
	// Answered all the same: an unacknowledged update comes back on every poll.
	if _, ok := f.seen["answerCallbackQuery"]; !ok {
		t.Error("the press was not acknowledged")
	}
	if off.at != 12 {
		t.Errorf("offset = %d; an ignored update must still advance it", off.at)
	}
}

// TestAPayloadThisBuildDoesNotUnderstandIsAcknowledged.
//
// Telegram keeps callback data for as long as the message exists, which on a
// chat somebody scrolls back through is for ever. An unanswered update comes
// back on every poll.
func TestAPayloadThisBuildDoesNotUnderstandIsAcknowledged(t *testing.T) {
	f := newFakeAPI(map[string]string{
		"getUpdates": `{"ok":true,"result":[` +
			callbackUpdate(11, 100500, "somethingelse:1:2") + `]}`,
	})
	off := &offsetStore{}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get, Save: off.set,
		Handle: func(context.Context, int64, int64) (string, error) {
			t.Error("an unknown payload reached the handler")
			return "", nil
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.seen["answerCallbackQuery"]; !ok {
		t.Error("the press was not acknowledged")
	}
}

// TestTheWatermarkIsSavedAfterActingAndNotBefore.
//
// A crash between the two re-delivers the press, and acting twice on «это не
// перепубликация» is harmless — the pair is already pinned. Saving first would
// lose a press instead, which is not.
func TestTheWatermarkIsSavedAfterActingAndNotBefore(t *testing.T) {
	f := newFakeAPI(map[string]string{
		"getUpdates": `{"ok":true,"result":[` +
			callbackUpdate(11, 100500, "norepost:200:100") + `]}`,
	})
	var order []string
	off := &offsetStore{}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get,
		Save: func(ctx context.Context, v int64) error {
			order = append(order, "save")
			return off.set(ctx, v)
		},
		Handle: func(context.Context, int64, int64) (string, error) {
			order = append(order, "act")
			return "", nil
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "act" || order[1] != "save" {
		t.Errorf("order = %v", order)
	}
}

// TestAHandlerFailureIsToldToThePersonWhoPressed, rather than leaving a button
// that spins and a correction that silently did not happen.
func TestAHandlerFailureIsToldToThePersonWhoPressed(t *testing.T) {
	f := newFakeAPI(map[string]string{
		"getUpdates": `{"ok":true,"result":[` +
			callbackUpdate(11, 100500, "norepost:200:100") + `]}`,
	})
	off := &offsetStore{}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get, Save: off.set,
		Handle: func(context.Context, int64, int64) (string, error) {
			return "", errors.New("база занята")
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.seen["answerCallbackQuery"].Get("text"); !strings.Contains(got, "база занята") {
		t.Errorf("the toast said %q", got)
	}
}

// TestNothingToPollIsNotAWrite.
//
// An empty poll must not rewrite the watermark: on a quiet installation that is
// a database write every twenty-five seconds for ever.
func TestNothingToPollIsNotAWrite(t *testing.T) {
	f := newFakeAPI(map[string]string{"getUpdates": `{"ok":true,"result":[]}`})
	saves := 0
	off := &offsetStore{at: 7}
	p := &Poller{
		Bot: f.bot(), Allowed: allowing(100500),
		Offset: off.get,
		Save: func(ctx context.Context, v int64) error {
			saves++
			return off.set(ctx, v)
		},
	}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if saves != 0 {
		t.Errorf("an empty poll wrote the watermark %d times", saves)
	}
}

// TestOnlyCallbacksAreAskedFor.
//
// This program has no use for the messages people send its bot, and asking for
// them would mean carrying somebody's private chat through a queue that logs
// errors.
func TestOnlyCallbacksAreAskedFor(t *testing.T) {
	f := newFakeAPI(map[string]string{"getUpdates": `{"ok":true,"result":[]}`})
	off := &offsetStore{}
	p := &Poller{Bot: f.bot(), Allowed: allowing(1), Offset: off.get, Save: off.set}
	if err := p.Turn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.seen["getUpdates"].Get("allowed_updates"); got != `["callback_query"]` {
		t.Errorf("allowed_updates = %q", got)
	}
}

// notifyMessage builds the shape the worker hands a transport.
func notifyMessage(address, body, attachment string) notify.Message {
	return notify.Message{Address: address, Body: body, Attachment: attachment}
}
