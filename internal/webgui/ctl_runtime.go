package webgui

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"supercli/internal/agent"
	"supercli/internal/buildinfo"
	"supercli/internal/system/doctor"
)

type webWorker struct {
	ID          string                `json:"id"`
	Agent       string                `json:"agent"`
	Description string                `json:"description"`
	Model       string                `json:"model,omitempty"`
	Status      string                `json:"status"`
	LastError   string                `json:"last_error,omitempty"`
	TokensIn    int                   `json:"tokens_in"`
	TokensOut   int                   `json:"tokens_out"`
	Steps       int                   `json:"steps"`
	ToolNames   []string              `json:"tool_names,omitempty"`
	Run         int                   `json:"run"`
	UpdatedAt   int64                 `json:"updated_at"`
	Activity    *agent.WorkerActivity `json:"activity,omitempty"`
	// Resumable: a saved, not-running worker that send_message can continue
	// with its full transcript (also after an app restart).
	Resumable bool `json:"resumable,omitempty"`
	// Session is the conversation the worker belongs to. Worker ids are
	// process-wide, so the GUI adopts a snapshot for a card restored from
	// history (no run number) only when it is this conversation's worker.
	Session string `json:"session,omitempty"`
	// Review: the run ended "done" but needs a look (no file changes, or a
	// red verify command); the card shows "needs review", not "done".
	Review string `json:"review,omitempty"`
	// The current or last run's start and end (server epoch ms; 0 unknown,
	// end 0 while it runs): the GUI's total working time.
	RunStartedAt int64 `json:"run_started_at,omitempty"`
	RunEndedAt   int64 `json:"run_ended_at,omitempty"`
	// Liveness of a running worker (server epoch ms; 0 when idle): its
	// current phase ("model" or "tool"), since when, the latest observable
	// progress and the running tool's live progress line.
	Phase          string `json:"phase,omitempty"`
	PhaseTool      string `json:"phase_tool,omitempty"`
	PhaseBytes     int    `json:"phase_bytes,omitempty"`
	PhaseSince     int64  `json:"phase_since,omitempty"`
	LastProgressAt int64  `json:"last_progress_at,omitempty"`
	ToolProgress   string `json:"tool_progress,omitempty"`
}

func workerDTO(s agent.Snapshot) webWorker {
	var activity *agent.WorkerActivity
	if s.Activity.Kind != "" {
		copy := s.Activity
		activity = &copy
	}
	dto := webWorker{
		ID: s.ID, Agent: s.Agent, Description: s.Description, Model: s.Model,
		Status: s.Status, LastError: s.LastError, TokensIn: s.TokensIn,
		TokensOut: s.TokensOut, Steps: s.Steps, ToolNames: s.ToolNames,
		Run: s.Runs, UpdatedAt: s.UpdatedAt.UnixMilli(), Activity: activity,
		Session: s.SessionID, Review: s.Review,
	}
	if !s.RunStartedAt.IsZero() {
		dto.RunStartedAt = s.RunStartedAt.UnixMilli()
		if !s.RunEndedAt.IsZero() {
			dto.RunEndedAt = s.RunEndedAt.UnixMilli()
		}
	}
	if live := s.Live; live.Phase != "" {
		dto.Phase, dto.PhaseTool, dto.ToolProgress = live.Phase, live.Tool, live.ToolProgress
		dto.PhaseBytes = live.ArgsBytes
		dto.PhaseSince, dto.LastProgressAt = live.PhaseSince.UnixMilli(), live.LastProgressAt.UnixMilli()
	}
	return dto
}

// handleWorkerActivity returns the running worker's current tool journal
// (the format saved with its report), so a card rebuilt after a refresh shows
// every call of the run so far. 404 when the worker has no run in progress.
func (s *Server) handleWorkerActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	sid := strings.TrimSpace(r.URL.Query().Get("session"))
	if s.eng.workers == nil || id == "" || sid == "" {
		http.Error(w, "session and id are required", http.StatusBadRequest)
		return
	}
	worker, ok := s.eng.workers.Get(id)
	if !ok || worker.Snapshot().SessionID != sid {
		http.NotFound(w, r)
		return
	}
	data, live := worker.LiveActivity()
	if !live || len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// handleWorkers lists the shared process registry or stops one active worker.
// It never invokes a model. POST accepts {"id":"worker-N","action":"stop"}.
func (s *Server) handleWorkers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows := []webWorker{}
		seen := map[string]bool{}
		sid := strings.TrimSpace(r.URL.Query().Get("session"))
		if s.eng.workers != nil {
			for _, worker := range s.eng.workers.List() {
				dto := workerDTO(worker.Snapshot())
				if sid != "" && dto.Session != "" && dto.Session != sid {
					// Another conversation's worker with the same id must not
					// settle (or hide the saved state of) this one's card.
					continue
				}
				dto.Resumable = dto.Status != "created" && dto.Status != "running"
				rows = append(rows, dto)
				seen[dto.ID] = true
			}
		}
		// Saved workers of this conversation without a live runner get a
		// definite state (never "unconfirmed" forever) and stay resumable.
		if sid != "" {
			rows = append(rows, s.eng.savedWorkerRows(r.Context(), sid, seen)...)
		}
		writeJSON(w, map[string]any{"workers": rows, "counts": s.eng.workers.Counts(), "server_now": time.Now().UnixMilli()})
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
	diagnostics := s.eng.toolDiagnostics
	s.eng.diagnosticMu.RUnlock()
	if diagnostics == nil {
		if _, err := s.eng.newLoop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.eng.diagnosticMu.RLock()
		diagnostics = s.eng.toolDiagnostics
		s.eng.diagnosticMu.RUnlock()
	}
	store, _ := s.eng.sessionStore()
	s.eng.mu.RLock()
	provider, caps := s.eng.prov, s.eng.caps
	s.eng.mu.RUnlock()
	report := doctor.Run(r.Context(), doctor.Env{
		Version: buildinfo.Version, Home: s.eng.Home(), DataDir: s.eng.DataDir(),
		Provider: provider, ToolDiagnostics: diagnostics, Sessions: store,
		ProviderMgr: s.eng.providerManager(), Caps: caps,
	})
	ok, warn, fail, skip := report.Summary()
	writeJSON(w, map[string]any{
		"generated_at": report.GeneratedAt, "version": report.Version,
		"checks":  report.Checks,
		"summary": map[string]int{"ok": ok, "warn": warn, "fail": fail, "skip": skip},
	})
}
