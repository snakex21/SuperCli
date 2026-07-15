package webgui

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"supercli/internal/system/config"
)

// orchestratorView is the JSON shape for the orchestrator toggle. Set
// reports the legacy boolean view of the tri-state config value. The main
// settings API is authoritative for auto/always/never; this endpoint remains
// for backwards compatibility with older clients.
type orchestratorView struct {
	Enabled bool `json:"enabled"`
	Set     bool `json:"set"`
}

// handleOrchestrator is a GET/POST endpoint for the hard-delegation
// switch. GET reads the current config.toml value; POST persists a new
// one. Like the CLI /orchestrator command, the change is a GLOBAL,
// explicit setting that takes effect on the NEXT launch (a new session),
// because swapping the tool registry mid-session would break the KV
// prompt cache — so the loop is not rebuilt here.
func (s *Server) handleOrchestrator(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.eng.dataDir, "config.toml")

	if r.Method == http.MethodPost {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Missing file loads as a zero config; a real error means the
		// file is unreadable — do not save a zero struct over it (that
		// would wipe providers and default_model).
		tc, err := config.LoadToml(path)
		if err != nil {
			http.Error(w, "load config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		v := req.Enabled
		tc.Orchestrator = &v
		if err := config.SaveToml(path, tc); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, orchestratorView{Enabled: v, Set: true})
		return
	}

	tc, err := config.LoadToml(path)
	if err != nil {
		writeJSON(w, orchestratorView{Enabled: false, Set: false})
		return
	}
	if tc.Orchestrator == nil {
		writeJSON(w, orchestratorView{Enabled: false, Set: false})
		return
	}
	writeJSON(w, orchestratorView{Enabled: *tc.Orchestrator, Set: true})
}
