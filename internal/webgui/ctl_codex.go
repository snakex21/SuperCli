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
	Label           string                  `json:"label"`
	LoggedIn        bool                    `json:"logged_in"`
	AccountID       string                  `json:"account_id,omitempty"`
	Email           string                  `json:"email,omitempty"`
	PlanType        string                  `json:"plan_type,omitempty"`
	LastRefresh     string                  `json:"last_refresh,omitempty"`
	Limits          *llm.CodexRateLimits    `json:"limits,omitempty"`
	Usage           *llm.CodexUsageSnapshot `json:"usage,omitempty"`
	UsageError      string                  `json:"usage_error,omitempty"`
	LoginInProgress bool                    `json:"login_in_progress,omitempty"`
	LoginError      string                  `json:"login_error,omitempty"`
}

func (s *Server) codexUsageViews(accounts []llm.CodexAccountUsage) ([]codexAccountView, []string) {
	out := make([]codexAccountView, 0, len(accounts))
	labels := make([]string, 0, len(accounts))
	for _, a := range accounts {
		v := codexAccountView{Label: a.Label, LoggedIn: a.LoggedIn, AccountID: a.AccountID, Email: a.Email, PlanType: a.PlanType, LastRefresh: a.LastRefresh, Usage: a.Usage, UsageError: a.UsageError}
		if a.LoggedIn && a.AccountID != "" {
			if rl, ok := llm.LoadCodexRateLimitsSnapshot(s.eng.dataDir, a.AccountID); ok {
				v.Limits = &rl
			}
		}
		v.LoginInProgress = s.codexLoginInProgress(a.Label)
		if err := s.codexLoginErr(a.Label); err != nil {
			v.LoginError = err.Error()
		}
		labels = append(labels, a.Label)
		out = append(out, v)
	}
	return out, labels
}

func (s *Server) handleCodexAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, summary, err := llm.ListCodexAccountUsageWithOptions(s.eng.dataDir, s.eng.providerManager().CodexAuthOptions())
	if err != nil {
		http.Error(w, "could not list Codex accounts", http.StatusInternalServerError)
		return
	}
	out, labels := s.codexUsageViews(accounts)
	writeJSON(w, map[string]any{"accounts": out, "usage_summary": summary, "pending_logins": s.codexPendingLogins(labels)})
}

// handleCodexUsage is an explicit manual refresh. Cached GETs never call it.
func (s *Server) handleCodexUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Label string `json:"label"`
		All   bool   `json:"all"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid usage request", http.StatusBadRequest)
		return
	}
	accounts, summary, err := llm.RefreshCodexAccountUsageWithOptions(r.Context(), s.eng.dataDir, req.Label, req.All, s.eng.providerManager().CodexAuthOptions())
	if accounts == nil {
		accounts, summary, _ = llm.ListCodexAccountUsageWithOptions(s.eng.dataDir, s.eng.providerManager().CodexAuthOptions())
	}
	out, labels := s.codexUsageViews(accounts)
	result := map[string]any{"ok": err == nil, "accounts": out, "usage_summary": summary, "pending_logins": s.codexPendingLogins(labels)}
	if err != nil {
		result["error"] = "could not refresh all requested account usage"
	}
	writeJSON(w, result)
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
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, s.eng.providerManager().CodexAuthOptions())
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
			log.Printf("webgui: codex login %q failed after %s", label, time.Since(started))
		} else {
			log.Printf("webgui: codex login %q succeeded after %s", label, time.Since(started))
			pm := s.eng.providerManager()
			if name, setupErr := pm.EnsureCodexProvider(); setupErr != nil {
				log.Printf("webgui: codex login: provider setup failed")
			} else if name != "" {
				if scan := pm.ScanProvider(name, s.eng.caps); scan.Err != nil {
					log.Printf("webgui: codex login: model discovery unavailable")
				}
			}
			if reloadErr := s.eng.reloadCodexAccounts(); reloadErr != nil {
				log.Printf("webgui: codex login: provider rebuild failed")
			}
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
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, s.eng.providerManager().CodexAuthOptions())
	info, _ := mgr.Account()
	if err := mgr.Logout(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Also drop the saved usage snapshot so the UI does not keep
	// showing the logged-out account's rate limits.
	if err := llm.ClearCodexAccountRateLimits(s.eng.dataDir, info.AccountID); err != nil {
		log.Printf("webgui: codex logout %q: clear usage snapshot: %v", label, err)
	}
	if err := s.eng.reloadCodexAccounts(); err != nil {
		log.Printf("webgui: codex logout: provider rebuild failed")
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
	mgr := codexauth.NewManagerFor(s.eng.dataDir, label, s.eng.providerManager().CodexAuthOptions())
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
	if err := s.eng.reloadCodexAccounts(); err != nil {
		log.Printf("webgui: codex refresh: provider rebuild failed")
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
