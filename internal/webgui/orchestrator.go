package webgui

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"supercli/internal/system/config"
)

// orchestratorView is the JSON shape for the orchestrator toggle. Set
// reports the tri-state config value (nil = default OFF), Enabled is the
// resolved boolean the next launch will use.
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
		tc, err := config.LoadToml(path)
		if err != nil {
			tc = config.TomlConfig{}
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
