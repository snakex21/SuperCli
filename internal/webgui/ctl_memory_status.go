package webgui

import (
	"net/http"

	"supercli/internal/system/config"
)

// memoryStatus is the effective persistent-memory switch for the active
// workspace, as GET /api/memory/status reports it. The GUI shows a "memory
// off" indicator from it; source tells where the value comes from (flag, env,
// project, config, default) so a project override is not mistaken for the
// global setting.
type memoryStatus struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
}

func (e *Engine) memoryStatusAt(home string) memoryStatus {
	enabled, source := config.EffectiveMemoryAt(e.DataDir(), home)
	return memoryStatus{Enabled: enabled, Source: source}
}

func (s *Server) handleMemoryStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.eng.memoryStatusAt(s.eng.Home()))
}
