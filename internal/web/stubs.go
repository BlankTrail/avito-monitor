// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "net/http"

// stubs.go holds the handlers whose screens are later tasks in this plan.
//
// Present so the router compiles and the shell's own tests can run. Each is
// deleted by the task that writes the real one; a stub still here when the plan
// is finished is a screen somebody skipped, which is why they say so out loud
// rather than rendering an empty page.
func (s *Server) todo(w http.ResponseWriter, r *http.Request, screen string) {
	s.render(w, r, page{
		Title: screen,
		Body:  rawHTML(note("warning", "Этот экран ещё не построен: "+screen)),
	})
}

func (s *Server) watchesPage(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Наблюдения")
}
func (s *Server) saveWatch(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Наблюдения")
}
func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Наблюдения")
}
func (s *Server) toggleWatch(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Наблюдения")
}
func (s *Server) firstRunPage(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Первый запуск")
}
func (s *Server) saveFirstRun(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Первый запуск")
}
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Настройки")
}
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Настройки")
}

func (s *Server) checkBlankTrail(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Настройки")
}
func (s *Server) checkTelegram(w http.ResponseWriter, r *http.Request) {
	s.todo(w, r, "Настройки")
}
func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request)  { s.todo(w, r, "Прокси") }
func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request)   { s.todo(w, r, "Прокси") }
func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) { s.todo(w, r, "Прокси") }
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request)   { s.todo(w, r, "Прокси") }
func (s *Server) listGateways(w http.ResponseWriter, r *http.Request)  { s.todo(w, r, "Прокси") }
