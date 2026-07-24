package webgui

import (
	"encoding/json"
	"net/http"
	"strings"

	"supercli/internal/llm"
)

func (s *Server) handleReasoning(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		level := strings.TrimSpace(strings.ToLower(req.Level))
		if level == "off" || level == "default" {
			level = ""
		}
		if err := llm.SetReasoningEffort(level); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Persist to config.toml so it survives restart
		m := s.eng.providerManager()
		if err := m.SaveReasoningEffort(level); err != nil {
			// Non-fatal — the in-memory value is already set
			_ = err
		}
	}
	writeJSON(w, s.reasoningView(s.eng.ModelName()))
}

func (s *Server) reasoningView(model string) reasoningView {
	// Reasoning is an always-present user preference. Providers negotiate the
	// actual wire parameter and learn rejection per endpoint/model, so model
	// discovery (and its optional scan) must never gate this control.
	capability := true
	key := s.eng.ReasoningSupportKey()
	configured, effective, adjusted := llm.ReasoningEffortAdjustmentWithCapability(key, capability)
	return reasoningView{
		Configured: configured,
		Effective:  effective,
		Adjusted:   adjusted,
		Supported:  true,
		Levels:     llm.ReasoningEffortLevels,
	}
}
