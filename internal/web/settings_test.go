// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BlankTrail/avito-monitor/internal/store"
)

// secretKey is a fixture and not a key. It has the shape of a BlankTrail API
// key — forty hex characters — because these tests assert that a secret of that
// shape never reaches a page, and nothing more. A working key in a public AGPL
// repository would be a working key in every clone of it.
const secretKey = "0000000000000000000000000000000000000000"

func settingsForm(over url.Values) url.Values {
	f := url.Values{
		"mode": {"direct"}, "requests_per_minute": {"6"},
		"quiet_from": {""}, "quiet_to": {""},
		"retention_body_days": {"30"}, "retention_body_mb": {"512"},
		"space_floor_gb": {"5"},
	}
	for k, v := range over {
		f[k] = v
	}
	return f
}

// TestASecretIsNeverRenderedBack.
//
// The page goes into a browser's history, into screenshots and into whatever a
// person pastes when they ask for help. «Задан» plus an empty box says the same
// thing without handing the key over.
func TestASecretIsNeverRenderedBack(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetBlankTrailConfig(ctx, store.BlankTrailConfig{
		ControlURL: "http://127.0.0.1:8891", APIKey: secretKey, Ports: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret", APIID: 42, APIHash: "hashhash",
	}); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/settings").Body.String()
	for _, secret := range []string{secretKey, "AAHsecretsecret", "hashhash"} {
		if strings.Contains(body, secret) {
			t.Errorf("the page carries a secret: %q", secret)
		}
	}
	if !strings.Contains(body, "задан") {
		t.Error("the page does not say that a secret is already set")
	}
	// The non-secret half is shown, because it is what somebody checks.
	if !strings.Contains(body, "127.0.0.1:8891") {
		t.Error("the control URL is not shown")
	}
}

