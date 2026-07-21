package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"supercli/internal/account/codexauth"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
)

type modelView struct {
	ID                  string  `json:"id"`
	Provider            string  `json:"provider"`
	Vision              bool    `json:"vision"`
	VisionKnown         bool    `json:"vision_known"`
	ToolUse             bool    `json:"tool_use"`
	Stream              bool    `json:"stream"`
	Reasoning           bool    `json:"reasoning"`
	ContextLength       int     `json:"context_length,omitempty"`
	ManualContextLength int     `json:"manual_context_length,omitempty"`
	InputCost           float64 `json:"input_cost,omitempty"`
	OutputCost          float64 `json:"output_cost,omitempty"`
	Hidden              bool    `json:"hidden"`
	Active              bool    `json:"active"`
}

type modelsResponse struct {
	Active    string        `json:"active"`
	Provider  string        `json:"provider"`
	Reasoning reasoningView `json:"reasoning"`
	Models    []modelView   `json:"models"`
}

type reasoningView struct {
	Configured string   `json:"configured"`
	Effective  string   `json:"effective"`
	Adjusted   bool     `json:"adjusted"`
	Supported  bool     `json:"supported"`
	Levels     []string `json:"levels"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	m := s.eng.providerManager()
	m.Reload()
	m.LoadHiddenState()
	active := s.eng.ModelName()
	provider, _, _ := s.eng.RuntimeSelection()

	// Auto-scan if no models cached yet (first request after startup)
	if total := cachedModelCount(m, s.eng.caps); total == 0 {
		m.ScanModels(s.eng.caps)
		m.Reload()
	}

	models := make([]modelView, 0)
	seen := map[string]struct{}{}
	for _, p := range m.Configured() {
		if !p.Disabled && p.Type == config.ProviderCodex {
			llm.RegisterCodexCatalog(s.eng.caps, p.Name)
		}
	}
	for _, p := range m.ListConfigured(s.eng.caps) {
		if p.Disabled {
			continue
		}
		for _, mi := range p.Models {
			if _, ok := seen[p.Name+"\x00"+mi.ID]; ok {
				continue
			}
			seen[p.Name+"\x00"+mi.ID] = struct{}{}
			models = append(models, toModelView(mi, p.Name, active, m.IsHiddenFor(p.Name, mi.ID), s.manualContextWindow(p.Name, mi.ID)))
		}
		if p.Model != "" {
			if !m.ModelVisible(p.Name, p.Model) {
				continue
			}
			if _, ok := seen[p.Name+"\x00"+p.Model]; ok {
				continue
			}
			mi, ok := s.eng.caps.Get(p.Model)
			if !ok {
				mi = llm.HeuristicCapabilities(p.Model)
			}
			mi.Provider = p.Name
			seen[p.Name+"\x00"+p.Model] = struct{}{}
			models = append(models, toModelView(mi, p.Name, active, m.IsHiddenFor(p.Name, p.Model), s.manualContextWindow(p.Name, p.Model)))
		}
	}
	if active != "" {
		key := provider + "\x00" + active
		if _, ok := seen[key]; !ok && active != "no model" && m.ModelVisible(provider, active) {
			mi, ok := s.eng.caps.Get(active)
			if !ok {
				mi = llm.HeuristicCapabilities(active)
			}
			if provider == "" {
				provider = mi.Provider
			}
			models = append(models, toModelView(mi, provider, active, m.IsHiddenFor(provider, active), s.manualContextWindow(provider, active)))
		}
	}
	writeJSON(w, modelsResponse{
		Active: active, Provider: provider,
		Reasoning: s.reasoningView(active),
		Models:    models,
	})
}

func (s *Server) manualContextWindow(provider, model string) int {
	if s == nil || s.eng == nil || s.eng.modelContexts == nil {
		return 0
	}
	tokens, _ := s.eng.modelContexts.Get(provider, model)
	return tokens
}

func toModelView(mi llm.ModelInfo, provider, active string, hidden bool, manualContext int) modelView {
	if provider == "" {
		provider = mi.Provider
	}
	return modelView{
		ID: mi.ID, Provider: provider, Vision: mi.Vision,
		VisionKnown: mi.Vision || mi.VisionKnown || mi.Source == llm.SourceSeed || mi.Source == llm.SourceCatalog,
		ToolUse:     mi.ToolUse,
		Stream:      mi.Stream, Reasoning: mi.Reasoning, ContextLength: mi.ContextLength,
		ManualContextLength: manualContext,
		InputCost:           mi.InputCost, OutputCost: mi.OutputCost, Hidden: hidden, Active: mi.ID == active,
	}
}

// handleModelContext stores/removes an exact provider+model working-context
// override. Value accepts 100k/1m/plain integers; auto removes only this pair.
func (s *Server) handleModelContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Value    string `json:"value"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if req.Provider == "" || req.Model == "" {
		http.Error(w, "provider and model are required", http.StatusBadRequest)
		return
	}
	if s.eng == nil || s.eng.modelContexts == nil {
		http.Error(w, "model context store is unavailable", http.StatusServiceUnavailable)
		return
	}
	tokens, automatic, err := config.ParseContextBudget(req.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if automatic {
		_, err = s.eng.modelContexts.Remove(req.Provider, req.Model)
	} else {
		err = s.eng.modelContexts.Set(req.Provider, req.Model, tokens)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"ok": true, "provider": req.Provider, "model": req.Model,
		"tokens": tokens, "automatic": automatic, "compact_at": tokens * 80 / 100,
	})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.eng.SwitchModel(strings.TrimSpace(req.Model), strings.TrimSpace(req.Provider)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "model": s.eng.ModelName()})
}

