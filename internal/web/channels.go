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

	// The consequence of the mix, stated rather than left to be inferred.
	body.WriteString(`<div class="card">`)
	fmt.Fprintf(&body, `<p class="sum">Настроено <strong>%d %s</strong> — `+
		`потолок <strong>%s</strong> запр./мин.</p>`,
		b.Addresses, esc(ru.Plural(b.Addresses, "адрес", "адреса", "адресов")),
		esc(num(b.CapacityPerMin)))
	body.WriteString(note("neutral",
		"Сколько адресов — во столько раз выше потолок. Один адрес остаётся "+
			"одним потолком, сколько бы портов на него ни было открыто: "+
			"порты — это не адреса, и лишние порты только доводят до предела "+
			"быстрее."))
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
		fmt.Fprintf(&b, `<p>%d %s.</p>`, addresses,
			esc(ru.Plural(addresses, "адрес", "адреса", "адресов")))
		if c.Source != "" {
			fmt.Fprintf(&b, `<p class="hint">Источник: %s</p>`, esc(c.Source))
		}
	case "rotating":
		b.WriteString(`<p>Один адрес за раз, меняется по ссылке провайдера.</p>`)
	case "gateway":
		fmt.Fprintf(&b, `<p>Шлюз BlankTrail: %s</p>`, esc(c.Source))
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
	b.WriteString(`<label class="field"><span class="label">Вид</span>` +
		`<select name="kind">` +
		`<option value="proxy-list">Список адресов</option>` +
		`<option value="rotating">Ротация по ссылке</option>` +
		`<option value="gateway">Шлюз BlankTrail</option>` +
		`<option value="direct">Собственный адрес этой машины</option>` +
		`</select></label>`)
	b.WriteString(`<label class="field"><span class="label">Список адресов</span>` +
		`<textarea class="input" name="list" rows="6" ` +
		`placeholder="socks5://user:pass@10.0.0.1:1080&#10;10.0.0.2:1080"></textarea>` +
		`<span class="hint">По одному в строке. Схема по умолчанию — socks5.</span></label>`)
	b.WriteString(field("Или ссылка на список", "source", "", "text",
		"http(s):// — список будет перечитываться."))
	b.WriteString(field("Адрес для ротации", "upstream", "", "text",
		"Один адрес, который провайдер меняет по ссылке."))
	b.WriteString(field("Ссылка смены адреса", "rotate_url", "", "text",
		"Эта ссылка не дёргается при проверке: провайдеры ограничивают, "+
			"как часто её можно вызывать."))
	b.WriteString(`<p><button type="button" data-load="/channels/gateways" ` +
		`data-into="#gateways">Показать шлюзы BlankTrail</button> ` +
		`<span id="gateways"></span></p>`)
	b.WriteString(field("Имя шлюза", "gateway", "", "text", ""))
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
		c.Source = strings.TrimSpace(r.FormValue("gateway"))
		if c.Source == "" {
			s.renderChannels(w, r, "не выбран шлюз")
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
	names := make([]string, 0, len(list.Gateways))
	for _, g := range list.Gateways {
		names = append(names, esc(g.Name))
	}
	writeFragment(w, `<span>`+strings.Join(names, ", ")+`</span>`)
}
