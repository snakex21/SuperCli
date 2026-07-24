package webgui

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/llm"
)

type codexAccountView struct {
	Label           string               `json:"label"`
	LoggedIn        bool                 `json:"logged_in"`
	AccountID       string               `json:"account_id,omitempty"`
	Email           string               `json:"email,omitempty"`
	PlanType        string               `json:"plan_type,omitempty"`
	LastRefresh     string               `json:"last_refresh,omitempty"`
	Limits          *llm.CodexRateLimits `json:"limits,omitempty"`
	LoginInProgress bool                 `json:"login_in_progress,omitempty"`
	LoginError      string               `json:"login_error,omitempty"`
}

func (s *Server) handleCodexAccounts(w http.ResponseWriter, r *http.Request) {
	labels, _ := codexauth.ListAccounts(s.eng.dataDir)
	if len(labels) == 0 {
		labels = []string{codexauth.DefaultAccount}
	}
	out := make([]codexAccountView, 0, len(labels))
	for _, label := range labels {
		mgr := codexauth.NewManagerFor(s.eng.dataDir, label, codexauth.Options{})
		info, _ := mgr.Account()
		v := codexAccountView{Label: label, LoggedIn: info.LoggedIn, AccountID: info.AccountID, Email: info.Email, PlanType: info.PlanType}
		if !info.LastRefresh.IsZero() {
			v.LastRefresh = info.LastRefresh.Format(time.RFC3339)
		}
		if rl, ok := llm.LoadCodexRateLimitsSnapshot(s.eng.dataDir, info.AccountID); ok {
			v.Limits = &rl
		}
		// Surface in-progress / recently-finished login state so the
		// frontend can show "logging in…" or the last error without
		// having to track it client-side.
		v.LoginInProgress = s.codexLoginInProgress(label)
		if err := s.codexLoginErr(label); err != nil {
			v.LoginError = err.Error()
		}
		out = append(out, v)
	}
	// pending_logins exposes login attempts for labels that do NOT
	// yet have an auth file (e.g. the very first /login for a
	// brand-new account). Without this the frontend's poll loop
	// would stop immediately because none of the existing account
	// rows would carry login_in_progress — and the UI would never
	// refresh once the OAuth callback finally lands.
	pending := s.codexPendingLogins(labels)
	writeJSON(w, map[string]any{
		"accounts":       out,
		"pending_logins": pending,
	})
}

// codexPendingLogins returns the labels of in-progress logins
// whose label is NOT already in known (the labels we just
// enumerated from disk). Those logins would otherwise be
// invisible to the frontend.
func (s *Server) codexPendingLogins(known []string) []string {
	knownSet := make(map[string]struct{}, len(known))
	for _, l := range known {
		knownSet[l] = struct{}{}
	}
	s.codexLoginMu.Lock()
	defer s.codexLoginMu.Unlock()
	var out []string
	for label, st := range s.codexLogins {
		if !st.inProgress {
			continue
		}
		if _, ok := knownSet[label]; ok {
			continue // already surfaced via the per-account field
		}
		out = append(out, label)
	}
	return out
}

// handleCodexLogin starts an asynchronous OAuth login for a Codex
// account. The handler returns immediately with {started:true}; the
// frontend polls /api/codex/accounts to detect the LoggedIn flip or
// a login_error. A login is rejected if one is already in progress
// for the same label, or if the environment is headless (no browser
// can be opened).
func (s *Server) handleCodexLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = codexauth.DefaultAccount
	}
	if codexauth.IsHeadless() {
		http.Error(w, "login is unavailable in headless mode — run supercli from a desktop session", http.StatusBadRequest)
		return
	}
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, codexauth.Options{})
	finish, ok := s.startCodexLogin(label)
	if !ok {
		writeJSON(w, map[string]any{"ok": true, "started": false, "busy": true, "label": label})
		return
	}
	// Run Login() in a goroutine with a hard cap. The OAuth flow
	// involves a browser round-trip, so 5 minutes is the same
	// ceiling the TUI uses for interactive sessions.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		started := time.Now()
		_, err := mgr.Login(ctx, logWriter{prefix: "codex login [" + label + "]"})
		if err != nil {
			log.Printf("webgui: codex login %q failed after %s: %v", label, time.Since(started), err)
		} else {
			log.Printf("webgui: codex login %q succeeded after %s", label, time.Since(started))
		}
		finish(err)
	}()
	writeJSON(w, map[string]any{"ok": true, "started": true, "label": label})
}

// handleCodexLogout removes the stored auth.json for a Codex account.
// Default label is used when the request body omits one. Returns
// {ok:true} even when there was nothing to remove (idempotent).
func (s *Server) handleCodexLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = codexauth.DefaultAccount
	}
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, codexauth.Options{})
	if err := mgr.Logout(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Also drop the saved usage snapshot so the UI does not keep
	// showing the logged-out account's rate limits.
	if err := llm.ClearCodexRateLimits(s.eng.dataDir); err != nil {
		log.Printf("webgui: codex logout %q: clear usage snapshot: %v", label, err)
	}
	writeJSON(w, map[string]any{"ok": true, "label": label})
}

// handleCodexRefresh forces a token refresh for a Codex account.
// Useful when the user suspects the cached access token has gone
// stale (e.g. plan changed on the ChatGPT side).
func (s *Server) handleCodexRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Label string `json:"label"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = codexauth.DefaultAccount
	}
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, codexauth.Options{})
	if !mgr.LoggedIn() {
		http.Error(w, "not logged in", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := mgr.Refresh(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, _ := mgr.Account()
	v := codexAccountView{Label: label, LoggedIn: info.LoggedIn, AccountID: info.AccountID, Email: info.Email, PlanType: info.PlanType}
	if !info.LastRefresh.IsZero() {
		v.LastRefresh = info.LastRefresh.Format(time.RFC3339)
	}
	writeJSON(w, map[string]any{"ok": true, "account": v})
}

// logWriter is a thin io.Writer adapter around the standard logger.
// It exists so codexauth.Manager.Login can stream human-readable
// progress lines (which it writes to its status writer) into the
// server log for diagnostics.