// TestAnEmptySecretFieldMeansLeaveItAlone.
//
// Otherwise saving quiet hours wipes the bot token, and the person finds out
// when their alerts stop.
func TestAnEmptySecretFieldMeansLeaveItAlone(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret",
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"quiet_from": {"22:30"}, "quiet_to": {"07:00"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.BotToken != "123456:AAHsecretsecret" {
		t.Error("the bot token was cleared by a save that did not mention it")
	}
}

// TestASecretCanStillBeCleared, deliberately, by a control that says so.
func TestASecretCanStillBeCleared(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if err := s.Store.SetTelegramConfig(ctx, store.TelegramConfig{
		BotToken: "123456:AAHsecretsecret",
	}); err != nil {
		t.Fatal(err)
	}
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"telegram_token_clear": {"1"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	got, _ := s.Store.TelegramConfig(ctx)
	if got.BotToken != "" {
		t.Error("an explicit clear did not clear the token")
	}
}

// TestTheMTProtoPairStaysHiddenUntilTheBotApiHasFailed.
//
// The schema's own words: «a machine where the direct route works is never sent
// to my.telegram.org». Offering it up front asks somebody to register an
// application they will never need.
func TestTheMTProtoPairStaysHiddenUntilTheBotApiHasFailed(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetTelegramConfig(context.Background(),
		store.TelegramConfig{BotToken: "123:AAH"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(get(t, s, "/settings").Body.String(), "my.telegram.org") {
		t.Error("the MTProto pair is offered before anything has failed")
	}
	s.CheckTelegram = func(context.Context, string) (string, error) {
		return "", errors.New("api.telegram.org недоступен")
	}
	if rec := get(t, s, "/settings/telegram"); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if !strings.Contains(get(t, s, "/settings").Body.String(), "my.telegram.org") {
		t.Error("the MTProto pair is still hidden after the Bot API failed")
	}
}

// TestSettingAPasswordStoresAHashAndNotThePassword.
func TestSettingAPasswordStoresAHashAndNotThePassword(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"password": {"мой пароль"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	p, err := s.Store.PanelSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.PasswordHash, "мой пароль") {
		t.Fatal("the password is stored as itself")
	}
	if !VerifyPassword(p.PasswordHash, "мой пароль") {
		t.Error("the stored hash does not verify")
	}
}

// TestTickingTheLanBoxWithNoPasswordIsRefusedOnTheScreenToo.
//
// ListenAddress refuses it at the port, which is the guarantee. Refusing it
// here as well is what stops a person ticking the box, restarting, and finding
// the panel did not come up — with the reason in a log they do not read.
func TestTickingTheLanBoxWithNoPasswordIsRefusedOnTheScreenToo(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/settings", settingsForm(url.Values{"lan": {"1"}}))
	if rec.Code == http.StatusSeeOther {
		t.Fatal("the LAN was opened with no password")
	}
	if !strings.Contains(rec.Body.String(), "парол") {
		t.Errorf("the refusal does not say what is missing: %s", rec.Body.String())
	}
	p, _ := s.Store.PanelSettings(context.Background())
	if p.LAN {
		t.Error("the LAN tick was stored anyway")
	}
}

// TestTheLanBoxAndAPasswordInOnePostIsAccepted, because that is the order a
// person does it in.
func TestTheLanBoxAndAPasswordInOnePostIsAccepted(t *testing.T) {
	s := newServer(t)
	rec := post(t, s, "/settings", settingsForm(url.Values{
		"lan": {"1"}, "password": {"секрет"},
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("refused: %s", rec.Body.String())
	}
	p, _ := s.Store.PanelSettings(context.Background())
	if !p.LAN || p.PasswordHash == "" {
		t.Errorf("stored %+v", p)
	}
}

// TestRetentionIsShownWithItsCurrentValuesRatherThanItsNames.
//
// The spec asks for exactly this. «Срок хранения тел» with no number is a
// setting nobody can tell they have already changed.
func TestRetentionIsShownWithItsCurrentValuesRatherThanItsNames(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	r, err := s.Store.StoredRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.BodyMaxAge = 7 * 24 * time.Hour
	if err := s.Store.SetStoredRetention(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(t, s, "/settings").Body.String(), `value="7"`) {
		t.Error("the current retention is not in the form")
	}
}

// TestSavingRetentionTakesEffectInThisProcessToo.
//
// The columns are read at start and nothing reads them again. A save that wrote
// the row and left the running process on the old numbers would be a setting
// that takes effect after a restart nobody mentioned.
func TestSavingRetentionTakesEffectInThisProcessToo(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"retention_body_days": {"7"}, "retention_body_mb": {"256"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	if got := s.Store.Retention().BodyMaxAge; got != 7*24*time.Hour {
		t.Errorf("the process still holds BodyMaxAge = %v", got)
	}
}

// TestQuietHoursAreStoredAsMinutes, so 22:30 survives being typed.
func TestQuietHoursAreStoredAsMinutes(t *testing.T) {
	s := newServer(t)
	if rec := post(t, s, "/settings", settingsForm(url.Values{
		"quiet_from": {"22:30"}, "quiet_to": {"07:00"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatal(rec.Body.String())
	}
	from, to, err := s.Store.QuietHours(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if from != 22*60+30 || to != 7*60 {
		t.Errorf("quiet hours = %d..%d, want 1350..420", from, to)
	}
}

// TestATimeThatIsNotATimeIsRefused rather than silently becoming midnight.
func TestATimeThatIsNotATimeIsRefused(t *testing.T) {
	s := newServer(t)
	for _, bad := range []string{"вечером", "25:00", "22:70", "22", "22:30:00"} {
		rec := post(t, s, "/settings", settingsForm(url.Values{"quiet_from": {bad}}))
		if rec.Code == http.StatusSeeOther {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestTheBlankTrailCheckDoesNotNeedALiveOne, which is what the field is for.
func TestTheBlankTrailCheckDoesNotNeedALiveOne(t *testing.T) {
	s := newServer(t)
	var sawKey string
	s.CheckBlankTrail = func(_ context.Context, url, key string) error {
		sawKey = key
		return nil
	}
	if err := s.Store.SetBlankTrailConfig(context.Background(),
		store.BlankTrailConfig{ControlURL: "http://127.0.0.1:8891",
			APIKey: secretKey, Ports: 2}); err != nil {
		t.Fatal(err)
	}
	rec := get(t, s, "/settings/blanktrail")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d", rec.Code)
	}
	if sawKey != secretKey {
		t.Error("the check was not given the stored key")
	}
	if strings.Contains(rec.Body.String(), secretKey) {
		t.Error("the answer carries the key")
	}
}

// TestAFailedCheckSaysWhatWentWrongWithoutTheKeyInIt.
func TestAFailedCheckSaysWhatWentWrongWithoutTheKeyInIt(t *testing.T) {
	s := newServer(t)
	s.CheckBlankTrail = func(context.Context, string, string) error {
		return errors.New("ключ " + secretKey + " отклонён")
	}
	if err := s.Store.SetBlankTrailConfig(context.Background(),
		store.BlankTrailConfig{ControlURL: "http://x", APIKey: secretKey, Ports: 2}); err != nil {
		t.Fatal(err)
	}
	body := get(t, s, "/settings/blanktrail").Body.String()
	if strings.Contains(body, secretKey) {
		t.Errorf("a key leaked through an error: %s", body)
	}
	if !strings.Contains(body, "отклонён") {
		t.Errorf("the reason was lost with the key: %s", body)
	}
}

// TestAutostartIsAskedRatherThanRemembered.
//
// An entry can be removed by anything. A checkbox showing a stored intention
// would tell somebody their monitor starts at login when it does not.
func TestAutostartIsAskedRatherThanRemembered(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetPanelSettings(context.Background(),
		store.PanelSettings{Autostart: true}); err != nil {
		t.Fatal(err)
	}
	asked := false
	s.Autostart = fakeAutostart{on: false, asked: &asked}
	body := get(t, s, "/settings").Body.String()
	if !asked {
		t.Error("the operating system was not asked")
	}
	if strings.Contains(body, `name="autostart" value="1" checked`) {
		t.Error("the box is ticked from the stored intention rather than from the truth")
	}
}

type fakeAutostart struct {
	on    bool
	asked *bool
}

func (f fakeAutostart) Enabled() (bool, error) { *f.asked = true; return f.on, nil }
func (f fakeAutostart) Enable() error          { return nil }
func (f fakeAutostart) Disable() error         { return nil }

// TestATickedBoxIsActuallyTicked.
//
// checkbox() writes a hidden "0" before the box so that unticking posts
// something — but a ticked box then posts both, «0» first, and r.FormValue
// returns the first. Read that way, every checkbox on this panel was
// permanently off whatever the person clicked: the panel could not be opened to
// the network, autostart could not be enabled, and «Стереть» never erased
// anything. Found by Результаты needing a working checkbox of its own.
func TestATickedBoxIsActuallyTicked(t *testing.T) {
	s := newServer(t)

	form := url.Values{"mode": {"direct"}}
	form["autostart"] = []string{"0", "1"} // exactly what a browser posts
	if rec := post(t, s, "/settings", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d (%s)", rec.Code, rec.Body.String())
	}
	p, err := s.Store.PanelSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Autostart {
		t.Error("the box was ticked and the setting stayed off")
	}

	// And the other half still holds: unticking posts only the hidden zero,
	// which is the reason the hidden companion exists at all.
	form["autostart"] = []string{"0"}
	if rec := post(t, s, "/settings", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d (%s)", rec.Code, rec.Body.String())
	}
	if p, _ = s.Store.PanelSettings(context.Background()); p.Autostart {
		t.Error("unticking the box left the setting on")
	}
}

// TestWhenTheBotApiFailsTheCheckTriesMTProto.
//
// The pair was asked for and then never used: «Проверить» said only that the
// Bot API was down, and nothing told the person whether the pair they typed
// would carry the messages.
func TestWhenTheBotApiFailsTheCheckTriesMTProto(t *testing.T) {
	s := newServer(t)
	if err := s.Store.SetTelegramConfig(context.Background(),
		store.TelegramConfig{BotToken: "123:AAH", APIID: 12345, APIHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	s.CheckTelegram = func(context.Context, string) (string, error) {
		return "", errors.New("api.telegram.org недоступен")
	}
	asked := false
	s.CheckMTProto = func(context.Context) error { asked = true; return nil }
	body := get(t, s, "/settings/telegram").Body.String()
	if !asked {
		t.Fatal("MTProto was not tried")
	}
	if !strings.Contains(body, "MTProto отвечает") {
		t.Errorf("the check does not say MTProto will carry the messages: %s", body)
	}

	s.CheckMTProto = func(context.Context) error { return errors.New("AUTH_KEY_UNREGISTERED") }
	body = get(t, s, "/settings/telegram").Body.String()
	if !strings.Contains(body, "AUTH_KEY_UNREGISTERED") || !strings.Contains(body, "api.telegram.org") {
		t.Errorf("a double failure does not name both: %s", body)
	}
}
