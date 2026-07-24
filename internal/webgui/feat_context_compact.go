package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

type contextCompactRequest struct {
	SessionID string `json:"session_id"`
}

type contextCompactResponse struct {
	OK      bool             `json:"ok"`
	Removed int              `json:"removed"`
	Before  int              `json:"before"`
	After   int              `json:"after"`
	Context statsContextView `json:"context"`
}

// handleContextCompact summarizes the older provider-visible projection for
// one session. The transcript table is deliberately untouched: users can still
// read, export and search the complete conversation after compaction.
func (s *Server) handleContextCompact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req contextCompactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		http.Error(w, "select a session before compacting", http.StatusBadRequest)
		return
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meta, err := store.Get(req.SessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if !sameSessionWorkspace(meta.Cwd, s.eng.Home()) {
		http.Error(w, "session not found in active project", http.StatusNotFound)
		return
	}
	initial, err := store.ReadModelContext(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	if sink := s.eng.usageCallSink(store, req.SessionID); sink != nil {
		ctx = llm.WithCallSink(ctx, sink)
	}
	loop, err := s.eng.newLoopWithSession(initial, session.NewWriter(store, req.SessionID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	event, err := loop.CompactNow(ctx)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.Canceled) {
			status = 499
		} else if strings.Contains(err.Error(), "nothing to compact") {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	report := loop.ContextReport()
	view := contextViewFromReport(report)
	s.eng.titles.Cancel(req.SessionID)
	writeJSON(w, contextCompactResponse{
		OK: true, Removed: event.Removed, Before: event.Estimated,
		After: view.EstimatedUsed, Context: view,
	})
}

func contextViewFromReport(report agent.ContextReport) statsContextView {
	view := statsContextView{Window: report.Window, EstimatedUsed: report.RequestTokens, CompactThreshold: report.CompactThreshold}
	view.Breakdown = statsContextBreakdown{
		User: report.UserTokens, Assistant: report.AssistantTokens,
		Tools: report.ToolResultTokens + report.ToolSchemaTokens + report.CatalogTokens,
		Other: report.SystemTokens,
	}
	accounted := view.Breakdown.User + view.Breakdown.Assistant + view.Breakdown.Tools + view.Breakdown.Other
	if view.EstimatedUsed < accounted {
		view.EstimatedUsed = accounted
	} else {
		// Request-only framing (freshness stamp and chat-template overhead)
		// belongs to Other so the four-part meter still sums to the request.
		view.Breakdown.Other += view.EstimatedUsed - accounted
	}
	if view.Window > 0 {
		view.Percent = int((int64(view.EstimatedUsed)*100 + int64(view.Window)/2) / int64(view.Window))
		if view.Percent > 100 {
			view.Percent = 100
		}
	}
	return view
}