// handleModelPrice stores or removes an exact provider/model price in USD per
// million tokens. Prices are deliberately provider-scoped: a familiar model
// name routed through a custom gateway must never inherit an unrelated direct
// API price.
func (s *Server) handleModelPrice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider              string  `json:"provider"`
		Model                 string  `json:"model"`
		InputPerMillion       float64 `json:"input_per_million"`
		CachedInputPerMillion float64 `json:"cached_input_per_million"`
		OutputPerMillion      float64 `json:"output_per_million"`
		Remove                bool    `json:"remove"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if req.Provider == "" || req.Model == "" {
		http.Error(w, "provider and model are required", http.StatusBadRequest)
		return
	}
	prices := []float64{req.InputPerMillion, req.CachedInputPerMillion, req.OutputPerMillion}
	for _, price := range prices {
		if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > 1_000_000 {
			http.Error(w, "prices must be finite values between 0 and 1000000 USD per million tokens", http.StatusBadRequest)
			return
		}
	}

	m := s.eng.providerManager()
	var err error
	if req.Remove {
		err = m.RemoveProviderPrice(req.Provider, req.Model)
	} else {
		err = m.SetProviderPrice(req.Provider, req.Model, req.InputPerMillion, req.CachedInputPerMillion, req.OutputPerMillion)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.SetModelPrices(s.eng.caps)
	writeJSON(w, map[string]any{"ok": true, "provider": req.Provider, "model": req.Model, "removed": req.Remove})
}

// handleModelDefault is the EXPLICIT "set as CLI default" action: it
// writes default_model/default_provider to config.toml. Regular model
// switching in the web GUI (POST /api/model) never touches config.toml.
func (s *Server) handleModelDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.eng.SetCLIDefault(strings.TrimSpace(req.Model), strings.TrimSpace(req.Provider)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "model": req.Model})
}

func (s *Server) handleModelToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m := s.eng.providerManager()
	hidden := m.ToggleHiddenFor(strings.TrimSpace(req.Provider), strings.TrimSpace(req.Model))
	writeJSON(w, map[string]any{"ok": true, "model": req.Model, "hidden": hidden})
}

func (s *Server) handleModelVisibility(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Models []string `json:"models,omitempty"` // legacy unscoped clients
		Refs   []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"refs,omitempty"`
		Hidden bool `json:"hidden"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Models)+len(req.Refs) == 0 || len(req.Models)+len(req.Refs) > 10_000 {
		http.Error(w, "models/refs must contain between 1 and 10000 entries", http.StatusBadRequest)
		return
	}
	refs := make([]providers.ModelRef, 0, len(req.Models)+len(req.Refs))
	for _, model := range req.Models {
		refs = append(refs, providers.ModelRef{ID: model})
	}
	for _, ref := range req.Refs {
		refs = append(refs, providers.ModelRef{Provider: ref.Provider, ID: ref.Model})
	}
	changed := s.eng.providerManager().SetModelRefsHidden(refs, req.Hidden)
	writeJSON(w, map[string]any{"ok": true, "hidden": req.Hidden, "changed": changed})
}

func (s *Server) handleReasoning(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			Level string `json:"level"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		level := strings.TrimSpace(strings.ToLower(req.Level))
		if level == "off" || level == "default" {
			level = ""
		}
		if err := llm.SetReasoningEffort(level); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Persist to config.toml so it survives restart
		m := s.eng.providerManager()
		if err := m.SaveReasoningEffort(level); err != nil {
			// Non-fatal — the in-memory value is already set
			_ = err
		}
	}
	writeJSON(w, s.reasoningView(s.eng.ModelName()))
}

func (s *Server) reasoningView(model string) reasoningView {
	// Reasoning is an always-present user preference. Providers negotiate the
	// actual wire parameter and learn rejection per endpoint/model, so model
	// discovery (and its optional scan) must never gate this control.
	capability := true
	key := s.eng.ReasoningSupportKey()
	configured, effective, adjusted := llm.ReasoningEffortAdjustmentWithCapability(key, capability)
	return reasoningView{
		Configured: configured,
		Effective:  effective,
		Adjusted:   adjusted,
		Supported:  true,
		Levels:     llm.ReasoningEffortLevels,
	}
}

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
		if err := m.Add(name, strings.TrimSpace(req.Type), strings.TrimSpace(req.BaseURL), req.APIKey, strings.TrimSpace(req.Model)); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res := m.ScanProvider(name, s.eng.caps)
		if res.Err != nil {
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
		writeJSON(w, map[string]any{"ok": true, "models": res.Models})
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
		writeJSON(w, map[string]any{"ok": true, "name": name, "runtime_refreshed": runtimeRefreshed, "scan_error": errString(res.Err), "models": res.Models})
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
type logWriter struct{ prefix string }

func (lw logWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		log.Printf("%s: %s", lw.prefix, msg)
	}
	return len(p), nil
}

// ensure imports used if logWriter is the only consumer of fmt/log.
var _ = fmt.Sprintf

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	loop, err := s.eng.newLoop()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	report := loop.ContextReport()
	writeJSON(w, map[string]any{"text": agent.FormatContextReport(report), "report": report})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// cachedModelCount returns how many models are already known (cached)
// across all configured providers, without triggering a scan.
func cachedModelCount(m *providers.Manager, caps *llm.CapabilityRegistry) int {
	n := 0
	for _, p := range m.ListConfigured(caps) {
		n += len(p.Models)
	}
	return n
}
