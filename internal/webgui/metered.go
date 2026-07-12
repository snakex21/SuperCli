package webgui

import (
	"context"
	"net/url"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type usageIdentity struct {
	Provider      string
	ProviderType  string
	EndpointHost  string
	Model         string
	ContextWindow int
	Source        string
}

// usageCallSink records provider-reported usage after each real model
// call as one metered-usage row per call. It is an llm.CallSink
// attached to the run context (llm.WithCallSink), so the single
// factory-built metered provider reports here without a second
// wrapper. It stores only counters and request-shape estimates, never
// prompts, URLs, headers, or credentials.
//
// One sink covers every call of the request: the coordinator's own
// steps ("model"), delegated worker calls (purpose "task" → source
// "worker", labeled with the task_model backend identity when
// configured) and background title summaries (purpose "title").
func (e *Engine) usageCallSink(store *session.Store, sessionID string) llm.CallSink {
	if store == nil || sessionID == "" {
		return nil
	}
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	model := e.usageIdentity(cfg, "model")
	var worker *usageIdentity
	if wcfg := e.taskWorkerConfig(e.tomlConfig()); wcfg != nil {
		id := e.usageIdentity(*wcfg, "worker")
		worker = &id
	}
	return func(s llm.CallStat) {
		// Mirror the old wrapper's contract: only calls that produced
		// a terminal usage frame become rows (failed or usage-less
		// calls carry no billable counters).
		if s.TokensIn == 0 && s.TokensOut == 0 {
			return
		}
		id := model
		switch s.Purpose {
		case llm.PurposeTask:
			if worker != nil {
				id = *worker
			}
			id.Source = "worker"
		case llm.PurposeTitle:
			id.Source = "title"
		}
		recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = store.AppendUsage(recordCtx, session.UsageRecord{
			SessionID:        sessionID,
			Provider:         id.Provider,
			ProviderType:     id.ProviderType,
			EndpointHost:     id.EndpointHost,
			Model:            s.Model,
			Input:            int64(s.TokensIn),
			Output:           int64(s.TokensOut),
			CachedInput:      int64(s.TokensCached),
			Reasoning:        int64(s.TokensReasoning),
			HasCachedInput:   s.TokensCached > 0,
			HasReasoning:     s.TokensReasoning > 0,
			ContextWindow:    id.ContextWindow,
			ContextSystem:    s.Request.System,
			ContextUser:      s.Request.User,
			ContextAssistant: s.Request.Assistant,
			ContextTool:      s.Request.Tool,
			ContextOther:     s.Request.Other,
			Source:           id.Source,
		})
	}
}

func endpointHost(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func (e *Engine) usageIdentity(cfg config.Config, source string) usageIdentity {
	providerName := e.providerNameForConfig(cfg)
	window := 0
	if tc := e.tomlConfig(); tc.ContextWindow > 0 {
		window = tc.ContextWindow
	}
	if window == 0 && e.caps != nil {
		if info, ok := e.caps.Get(cfg.Model); ok {
			window = info.ContextLength
		}
		if window == 0 && providerName != "" {
			if info, ok := e.caps.Get(providerName + "/" + cfg.Model); ok {
				window = info.ContextLength
			}
		}
	}
	return usageIdentity{
		Provider:      providerName,
		ProviderType:  cfg.Provider,
		EndpointHost:  endpointHost(cfg.BaseURL),
		Model:         cfg.Model,
		ContextWindow: window,
		Source:        source,
	}
}

func (e *Engine) providerNameForConfig(cfg config.Config) string {
	lastModel, lastProvider := LastModel(e.dataDir)
	configured := e.providerManager().Configured()
	if lastModel == cfg.Model && lastProvider != "" {
		for _, p := range configured {
			if p.Name == lastProvider && p.Type == cfg.Provider && strings.TrimRight(p.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
				return p.Name
			}
		}
	}
	for _, p := range configured {
		if p.Type == cfg.Provider && strings.TrimRight(p.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
			return p.Name
		}
	}
	return cfg.Provider
}
