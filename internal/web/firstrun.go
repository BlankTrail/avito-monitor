// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BlankTrail/avito-monitor/internal/transport"
)

// The first screen asks which way out to use, and it asks with numbers.
//
// Spec decision 3: settings.transport_mode starts empty and nothing collects
// until it is set. Defaulting to direct would let a user conclude the product is
// broken when it is behaving exactly as documented; defaulting to BlankTrail
// would contradict mode (b) being first class. Asking once costs one screen and
// is the only honest option.
//
// Every figure below is from docs/measurements/2026-08-23-soak.md and carries
// that run's caveats, because a number on a settings screen is a number
// somebody plans capacity against. The spec's own earlier estimate of «300–700
// requests» is not here: that run refuted it, and neither transport turned out
// to have a ceiling in requests at all.

func (s *Server) firstRunPage(w http.ResponseWriter, r *http.Request) {
	s.renderFirstRun(w, r, "")
}

func (s *Server) renderFirstRun(w http.ResponseWriter, r *http.Request, problem string) {
	var b strings.Builder

	if problem != "" {
		b.WriteString(`<div class="card">` + note("bad", problem) + `</div>`)
	}

	b.WriteString(`<form method="post" action="/first-run">`)
	b.WriteString(`<div class="row">`)

	b.WriteString(`<div class="card grow">`)
	b.WriteString(`<label class="check"><input type="radio" name="mode" value="direct">` +
		`<span><strong>Напрямую с этой машины</strong></span></label>`)
	b.WriteString(`<p>Бесплатно. Около <strong>250–260 чистых запросов в час</strong> ` +
		`с одного адреса.</p>`)
	b.WriteString(`<p>Останавливается на капче: пройти её этот режим не умеет, ` +
		`и при первой же встрече наблюдение встаёт и само не продолжит.</p>`)
	b.WriteString(`<p>Список прокси умножает первую цифру: сколько адресов — ` +
		`во столько раз больше. Один адрес — один потолок, сколько бы ` +
		`соединений вы ни открыли.</p>`)
	b.WriteString(note("warning",
		"Три четверти времени уходит на ожидание: в измеренном прогоне "+
			"69 минут из 93 машина просто ждала снятия отказов."))
	b.WriteString(`</div>`)

	b.WriteString(`<div class="card grow">`)
	b.WriteString(`<label class="check"><input type="radio" name="mode" value="blanktrail">` +
		`<span><strong>Через BlankTrail</strong></span></label>`)
	b.WriteString(`<p>Платно, 299 ₽ в месяц. Около <strong>360 чистых запросов в час</strong> ` +
		`с того же одного адреса.</p>`)
	b.WriteString(`<p>Проходит капчу молча: клиент получает обычный ответ, ` +
		`просто дольше. Наблюдение не встаёт.</p>`)
	b.WriteString(`</div>`)

	b.WriteString(`</div>`)

	// The caveat, once, under both — because it applies to both figures and
	// repeating it per card would make it read as fine print rather than as the
	// condition the numbers hold under.
	b.WriteString(note("neutral",
		"Обе цифры — из одного измерения 23–24 августа 2026 года: один общий "+
			"адрес выхода, около 1200 запросов, и в обоих случаях прогон "+
			"остановил оператор, а не площадка. С другого адреса или с "+
			"нескольких числа будут другие."))
	b.WriteString(note("neutral", "Это можно поменять позже в настройках."))

	b.WriteString(`<p><button class="primary" type="submit">Сохранить</button></p>`)
	b.WriteString(`</form>`)

	s.render(w, r, page{Title: "Как выходить в сеть", Body: rawHTML(b.String())})
}

func (s *Server) saveFirstRun(w http.ResponseWriter, r *http.Request) {
	mode, err := parseMode(r.FormValue("mode"))
	if err != nil {
		s.renderFirstRun(w, r, err.Error())
		return
	}
	if err := s.Store.SetTransportMode(r.Context(), mode); err != nil {
		s.renderFirstRun(w, r, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// parseMode refuses anything this build cannot run.
//
// Refused here rather than at the column's CHECK constraint, so the person
// reading the message is told which choices exist instead of being shown an SQL
// error about a table they have never heard of.
func parseMode(v string) (transport.Mode, error) {
	switch v {
	case string(transport.ModeDirect):
		return transport.ModeDirect, nil
	case string(transport.ModeBlankTrail):
		return transport.ModeBlankTrail, nil
	case "":
		return "", errors.New("выберите один из двух способов")
	}
	return "", fmt.Errorf("способ %q эта сборка не знает", v)
}
