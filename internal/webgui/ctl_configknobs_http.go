package webgui

import (
	"encoding/json"
	"net/http"
	"strings"

	"supercli/internal/system/config"
)

func (s *Server) handleConfigKnobs(w http.ResponseWriter, r *http.Request) {
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())

	if r.Method == http.MethodPost {
		var req struct {
			Key      string `json:"key"`
			Value    string `json:"value"`
			ResetAll bool   `json:"reset_all"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		// A load error means the file exists but is unreadable/corrupt
		// (a missing file loads as a clean zero config). Saving a zero
		// struct over it would silently wipe providers, API keys and
		// default_model — refuse instead.
		tc, err := config.LoadToml(global)
		if err != nil {
			http.Error(w, "load config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if req.ResetAll {
			knobResetAll(&tc)
		} else if err := knobSet(&tc, strings.TrimSpace(req.Key), req.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := config.SaveToml(global, tc); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	tc, err := config.LoadToml(global)
	if err != nil {
		tc = config.TomlConfig{}
	}
	out := make([]knobView, 0, len(knobDefs()))
	for _, d := range knobDefs() {
		value, source, raw := knobValue(&tc, d.key)
		out = append(out, knobView{
			Key: d.key, Label: d.key, Desc: d.desc, Kind: d.kind,
			Value: value, Source: source, Raw: raw,
			State: knobState(&tc, d.key), Default: knobDefault(d.key), NextSession: d.nextSession,
		})
	}
	writeJSON(w, map[string]any{"knobs": out})
}
