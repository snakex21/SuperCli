package webgui

import (
	"net/http"

	"supercli/internal/account/claudeauth"
	"supercli/internal/system/browser"
	"supercli/internal/system/config"
)

// Login holds a single cancellable request until the OAuth callback arrives.
// The UI receives completion directly instead of polling an account endpoint.
func (s *Server) handleClaudeLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	language := r.URL.Query().Get("lang")
	if language == "" {
		language = s.eng.tomlConfig().Language
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	mgr := claudeauth.NewManager(s.eng.dataDir, claudeauth.Options{Language: language, ReturnURL: scheme + "://" + r.Host + "/"})
	if err := mgr.Login(r.Context(), browser.Open); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pm := s.eng.providerManager()
	name, err := pm.EnsureClaudeProvider()
	if err != nil {
		writeJSON(w, map[string]any{"ok": true, "logged_in": true, "setup_error": "Claude login saved; provider setup failed"})
		return
	}
	response := map[string]any{"ok": true, "logged_in": true, "provider": name}
	if name != "" {
		scan := pm.ScanProvider(name, s.eng.caps)
		response["models"] = scan.Models
		if scan.Err != nil {
			response["scan_error"] = scan.Err.Error()
		}
	}
	if usage, usageErr := mgr.RefreshUsage(r.Context()); usageErr == nil {
		response["usage"] = usage
	} else {
		response["usage_error"] = usageErr.Error()
	}
	writeJSON(w, response)
}

func (s *Server) handleClaudeAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.claudeAccountView())
}

func (s *Server) claudeAccountView() map[string]any {
	mgr := claudeauth.NewManager(s.eng.dataDir, claudeauth.Options{})
	loggedIn := mgr.LoggedIn()
	out := map[string]any{"logged_in": loggedIn}
	if !loggedIn {
		return out
	}
	out["usage"] = mgr.Usage()
	for _, p := range s.eng.providerManager().ListConfigured(s.eng.caps) {
		if p.Type != config.ProviderClaude {
			continue
		}
		out["provider"] = p.Name
		models := []string{}
		for _, m := range p.Models {
			models = append(models, m.ID)
		}
		out["models"] = models
		_, active, _ := s.eng.RuntimeSelection()
		out["active_model"] = active
		break
	}
	return out
}

func (s *Server) handleClaudeUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, err := claudeauth.NewManager(s.eng.dataDir, claudeauth.Options{}).RefreshUsage(r.Context())
	out := s.claudeAccountView()
	out["ok"] = err == nil
	if err != nil {
		out["usage_error"] = err.Error()
	}
	writeJSON(w, out)
}

func (s *Server) handleClaudeLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := claudeauth.NewManager(s.eng.dataDir, claudeauth.Options{}).Logout(); err != nil {
		http.Error(w, "could not remove Claude credentials from the portable data directory", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
