package webgui

import (
	"encoding/json"
	"net/http"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/system/doctor"
)

type webWorker struct {
	ID          string   `json:"id"`
	Agent       string   `json:"agent"`
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"`
	Status      string   `json:"status"`
	LastError   string   `json:"last_error,omitempty"`
	TokensIn    int      `json:"tokens_in"`
	TokensOut   int      `json:"tokens_out"`
	Steps       int      `json:"steps"`
	ToolNames   []string `json:"tool_names,omitempty"`
}

func workerDTO(s agent.Snapshot) webWorker {
	return webWorker{
		ID: s.ID, Agent: s.Agent, Description: s.Description, Model: s.Model,
		Status: s.Status, LastError: s.LastError, TokensIn: s.TokensIn,
		TokensOut: s.TokensOut, Steps: s.Steps, ToolNames: s.ToolNames,
	}
}

// handleWorkers lists the shared process registry or stops one active worker.
// It never invokes a model. POST accepts {"id":"worker-N","action":"stop"}.
func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows := []webWorker{}
		if s.eng.workers != nil {
			for _, worker := range s.eng.workers.List() {
				rows = append(rows, workerDTO(worker.Snapshot()))
			}
		}
		writeJSON(w, map[string]any{"workers": rows, "counts": s.eng.workers.Counts()})
	case http.MethodPost:
		var req struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.ToLower(strings.TrimSpace(req.Action)) != "stop" || strings.TrimSpace(req.ID) == "" {
			http.Error(w, "action must be stop and id is required", http.StatusBadRequest)
			return
		}
		if err := s.eng.workers.Stop(strings.TrimSpace(req.ID)); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleDoctor exposes the same diagnostics used by TUI /doctor. Building a
// loop once, when necessary, only assembles local tool schemas; it does not
// call a provider or start a worker.
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.eng.diagnosticMu.RLock()
	registry := s.eng.diagnosticRegistry
	s.eng.diagnosticMu.RUnlock()
	if registry == nil {
		if _, err := s.eng.newLoop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.eng.diagnosticMu.RLock()
		registry = s.eng.diagnosticRegistry
		s.eng.diagnosticMu.RUnlock()
	}
	store, _ := s.eng.sessionStore()
	s.eng.mu.RLock()
	provider, caps := s.eng.prov, s.eng.caps
	s.eng.mu.RUnlock()
	report := doctor.Run(r.Context(), doctor.Env{
		Version: "0.6.0", Home: s.eng.Home(), DataDir: s.eng.DataDir(),
		Provider: provider, Registry: registry, Sessions: store,
		ProviderMgr: s.eng.providerManager(), Caps: caps,
	})
	ok, warn, fail, skip := report.Summary()
	writeJSON(w, map[string]any{
		"generated_at": report.GeneratedAt, "version": report.Version,
		"checks":  report.Checks,
		"summary": map[string]int{"ok": ok, "warn": warn, "fail": fail, "skip": skip},
	})
}
