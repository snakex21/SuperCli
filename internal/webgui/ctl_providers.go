package webgui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	m := s.eng.providerManager()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"providers": m.ListConfigured(s.eng.caps), "templates": providers.PredefinedProviders()})
	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
			Model   string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.Name)
		typ := strings.TrimSpace(req.Type)
		detectWarning := ""
		if typ == "" || strings.EqualFold(typ, "auto") {
			detected, detectErr := llm.DetectProviderProtocol(r.Context(), strings.TrimSpace(req.BaseURL), req.APIKey)
			if detectErr != nil {
				// Generic local/reseller APIs overwhelmingly use OpenAI-compatible
				// chat. Keep custom providers addable even when /models is slow or
				// absent, but tell the UI that detection was inconclusive.
				typ = "openai"
				detectWarning = "Nie udało się jednoznacznie wykryć typu API; zapisano jako OpenAI-compatible. Możesz zmienić typ podczas edycji."
			} else {
				typ = detected
			}
		}
		if err := m.Add(name, typ, strings.TrimSpace(req.BaseURL), req.APIKey, strings.TrimSpace(req.Model)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res := m.ScanProvider(name, s.eng.caps)
		if res.Err != nil {
			// A timeout is not a rejection. The provider may be a slow or
			// queueing gateway, or one that does not implement /v1/models at
			// all while serving /chat/completions perfectly well — in both
			// cases the user knows better than our stopwatch does. Rolling
			// those back left the provider permanently unaddable, so we keep
			// the entry and say plainly that discovery did not finish.
			// Definite answers (rejected key, wrong URL, unresolvable host)
			// still roll back: this must not become "accept anything".
			if llm.IsTimeoutError(res.Err) {
				warning := fmt.Sprintf("%s was added, but it did not finish listing models within %s, so no models are known yet. If it is just slow, press Rescan; the provider works as soon as it answers.", name, llm.ProviderDiscoveryTimeout)
				if detectWarning != "" {
					warning = detectWarning + " " + warning
				}
				writeJSON(w, map[string]any{
					"ok":      true,
					"type":    typ,
					"models":  res.Models,
					"warning": warning,
				})
				return
			}
			// Adding a provider is transactional from the GUI's point of view:
			// an entry that cannot be verified must not silently remain in the
			// config. This also makes a non-2xx response mean exactly what the
			// form tells the user: the provider was not added.
			if rollbackErr := m.Remove(name); rollbackErr != nil {
				http.Error(w, fmt.Sprintf("provider verification failed: %v; rollback failed: %v", res.Err, rollbackErr), http.StatusBadGateway)
				return
			}
			http.Error(w, fmt.Sprintf("provider verification failed: %v; provider was not added", res.Err), http.StatusBadGateway)
			return
		}
		response := map[string]any{"ok": true, "type": typ, "models": res.Models}
		if detectWarning != "" {
			response["warning"] = detectWarning
		}
		writeJSON(w, response)
	case http.MethodPut:
		// Partial update: name identifies the provider; only
		// non-nil fields are applied by Manager.Update. The
		// request uses *string semantics: a field omitted (or
		// JSON null) means "keep current"; a field present with
		// an empty string means "clear this value". The frontend
		// sends api_key only when the user typed something or
		// explicitly ticked "clear key".
		var req struct {
			Name     string  `json:"name"`
			Type     *string `json:"type"`
			BaseURL  *string `json:"base_url"`
			APIKey   *string `json:"api_key"`
			Model    *string `json:"model"`
			Disabled *bool   `json:"disabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if req.Type != nil && (strings.TrimSpace(*req.Type) == "" || strings.EqualFold(strings.TrimSpace(*req.Type), "auto")) {
			var currentBase, currentKey string
			for _, configured := range m.Configured() {
				if configured.Name == name {
					currentBase = configured.BaseURL
					break
				}
			}
			if req.BaseURL != nil {
				currentBase = strings.TrimSpace(*req.BaseURL)
			}
			if key, ok := m.APIKey(name); ok {
				currentKey = key
			}
			if req.APIKey != nil {
				currentKey = *req.APIKey
			}
			detected, detectErr := llm.DetectProviderProtocol(r.Context(), currentBase, currentKey)
			if detectErr != nil {
				http.Error(w, fmt.Sprintf("nie udało się wykryć typu API: %v", detectErr), http.StatusBadGateway)
				return
			}
			req.Type = &detected
		}
		// Capture the active selection before changing the provider config.
		// RuntimeSelection resolves configured providers by transport + URL, so
		// after a URL edit the old runtime may no longer match the freshly saved
		// entry. Keeping this snapshot also lets us rebuild the active transport
		// immediately instead of continuing to send requests to the old address.
		activeProvider, activeModel, _ := s.eng.RuntimeSelection()
		if req.Disabled != nil && *req.Disabled {
			if activeProvider == name {
				http.Error(w, "switch to another model before disabling the active provider", http.StatusConflict)
				return
			}
		}
		if err := m.Update(name, req.Type, req.BaseURL, req.APIKey, req.Model); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Disabled != nil {
			if err := m.SetDisabled(name, *req.Disabled); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		// Re-scan so the capability registry reflects the new
		// credentials (or the lack of them). Scan errors are
		// surfaced as a soft warning — the update itself
		// already succeeded.
		if req.Disabled != nil && *req.Disabled {
			writeJSON(w, map[string]any{"ok": true, "name": name, "disabled": true, "models": []string{}})
			return
		}
		runtimeRefreshed := false
		if activeProvider == name {
			if err := s.eng.SwitchModel(activeModel, name); err != nil {
				http.Error(w, fmt.Sprintf("provider saved but active connection could not be refreshed: %v", err), http.StatusBadGateway)
				return
			}
			runtimeRefreshed = true
		}
		res := m.ScanProvider(name, s.eng.caps)
		if req.APIKey != nil && strings.TrimSpace(*req.APIKey) == "" && res.Err == nil && len(res.Models) > 0 {
			if activeProvider == name && !m.ModelVisible(name, activeModel) {
				// Removing a gateway key must not leave the runtime pointing at a
				// now-hidden paid model that will fail with 401 on the next prompt.
				// Pick the first server-reported free model without running inference.
				_ = s.eng.SwitchModel(res.Models[0], name)
			}
		}
		response := map[string]any{"ok": true, "name": name, "runtime_refreshed": runtimeRefreshed, "scan_error": errString(res.Err), "models": res.Models}
		if req.Type != nil {
			response["type"] = strings.TrimSpace(*req.Type)
		}
		writeJSON(w, response)
	case http.MethodDelete:
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			http.Error(w, "name query parameter is required", http.StatusBadRequest)
			return
		}
		if err := m.Remove(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "name": name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProviderKeyReveal returns one stored API key for the provider edit
// form. Routing applies an additional Host + socket-peer loopback guard even
// when the rest of the GUI was explicitly started with --allow-remote.
func (s *Server) handleProviderKeyReveal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	key, ok := s.eng.providerManager().APIKey(name)
	if !ok {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"api_key": key})
}

func (s *Server) handleProviderScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	m := s.eng.providerManager()
	if name == "" {
		count := m.ScanModels(s.eng.caps)
		writeJSON(w, map[string]any{"ok": true, "count": count})
		return
	}
	res := m.ScanProvider(name, s.eng.caps)
	writeJSON(w, map[string]any{"ok": res.Err == nil, "provider": res.Provider, "models": res.Models, "error": errString(res.Err)})
}
