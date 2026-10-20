package webgui

import (
	"context"
	"net/http"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/system/updater"
)

// handleUpdateNotice serves the background new-release notice. It checks at
// most once a day (cached in the data dir), never downloads, and reports
// "disabled" when update_notify is off or for branded launchers.
func (s *Server) handleUpdateNotice(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	disabled := map[string]any{"status": "disabled", "available": false}
	if s.runtimeAppName() != "SuperCli" {
		writeJSON(w, disabled)
		return
	}
	global, _ := config.FindTomlPaths(s.eng.DataDir(), s.eng.Home())
	tc, err := config.LoadToml(global)
	if err != nil || !config.UpdateNotifyEnabled(tc) {
		writeJSON(w, disabled)
		return
	}
	check := func(ctx context.Context) (updater.State, error) {
		var manager applicationUpdater
		var err error
		if s.updateFactory != nil {
			manager, err = s.updateFactory()
		} else {
			manager, err = updater.New()
		}
		if err != nil {
			return updater.State{}, err
		}
		return manager.Check(ctx)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	state, err := updater.CachedCheck(ctx, s.eng.DataDir(), check)
	if err != nil {
		// Offline or rate limited: stay silent, the manual check still works.
		writeJSON(w, map[string]any{"status": "unchecked", "available": false})
		return
	}
	writeJSON(w, state)
}
