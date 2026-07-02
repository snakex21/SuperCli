package webgui

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// uiSettingsFile is the on-disk name (inside the data dir) for the
// front-end UI preferences blob: theme, fonts, keybinds, language,
// notification/permission toggles, and the last-used model. These are
// presentation-only settings owned by the browser front-end, kept
// separate from config.toml (which holds backend/provider config).
const uiSettingsFile = "webgui-settings.json"

// lastModelKey is the key inside the settings blob that carries the
// web GUI's own active model. The value is a JSON-encoded string
// (`"{\"id\":...,\"provider\":...}"`) because the front-end mirrors
// localStorage entries verbatim, and localStorage values are strings.
const lastModelKey = "supercli-last-model"

// uiSettingsMu serializes read-modify-write access to the settings
// file between the /api/settings handler and the server-side
// last-model writer.
var uiSettingsMu sync.Mutex

// lastModelEntry is the decoded shape of the lastModelKey value.
type lastModelEntry struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// LastModel reads the web GUI's own persisted model selection from
// webgui-settings.json in dataDir. Returns empty strings when the
// file, the key, or the value is missing/corrupt — callers fall back
// to config.toml. This is the web GUI's model memory: it is owned by
// the web front-end and deliberately independent from the CLI's
// default_model in config.toml.
func LastModel(dataDir string) (model, provider string) {
	uiSettingsMu.Lock()
	defer uiSettingsMu.Unlock()
	blob := readUISettings(dataDir)
	raw, ok := blob[lastModelKey].(string)
	if !ok || raw == "" {
		return "", ""
	}
	var e lastModelEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return "", ""
	}
	return e.ID, e.Provider
}

// saveLastModel persists the web GUI's active model into
// webgui-settings.json, preserving every other key in the blob. Errors
// are returned so SwitchModel can surface a failed persist, but a
// missing/corrupt existing file just degrades to a fresh blob.
func saveLastModel(dataDir, modelID, providerName string) error {
	uiSettingsMu.Lock()
	defer uiSettingsMu.Unlock()
	blob := readUISettings(dataDir)
	val, err := json.Marshal(lastModelEntry{ID: modelID, Provider: providerName})
	if err != nil {
		return err
	}
	blob[lastModelKey] = string(val)
	data, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, uiSettingsFile), data, 0o644)
}

// readUISettings loads the settings blob, degrading to an empty map
// on any error. Callers must hold uiSettingsMu.
func readUISettings(dataDir string) map[string]any {
	blob := map[string]any{}
	data, err := os.ReadFile(filepath.Join(dataDir, uiSettingsFile))
	if err != nil {
		return blob
	}
	if err := json.Unmarshal(data, &blob); err != nil {
		return map[string]any{}
	}
	return blob
}

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
	uiSettingsMu.Lock()
	defer uiSettingsMu.Unlock()
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
