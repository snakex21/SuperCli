package webgui

import (
	"encoding/json"
	"net/http"
	"strings"

	"supercli/internal/llm"
)

type reasoningSession struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	RuntimeKnown    bool   `json:"runtime_known"`
}

type reasoningResponse struct {
	reasoningView
	Session *reasoningSession `json:"session,omitempty"`
	Warning string            `json:"warning,omitempty"`
}

func (s *Server) handleReasoning(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var response reasoningResponse
	if r.Method == http.MethodPost {
		var req struct {
			Level     string `json:"level"`
			SessionID string `json:"session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		level := strings.TrimSpace(strings.ToLower(req.Level))
		if level == "off" || level == "default" {
			level = ""
		}
		if err := llm.ValidateReasoningEffort(level); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if id := strings.TrimSpace(req.SessionID); id != "" {
			store, err := s.eng.sessionStore()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			meta, err := store.Get(id)
			if err != nil || !sameSessionWorkspace(meta.Cwd, s.eng.Home()) {
				http.Error(w, "session not found in active project", http.StatusNotFound)
				return
			}
			provider, model, _ := s.eng.RuntimeSelection()
			if err := store.SetRuntime(id, provider, model, level); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			response.Session = &reasoningSession{ID: id, Provider: provider, Model: model, ReasoningEffort: level, RuntimeKnown: true}
		}
		_ = llm.SetReasoningEffort(level) // validated before changing session or runtime
		if err := s.eng.providerManager().SaveReasoningEffort(level); err != nil {
			// The selection is already applied; report persistence failure
			// without presenting a failed request that did nothing.
			response.Warning = "Reasoning applied, but config.toml could not be saved: " + err.Error()
		}
	}
	s.eng.ensureLocalReasoningMetadata(r.Context())
	response.reasoningView = s.reasoningView(s.eng.ModelName())
	writeJSON(w, response)
}

func (s *Server) reasoningView(model string) reasoningView {
	s.eng.mu.RLock()
	provider := s.eng.prov
	s.eng.mu.RUnlock()
	return llm.ProviderReasoningState(provider)
}
