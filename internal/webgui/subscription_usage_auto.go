package webgui

import (
	"context"
	"encoding/json"
	"net/http"
)

// autoRefreshSubscriptionUsage refreshes due subscription snapshots in the
// background (see llm.SubscriptionUsageRefresher). Stats rendering still only
// reads saved snapshots. force targets the active provider after a chat run.
func (e *Engine) autoRefreshSubscriptionUsage(ctx context.Context, force bool) (refreshed, busy bool) {
	forceProvider := ""
	if force {
		e.mu.RLock()
		forceProvider = e.cfg.Provider
		e.mu.RUnlock()
	}
	return e.subUsage.RefreshDue(ctx, e.dataDir, e.providerManager().CodexAuthOptions(), forceProvider)
}

// handleSubscriptionUsageAuto is the browser's background quota refresh. It
// never reports provider errors; the GUI keeps the last snapshot as stale.
func (s *Server) handleSubscriptionUsageAuto(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req)
	}
	refreshed, busy := s.eng.autoRefreshSubscriptionUsage(r.Context(), req.Force)
	writeJSON(w, map[string]any{"refreshed": refreshed, "busy": busy})
}
