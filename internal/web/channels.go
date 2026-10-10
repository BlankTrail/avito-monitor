// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// Прокси: where the requests go out from, and the screen the pool is worthless
// without.
//
// Four kinds in any mix — a list from a file or a URL, a rotating address with
// its change link, a BlankTrail gateway, and this machine's own address. Ports
// are spread over whatever is ticked, and an entry that starts producing blocks
// loses weight on its own through blanktrail.Mixer.
//
// Each entry has a test that costs nothing, and what «nothing» means is the
// donor's reasoning kept exactly. A list is read and counted with the first bad
// line quoted. A gateway name is checked against the ones BlankTrail actually
// has. A rotating channel's change link is deliberately NOT pulled — the
// provider limits how often it may be, and a test that broke what it was testing
// would be worse than no test at all.

func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) {
	s.renderChannels(w, r, "")
}

func (s *Server) renderChannels(w http.ResponseWriter, r *http.Request, problem string) {
	ctx := r.Context()
	channels, err := s.Store.Channels(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	counts, err := s.Store.CountProxies(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b, err := s.budget(ctx, nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var body strings.Builder

	// The consequence of the mix, stated rather than left to be inferred — and
	// in the terms the collector actually uses. It said «Настроено 6 адресов»
	// over a hundred and thirty exits and a link to fifteen thousand, because
	// the number is the ports in use at once; a person reading it took it for
	// the size of their lists.
	exits, byLink := 0, 0
	for _, c := range channels {
		if !c.Enabled {
			continue
		}
		switch c.Kind {
		case "proxy-list":
			if counts[c.ID] > 0 {
				exits += counts[c.ID]
			} else if c.Source != "" {
				byLink++
			}
		case "gateway":
			exits += max(1, len(splitNames(c.Source)))
		default:
			exits++
		}
	}
	body.WriteString(`<div class="card">`)
	fmt.Fprintf(&body, `<p class="sum">В работе одновременно <strong>%d %s</strong> — `+
		`потолок <strong>%s</strong> запр./мин.</p>`,
		b.Addresses, esc(ru.Plural(b.Addresses, "адрес", "адреса", "адресов")),
		esc(num(b.CapacityPerMin)))
	if exits > 0 || byLink > 0 {
		line := fmt.Sprintf("Всего выходов в запасе: %d", exits)
		if byLink > 0 {
			line += fmt.Sprintf(" и %d %s по ссылке", byLink,
				ru.Plural(byLink, "список", "списка", "списков"))
		}
		body.WriteString(`<p>` + esc(line) + `. Сбор переходит на следующий, ` +
			`когда выход не отвечает или подменяет сертификат.</p>`)
	}
	mode, _ := s.Store.TransportMode(ctx)
	if mode == transport.ModeBlankTrail {
		body.WriteString(note("neutral",
			"Через BlankTrail одновременно работает столько адресов, сколько открыто "+
				"портов (не больше, чем выходов). Каждый адрес — свой потолок; "+
				"порты без своих адресов только доводят до предела быстрее."))
	} else {
		body.WriteString(note("neutral",
			"Напрямую сбор держит одну личность и ходит с одного адреса за раз: "+
				"остальные выходы — запасные, на них он переходит, когда текущий "+
				"не отвечает."))
	}
	body.WriteString(`</div>`)

	if problem != "" {
		body.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}

	for _, c := range channels {
		body.WriteString(s.channelCard(c, counts[c.ID]))
	}
	body.WriteString(channelForm())

	s.render(w, r, page{Title: "Прокси", Body: rawHTML(body.String())})
}

func (s *Server) channelCard(c store.Channel, addresses int) string {
	id := strconv.FormatInt(c.ID, 10)
	tone := "warn"
	if c.Enabled {
		tone = "ok"
	}

	var b strings.Builder
	b.WriteString(`<div class="card">`)
	fmt.Fprintf(&b, `<h2>%s <span class="pill %s">%s</span></h2>`,
		esc(c.Name), attr(tone), esc(kindLabel(c.Kind)))

	switch c.Kind {
	case "proxy-list":
		if addresses > 0 {
			fmt.Fprintf(&b, `<p>%d %s.</p>`, addresses,
				esc(ru.Plural(addresses, "адрес", "адреса", "адресов")))
		} else if c.Source != "" {
			// «0 адресов» read as «empty» over a link to fifteen thousand.
			b.WriteString(`<p>Адреса берутся по ссылке и перечитываются раз в час; ` +
				`сколько их, покажет «Проверить».</p>`)
		}
		if c.Source != "" {
			fmt.Fprintf(&b, `<p class="hint">Источник: %s</p>`, esc(c.Source))
		}
	case "rotating":
		b.WriteString(`<p>Один адрес за раз, меняется по ссылке провайдера.</p>`)
	case "gateway":
		n := len(splitNames(c.Source))
		fmt.Fprintf(&b, `<p>%d %s VPN BlankTrail: %s</p>`, n,
			esc(ru.Plural(n, "шлюз", "шлюза", "шлюзов")), esc(c.Source))
	case "direct":
		b.WriteString(`<p>Собственный адрес этой машины.</p>`)
	}

	// The first unreadable line of the list that was just saved, so a person
	// who pasted a file with one broken line is told which one rather than
	// being left to scroll it.
	if rep := s.lastList.Load(); rep != nil && rep.ChannelID == c.ID && len(rep.Bad) > 0 {
		fmt.Fprintf(&b, `<p class="note warning">%d %s не разобрано, первая: %s</p>`,
			len(rep.Bad),
			esc(ru.Plural(len(rep.Bad), "строка", "строки", "строк")),
			esc(rep.Bad[0]))
	}

	fmt.Fprintf(&b, `<p><button type="button" data-load="/channels/test?id=%s" `+
		`data-into="#test-%s">Проверить</button> <span id="test-%s"></span></p>`,
		attr(id), attr(id), attr(id))
	fmt.Fprintf(&b, `<form method="post" action="/channels/delete" `+
		`data-confirm="Удалить «%s»? Потолок запросов станет ниже.">`+
		`<input type="hidden" name="id" value="%s">`+
		`<button class="danger" type="submit">Удалить</button></form>`,
		attr(c.Name), attr(id))
	b.WriteString(`</div>`)
	return b.String()
}

func kindLabel(kind string) string {
	switch kind {
	case "proxy-list":
		return "список"
	case "rotating":
		return "ротация"
	case "gateway":
		return "шлюз"
	case "direct":
		return "эта машина"
	}
	return kind
}

func channelForm() string {
	var b strings.Builder
	b.WriteString(`<div class="card"><h2>Добавить выход</h2>`)
	b.WriteString(`<form method="post" action="/channels">`)
	b.WriteString(field("Название", "name", "", "text", ""))
	// The fields follow the kind. All of them on screen at once — a list, a
	// link, a rotation address, a change link and a gateway name, for a choice
	// that uses at most two — was a form nobody could read at a glance, and the
	// first thing anybody filming it would have had to explain away.
	b.WriteString(`<label class="field"><span class="label">Вид</span>` +
		`<select name="kind" data-switch>` +
		`<option value="proxy-list">Список адресов</option>` +
		`<option value="rotating">Ротация по ссылке</option>` +
		`<option value="gateway">Шлюзы VPN BlankTrail</option>` +
		`<option value="direct">Собственный адрес этой машины</option>` +
		`</select></label>`)
	b.WriteString(`<div data-when="proxy-list">`)
	b.WriteString(`<label class="field"><span class="label">Список адресов</span>` +
		`<textarea class="input" name="list" rows="6" ` +
		`placeholder="socks5://user:pass@10.0.0.1:1080&#10;10.0.0.2:1080"></textarea>` +
		`<span class="hint">По одному в строке: схема://логин:пароль@адрес:порт, ` +
		`без схемы — socks5. У резидентских прокси с сессиями каждая строка — ` +
		`своя сессия, а значит свой адрес выхода.</span></label>`)
	b.WriteString(field("Или ссылка на список", "source", "", "text",
		"http(s):// или путь к файлу — список перечитывается раз в час."))
	b.WriteString(`</div>`)
	b.WriteString(`<div data-when="rotating">`)
	b.WriteString(field("Адрес для ротации", "upstream", "", "text",
		"Один адрес, который провайдер меняет по ссылке."))
	b.WriteString(field("Ссылка смены адреса", "rotate_url", "", "text",
		"Эта ссылка не дёргается при проверке: провайдеры ограничивают, "+
			"как часто её можно вызывать."))
	b.WriteString(`</div>`)
	// The gateways are fetched when the kind is chosen, as ticks: a set of
	// them is one exit the way a list is one, and the collector hands them out
	// in turn across its ports. Typing a name from memory was the old way.
	b.WriteString(`<div data-when="gateway">` +
		`<p class="hint">Шлюзы VPN настраиваются в самой BlankTrail Proxy ` +
		`(«Конфигурации VPN»); здесь отмечается, какие из них можно брать. ` +
		`Работают только при выходе через BlankTrail.</p>` +
		`<div id="gateways" data-load="/channels/gateways" data-auto></div></div>`)
	b.WriteString(`<div data-when="direct"><p class="hint">Запросы уходят с адреса ` +
		`этой машины. Его можно смешать с другими выходами.</p></div>`)
	b.WriteString(`<button class="primary" type="submit">Добавить</button>`)
	b.WriteString(`</form></div>`)
	return b.String()
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind := r.FormValue("kind")
	c := store.Channel{
		Name:      strings.TrimSpace(r.FormValue("name")),
		Kind:      kind,
		Source:    strings.TrimSpace(r.FormValue("source")),
		RotateURL: strings.TrimSpace(r.FormValue("rotate_url")),
		Weight:    100,
		Enabled:   true,
	}
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		c.ID = id
	}
	if c.Name == "" {
		s.renderChannels(w, r, "не задано название")
		return
	}

	var parsed []blanktrail.Upstream
	var bad []string
	switch kind {
	case "proxy-list":
		parsed, bad = blanktrail.Parse(r.FormValue("list"), "socks5")
		if len(parsed) == 0 && c.Source == "" {
			s.renderChannels(w, r, listProblem(bad))
			return
		}
	case "rotating":
		parsed, _ = blanktrail.Parse(r.FormValue("upstream"), "socks5")
		if len(parsed) != 1 {
			s.renderChannels(w, r, "для ротации нужен ровно один адрес")
			return
		}
		if c.RotateURL == "" {
			s.renderChannels(w, r, "не задана ссылка смены адреса")
			return
		}
	case "gateway":
		// The ticked set, or one name typed by hand for a build that cannot
		// reach the service to list them.
		var names []string
		for _, n := range r.Form["gateways"] {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		if typed := strings.TrimSpace(r.FormValue("gateway")); typed != "" {
			names = append(names, typed)
		}
		c.Source = strings.Join(names, ", ")
		if c.Source == "" {
			s.renderChannels(w, r, "не отмечено ни одного шлюза")
			return
		}
	case "direct":
		// Nothing to parse: this machine's own address is one address.
	default:
		s.renderChannels(w, r, fmt.Sprintf("вид %q эта сборка не знает", kind))
		return
	}

	id, err := s.Store.SaveChannel(ctx, c)
	if err != nil {
		s.renderChannels(w, r, err.Error())
		return
	}
	if len(parsed) > 0 {
		ps := make([]store.Proxy, 0, len(parsed))
		for _, u := range parsed {
			port, _ := strconv.Atoi(u.Port)
			ps = append(ps, store.Proxy{
				ChannelID: id, Scheme: u.Scheme, Host: u.Host, Port: port,
				Username: u.User, Password: u.Pass, Enabled: true,
			})
		}
		if err := s.Store.ReplaceProxies(ctx, id, ps); err != nil {
			s.renderChannels(w, r, err.Error())
			return
		}
	}
	// The parse report survives the redirect, so the first unreadable line is
	// shown beside the channel it belongs to rather than being lost.
	s.lastList.Store(&listReport{ChannelID: id, Good: len(parsed), Bad: bad})
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

// listProblem is what a list that produced nothing usable says.
func listProblem(bad []string) string {
	if len(bad) == 0 {
		return "список пуст"
	}
	return fmt.Sprintf("ни одного адреса разобрать не удалось; первая "+
		"нечитаемая строка: %s", bad[0])
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "нет такого выхода", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteChannel(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

// testChannel is the per-entry test that costs nothing.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeFragment(w, note("bad", "нет такого выхода"))
		return
	}
	channels, err := s.Store.Channels(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	var c store.Channel
	for _, ch := range channels {
		if ch.ID == id {
			c = ch
		}
	}
	if c.ID == 0 {
		writeFragment(w, note("bad", "нет такого выхода"))
		return
	}

	if c.Kind == "rotating" {
		// Deliberately nothing. The provider limits how often the change link
		// may be pulled, and a test that broke what it was testing would be
		// worse than no test at all.
		writeFragment(w, note("neutral",
			"Ссылка смены адреса не проверяется: провайдеры ограничивают, "+
				"как часто её можно вызывать, и проверка потратила бы одну "+
				"из этих попыток."))
		return
	}
	if s.CheckChannel == nil {
		counts, _ := s.Store.CountProxies(r.Context())
		writeFragment(w, note("neutral", fmt.Sprintf("%d %s в списке.",
			counts[id], ru.Plural(counts[id], "адрес", "адреса", "адресов"))))
		return
	}
	summary, err := s.CheckChannel(r.Context(), id)
	if err != nil {
		writeFragment(w, note("bad", s.redactChannel(r, id, err.Error())))
		return
	}
	writeFragment(w, note("neutral", summary))
}

// redactChannel takes every stored credential of this channel out of a message.
//
// The engine's error may quote the URL it dialled, and the URL carries the
// password. This fragment goes onto a screen.
func (s *Server) redactChannel(r *http.Request, id int64, msg string) string {
	ps, err := s.Store.Proxies(r.Context(), id)
	if err != nil {
		return msg
	}
	for _, p := range ps {
		if p.Password != "" {
			msg = strings.ReplaceAll(msg, p.Password, "<пароль>")
		}
	}
	return msg
}

// listGateways offers what BlankTrail actually has configured, so a gateway is
// picked rather than typed from memory.
func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) {
	if s.Gateways == nil {
		writeFragment(w, note("warning", "Эта сборка не умеет спрашивать шлюзы."))
		return
	}
	list, err := s.Gateways(r.Context())
	if err != nil {
		writeFragment(w, note("bad", err.Error()))
		return
	}
	// Available is whether the gateway backend works at all, and Reason says
	// why not. Two different silences: «шлюзы недоступны» is something to fix,
	// «шлюзов не настроено» is something to do.
	if !list.Available {
		reason := list.Reason
		if reason == "" {
			reason = "шлюзы недоступны"
		}
		writeFragment(w, note("warning", reason))
		return
	}
	if len(list.Gateways) == 0 {
		writeFragment(w, note("warning", "ни одного шлюза не настроено"))
		return
	}
	// Ticks, all on by default: the common wish is «через мои шлюзы», and
	// unticking the one that misbehaves is easier than ticking fifteen.
	var b strings.Builder
	fmt.Fprintf(&b, `<p class="hint">У BlankTrail %d %s.</p><div class="gw-list">`,
		len(list.Gateways), esc(ru.Plural(len(list.Gateways), "шлюз", "шлюза", "шлюзов")))
	for _, g := range list.Gateways {
		fmt.Fprintf(&b, `<label class="check"><input type="checkbox" name="gateways" `+
			`value="%s" checked><span>%s</span></label>`, attr(g.Name), esc(g.Name))
	}
	b.WriteString(`</div>`)
	writeFragment(w, b.String())
}
