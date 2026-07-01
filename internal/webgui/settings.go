package webgui

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// uiSettingsFile is the on-disk name (inside the data dir) for the
// front-end UI preferences blob: theme, fonts, keybinds, language,
// notification/permission toggles, and the last-used model. These are
// presentation-only settings owned by the browser front-end, kept
// separate from config.toml (which holds backend/provider config).
const uiSettingsFile = "webgui-settings.json"

// handleUISettings persists the web GUI's UI preferences server-side so
// they survive restarts.
//
// Why this exists: the GUI runs in an app-mode browser window pointed at
// http://127.0.0.1:<port>/ where the port is OS-assigned fresh on every
// launch. localStorage is partitioned by origin (scheme+host+port), so a
// new port each restart means a brand-new, empty localStorage bucket —
// which is exactly why saved settings/fonts "don't persist." Storing the
// blob in the data dir makes it independent of port and browser profile.
//
//	GET  /api/settings -> {"settings": <object|null>}
//	POST /api/settings  body: <object>  -> {"ok": true}
//
// The payload is an opaque JSON object owned by the front-end; the server
// only validates that it is valid JSON and round-trips it verbatim.
func (s *Server) handleUISettings(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.eng.DataDir(), uiSettingsFile)
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(path)
		if err != nil {
			// Missing file (first run) is not an error: report empty.
			writeJSON(w, map[string]any{"settings": nil})
			return
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			// Corrupt file degrades to empty rather than failing the UI.
			writeJSON(w, map[string]any{"settings": nil})
			return
		}
		writeJSON(w, map[string]any{"settings": v})
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Validate it is a JSON object before persisting.
		var v map[string]any
		if err := json.Unmarshal(body, &v); err != nil {
			http.Error(w, "invalid settings JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(s.eng.DataDir(), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
