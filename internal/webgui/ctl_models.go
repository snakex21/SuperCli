package webgui

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"

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
