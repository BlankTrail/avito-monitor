// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Настройки: the screen decision 3 lands on, and the one every other decision
// quietly assumes.
//
// Three rules run through all of it.
//
// A secret already set is never rendered back — the page goes into a browser's
// history, into screenshots and into whatever a person pastes when they ask for
// help, so it says «задан» beside an empty box. An empty secret box therefore
// means «не менять» and not «стереть», or saving quiet hours would wipe the bot
// token and the person would find out when their alerts stopped; clearing one
// is its own tick. And no error from a check is shown without the credential
// being taken out of it first.

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, problem string) {
	ctx := r.Context()

	mode, modeErr := s.Store.TransportMode(ctx)
	bt, err := s.Store.BlankTrailConfig(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tg, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	panel, err := s.Store.PanelSettings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ret, err := s.Store.StoredRetention(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	floor, err := s.Store.SpaceFloorBytes(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rpm, err := s.Store.RequestsPerMinute(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	quietFrom, quietTo, err := s.Store.QuietHours(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var b strings.Builder
	if problem != "" {
		b.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}
	b.WriteString(`<form method="post" action="/settings">`)

	// — Transport
	b.WriteString(`<div class="card"><h2>Как выходить в сеть</h2>`)
	b.WriteString(`<label class="field"><span class="label">Способ</span><select name="mode">`)
	for _, o := range []struct{ v, label string }{
		{"direct", "Напрямую с этой машины"},
		{"blanktrail", "Через BlankTrail"},
	} {
		sel := ""
		if modeErr == nil && string(mode) == o.v {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, attr(o.v), sel, esc(o.label))
	}
	b.WriteString(`</select></label>`)
	b.WriteString(field("Адрес управления BlankTrail", "bt_url", bt.ControlURL, "text",
		"Например http://127.0.0.1:8891"))
	b.WriteString(secretField("Ключ BlankTrail", "bt_key", bt.APIKey != ""))
	b.WriteString(field("Портов", "bt_ports", strconv.Itoa(bt.Ports), "number",
		"Сколько портов открывать. Порты — это не адреса: один адрес остаётся "+
			"одним потолком, сколько бы портов ни было открыто."))
	b.WriteString(field("Запросов в минуту на адрес", "requests_per_minute",
		strconv.Itoa(rpm), "number",
		"Измеренный потолок — 6 (шестьдесят запросов за десять минут). "+
			"Больше поставить можно, но действовать будет меньшее из двух."))
	b.WriteString(`<p><button type="button" data-load="/settings/blanktrail" ` +
		`data-into="#bt-check">Проверить</button> <span id="bt-check"></span></p>`)
	b.WriteString(`</div>`)

	// — Telegram
	b.WriteString(`<div class="card"><h2>Телеграм</h2>`)
	b.WriteString(secretField("Токен бота", "telegram_token", tg.BotToken != ""))
	b.WriteString(`<p><button type="button" data-load="/settings/telegram" ` +
		`data-into="#tg-check">Проверить</button> <span id="tg-check"></span></p>`)
	if s.botAPIFailed.Load() || tg.APIID != 0 {
		b.WriteString(note("warning",
			"Bot API не отвечает. Через MTProto сообщения идут собственным "+
				"протоколом телеграма — для этого нужна пара с my.telegram.org."))
		b.WriteString(field("api_id (my.telegram.org)", "telegram_api_id",
			strconv.Itoa(tg.APIID), "number", ""))
		b.WriteString(secretField("api_hash (my.telegram.org)",
			"telegram_api_hash", tg.APIHash != ""))
	}
	b.WriteString(field("Тихие часы, с", "quiet_from", minutesToClock(quietFrom), "text",
		"Например 22:30. Пусто — тихих часов нет."))
	b.WriteString(field("Тихие часы, до", "quiet_to", minutesToClock(quietTo), "text", ""))
	b.WriteString(`</div>`)

	// — The panel itself
	b.WriteString(`<div class="card"><h2>Панель</h2>`)
	b.WriteString(secretField("Пароль", "password", panel.PasswordHash != ""))
	b.WriteString(checkbox("Открыть панель для локальной сети", "lan", panel.LAN,
		"Без своего пароля панель за пределы этой машины не выходит: здесь "+
			"лежат ключ прокси, токен бота и всё, что собрано."))
	on := panel.Autostart
	if s.Autostart != nil {
		// Asked, not remembered: an entry can be removed by anything, and a box
		// ticked from a stored intention would tell somebody their monitor
		// starts at login when it does not.
		if actual, err := s.Autostart.Enabled(); err == nil {
			on = actual
		}
	}
	b.WriteString(checkbox("Запускать при входе в систему", "autostart", on, ""))
	b.WriteString(`</div>`)

	// — Storage
	b.WriteString(`<div class="card"><h2>Хранение</h2>`)
	b.WriteString(field("Хранить тела страниц, дней", "retention_body_days",
		strconv.FormatInt(int64(ret.BodyMaxAge/(24*time.Hour)), 10), "number",
		"Архив ответов, по которым можно разобрать, что случилось."))
	b.WriteString(field("…и не больше, МБ", "retention_body_mb",
		strconv.FormatInt(ret.BodyMaxBytes>>20, 10), "number",
		"Ограничение по возрасту без ограничения по размеру на сломанной "+
			"установке не ограничивает ничего."))
	b.WriteString(field("Не собирать, если свободно меньше, ГБ", "space_floor_gb",
		strconv.FormatInt(floor>>30, 10), "number",
		"Действует большее из этого числа и двух процентов тома."))
	if v, err := s.Store.Volume(ctx); err == nil {
		fmt.Fprintf(&b, `<p class="note neutral">Сейчас база занимает %s МБ.</p>`,
			esc(num(float64(v.FileBytes)/(1<<20))))
	}
	b.WriteString(`</div>`)

	b.WriteString(`<p><button class="primary" type="submit">Сохранить</button></p>`)
	b.WriteString(`</form>`)

	s.render(w, r, page{Title: "Настройки", Body: rawHTML(b.String())})
}

// secretField is a credential's control: whether one is set, a box to replace
// it, and a tick to clear it.
//
// The value is never in the markup. «Задан» plus an empty box says the same
// thing without putting the key in the browser's history.
func secretField(label, name string, set bool) string {
	state := "не задан"
	if set {
		state = "задан"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<label class="field"><span class="label">%s — %s</span>`,
		esc(label), esc(state))
	fmt.Fprintf(&b, `<input class="input" type="password" name="%s" value="" `+
		`autocomplete="new-password">`, attr(name))
	b.WriteString(`<span class="hint">Пустое поле — оставить как есть.</span></label>`)
	if set {
		b.WriteString(checkbox("Стереть", name+"_clear", false, ""))
	}
	return b.String()
}

func minutesToClock(m int) string {
	if m < 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// clockToMinutes reads «22:30». -1 means «not set», which is what the schema's
// default of -1 says and what an empty box means.
func clockToMinutes(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return -1, nil
	}
	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, fmt.Errorf("время %q не разобрать: нужно, например, 22:30", v)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("время %q не разобрать: нужно, например, 22:30", v)
	}
	return hh*60 + mm, nil
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var bad []string

	mode, err := parseMode(r.FormValue("mode"))
	if err != nil {
		bad = append(bad, err.Error())
	}

	// The panel half first, because the LAN refusal depends on the password
	// that may be arriving in this same post.
	panel, err := s.Store.PanelSettings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.FormValue("password_clear") == "1" {
		panel.PasswordHash = ""
	} else if v := r.FormValue("password"); v != "" {
		hash, err := HashPassword(v)
		if err != nil {
			bad = append(bad, err.Error())
		} else {
			panel.PasswordHash = hash
		}
	}
	panel.LAN = r.FormValue("lan") == "1"
	panel.Autostart = r.FormValue("autostart") == "1"
	if panel.LAN && panel.PasswordHash == "" {
		// Refused here as well as at the port. ListenAddress is the guarantee;
		// this is what stops a person ticking the box, restarting, and finding
		// the panel did not come up — with the reason in a log nobody reads.
		bad = append(bad, "чтобы открыть панель для сети, сначала задайте пароль")
	}

	quietFrom, err := clockToMinutes(r.FormValue("quiet_from"))
	if err != nil {
		bad = append(bad, err.Error())
	}
	quietTo, err := clockToMinutes(r.FormValue("quiet_to"))
	if err != nil {
		bad = append(bad, err.Error())
	}

	if len(bad) > 0 {
		s.renderSettings(w, r, strings.Join(bad, "; "))
		return
	}

	for _, step := range []func() error{
		func() error { return s.Store.SetTransportMode(ctx, mode) },
		func() error { return s.saveBlankTrail(ctx, r) },
		func() error { return s.saveTelegram(ctx, r) },
		func() error { return s.Store.SetPanelSettings(ctx, panel) },
		func() error { return s.applyAutostart(panel.Autostart) },
		func() error { return s.Store.SetQuietHours(ctx, quietFrom, quietTo) },
		func() error { return s.saveStorage(ctx, r) },
	} {
		if err := step(); err != nil {
			s.renderSettings(w, r, err.Error())
			return
		}
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// secretFrom applies the three-way rule: clear, replace, or leave alone.
func secretFrom(r *http.Request, name, current string) string {
	if r.FormValue(name+"_clear") == "1" {
		return ""
	}
	if v := strings.TrimSpace(r.FormValue(name)); v != "" {
		return v
	}
	return current
}

func (s *Server) saveBlankTrail(ctx context.Context, r *http.Request) error {
	cur, err := s.Store.BlankTrailConfig(ctx)
	if err != nil {
		return err
	}
	cur.ControlURL = strings.TrimSpace(r.FormValue("bt_url"))
	cur.APIKey = secretFrom(r, "bt_key", cur.APIKey)
	if v, err := strconv.Atoi(r.FormValue("bt_ports")); err == nil && v > 0 {
		cur.Ports = v
	}
	if err := s.Store.SetBlankTrailConfig(ctx, cur); err != nil {
		return err
	}
	if v, err := strconv.Atoi(r.FormValue("requests_per_minute")); err == nil && v > 0 {
		return s.Store.SetRequestsPerMinute(ctx, v)
	}
	return nil
}

func (s *Server) saveTelegram(ctx context.Context, r *http.Request) error {
	cur, err := s.Store.TelegramConfig(ctx)
	if err != nil {
		return err
	}
	cur.BotToken = secretFrom(r, "telegram_token", cur.BotToken)
	cur.APIHash = secretFrom(r, "telegram_api_hash", cur.APIHash)
	if v, err := strconv.Atoi(r.FormValue("telegram_api_id")); err == nil {
		cur.APIID = v
	}
	return s.Store.SetTelegramConfig(ctx, cur)
}

// saveStorage writes the retention row and tells this process about it.
//
// Both, because the columns are read at start and nothing reads them again. A
// save that wrote the row and left the running process on the old numbers would
// be a setting that takes effect after a restart nobody mentioned.
func (s *Server) saveStorage(ctx context.Context, r *http.Request) error {
	ret, err := s.Store.StoredRetention(ctx)
	if err != nil {
		return err
	}
	if v, err := strconv.Atoi(r.FormValue("retention_body_days")); err == nil && v > 0 {
		ret.BodyMaxAge = time.Duration(v) * 24 * time.Hour
	}
	if v, err := strconv.ParseInt(r.FormValue("retention_body_mb"), 10, 64); err == nil && v > 0 {
		ret.BodyMaxBytes = v << 20
	}
	if err := s.Store.SetStoredRetention(ctx, ret); err != nil {
		return err
	}
	s.Store.SetRetention(ret)

	if v, err := strconv.ParseInt(r.FormValue("space_floor_gb"), 10, 64); err == nil && v > 0 {
		if err := s.Store.SetSpaceFloorBytes(ctx, v<<30); err != nil {
			return err
		}
		s.Store.SetSpaceFloor(v << 30)
	}
	return nil
}

func (s *Server) applyAutostart(want bool) error {
	if s.Autostart == nil {
		return nil
	}
	on, err := s.Autostart.Enabled()
	if err != nil {
		return err
	}
	switch {
	case want && !on:
		return s.Autostart.Enable()
	case !want && on:
		return s.Autostart.Disable()
	}
	return nil
}

// checkBlankTrail asks the control API who it is, and answers with a fragment.
func (s *Server) checkBlankTrail(w http.ResponseWriter, r *http.Request) {
	if s.CheckBlankTrail == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет проверять BlankTrail."))
		return
	}
	cfg, err := s.Store.BlankTrailConfig(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	if cfg.APIKey == "" || cfg.ControlURL == "" {
		writeFragment(w, note("warning", "Не заданы адрес управления или ключ."))
		return
	}
	if err := s.CheckBlankTrail(r.Context(), cfg.ControlURL, cfg.APIKey); err != nil {
		// The key is taken out of whatever came back. An SDK that quoted the
		// request quoted the key, and this fragment goes on a screen.
		writeFragment(w, note("bad", "Не отвечает: "+redactSecret(err.Error(), cfg.APIKey)))
		return
	}
	writeFragment(w, note("neutral", "Отвечает."))
}

func (s *Server) checkTelegram(w http.ResponseWriter, r *http.Request) {
	if s.CheckTelegram == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет проверять телеграм."))
		return
	}
	cfg, err := s.Store.TelegramConfig(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	if cfg.BotToken == "" {
		writeFragment(w, note("warning", "Не задан токен бота."))
		return
	}
	name, err := s.CheckTelegram(r.Context(), cfg.BotToken)
	if err != nil {
		s.botAPIFailed.Store(true)
		writeFragment(w, note("bad",
			"Не отвечает: "+redactSecret(err.Error(), cfg.BotToken)+
				" Перезагрузите страницу — появятся поля для MTProto."))
		return
	}
	writeFragment(w, note("neutral", "Отвечает: @"+name))
}

// redactSecret takes a credential out of a message that may quote it.
//
// Belt and braces over internal/telegram's own redaction: this fragment goes
// onto a screen, and the one error nobody anticipated is the one that carries
// the key.
func redactSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(msg, secret, "<ключ>")
}
