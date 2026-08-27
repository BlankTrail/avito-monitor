// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"

	"github.com/BlankTrail/avito-monitor/internal/ru"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// /healthz: the one endpoint a supervisor reads.
//
// The spec is precise about why it exists and what it must not do. When every
// enabled watch is halted, this install is not collecting anything and a
// process manager should know — but the program must **not exit**, because a
// process that exits is restarted by systemd into the same wall for ever, and
// because the panel is the only place the reason can be read. So it stays up
// and fails its readiness instead.
//
// Not behind the password, deliberately. A health endpoint a supervisor cannot
// read is a health endpoint that does nothing, and what it discloses is the
// shape of the installation's trouble rather than any of its data: how many
// watches are halted and the site's own marker for why. Somebody who can reach
// this port can already reach the login prompt.

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	watches, err := s.Store.Watches(r.Context())
	if err != nil {
		// The database is the one thing this endpoint cannot answer without.
		http.Error(w, "база недоступна: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	enabled, halted := 0, 0
	var reasons []string
	for _, watch := range watches {
		if !watch.Enabled {
			continue
		}
		enabled++
		if watch.State != "halted" {
			continue
		}
		halted++
		reasons = append(reasons, reasonLine(watch))
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	switch {
	case enabled == 0:
		// Nothing to collect is not a fault. An install somebody has not
		// finished setting up must not read as broken to a supervisor that
		// would then restart it hourly.
		fmt.Fprintln(w, "ok: наблюдений нет")

	case halted == enabled:
		// Every watch is behind a wall. Named rather than counted: «3 halted»
		// tells a supervisor to restart, and restarting is the one thing that
		// cannot help.
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "не собирает: все %d %s остановлены\n", enabled,
			ru.Plural(enabled, "наблюдение", "наблюдения", "наблюдений"))
		for _, line := range reasons {
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Перезапуск не поможет: остановку снимает человек, "+
			"в панели или командой avitomon watch resume.")

	default:
		fmt.Fprintf(w, "ok: собирает %d из %d\n", enabled-halted, enabled)
		for _, line := range reasons {
			fmt.Fprintln(w, line)
		}
	}
}

func reasonLine(w store.Watch) string {
	reason := w.HaltedReason
	if reason == "" {
		reason = "без указанной причины"
	}
	return fmt.Sprintf("  наблюдение %d «%s»: %s", w.ID, w.Name, reason)
}
