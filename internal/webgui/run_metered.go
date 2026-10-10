package webgui

import (
	"context"
	"net/url"
	"strings"
	"time"

	"supercli/internal/account/usagecost"
	"supercli/internal/agent"
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
	WindowSource  string
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
	tc := e.tomlConfig()
	var worker *usageIdentity
	if wcfg := e.taskWorkerConfig(e.tomlConfig()); wcfg != nil {
		id := e.usageIdentity(*wcfg, "worker")
		worker = &id
	}
	return func(s llm.CallStat) {
		id := model
		switch s.Purpose {
		case llm.PurposeTask:
			if worker != nil {
				id = *worker
			}
			id.Source = "worker"
		case llm.PurposeTitle:
			id.Source = "title"
		case "", llm.PurposeMain, "model":
			// Old GUI rows use model, while TUI rows use main.
		default:
			id.Source = s.Purpose
		}
		actual := usagecost.CallIdentity(tc, s, session.UsageRecord{Provider: id.Provider, ProviderType: id.ProviderType, EndpointHost: id.EndpointHost, Model: id.Model})
		e.recordProviderPerformance(actual.Provider, s)
		// Mirror the old wrapper's contract: only calls that produced
		// a terminal usage frame become rows (failed or usage-less
		// calls carry no billable counters). Performance still records
		// those calls above so the diagnostics panel can explain them.
		if s.TokensIn == 0 && s.TokensOut == 0 {
			return
		}
		recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		u := session.UsageRecord{
			SessionID:              sessionID,
			Provider:               actual.Provider,
			ProviderType:           actual.ProviderType,
			EndpointHost:           actual.EndpointHost,
			Model:                  actual.Model,
			Input:                  int64(s.TokensIn),
			Output:                 int64(s.TokensOut),
			CachedInput:            int64(s.TokensCached),
			Reasoning:              int64(s.TokensReasoning),
			HasCachedInput:         s.TokensCached > 0,
			HasReasoning:           s.TokensReasoning > 0,
			TTFTMS:                 s.TTFT.Milliseconds(),
			PrefillEvaluated:       int64(s.PrefillEvaluated),
			PrefillTokensPerSecond: s.PrefillTokensPerSecond,
			PrefillBudget:          s.PrefillBudget,
			PrefillBudgetSource:    s.PrefillBudgetSource,
			ContextWindow:          id.ContextWindow,
			ContextSystem:          s.Request.System,
			ContextUser:            s.Request.User,
			ContextAssistant:       s.Request.Assistant,
			ContextTool:            s.Request.Tool,
			ContextOther:           s.Request.Other,
			Source:                 id.Source,
			CreatedAt:              actual.CreatedAt,
		}
		u.DurationMS, u.HasTiming = usagecost.CallTiming(s)
		if actual.Model != id.Model || actual.Provider != id.Provider || actual.EndpointHost != id.EndpointHost {
			u.ContextWindow = 0
			u.ContextWindow = e.statsContextFromUsage(u, model).Window
		}
		price := usagecost.FreezeQuote(tc, u, false)
		u.PriceSnapshot = &price
		if err := store.AppendUsage(recordCtx, u); err == nil && config.EffectiveCostCurrency(tc) != "USD" && price.AmountUSD != nil && *price.AmountUSD > 0 {
			if rates := e.historyRates(); rates != nil {
				rates.WarmCurrency(config.EffectiveCostCurrency(tc), price.UsageDay)
			}
		}
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
	windowProvider := providerName
	e.mu.RLock()
	active := e.cfg
	e.mu.RUnlock()
	if cfg.Model == active.Model && cfg.Provider == active.Provider && cfg.BaseURL == active.BaseURL {
		// The loop scopes overrides by RuntimeSelection. Keep billing's
		// legacy provider identity independent from that window policy.
		windowProvider, _, _ = e.RuntimeSelection()
	}
	window := e.statsContextWindow(cfg, windowProvider)
	return usageIdentity{
		Provider:      providerName,
		ProviderType:  cfg.Provider,
		EndpointHost:  endpointHost(cfg.BaseURL),
		Model:         cfg.Model,
		ContextWindow: window.Tokens,
		WindowSource:  window.Source,
		Source:        source,
	}
}

// statsContextWindow mirrors the loop's scoped override and shared resolution
// cascade. Unlike a loop, a panel read needs no registry, memory or preflight.
func (e *Engine) statsContextWindow(cfg config.Config, providerName string) agent.ContextWindowResolution {
	if tokens, ok := e.modelContexts.Get(providerName, cfg.Model); ok {
		return agent.ClampContextWindowToRuntime(agent.ContextWindowResolution{Tokens: tokens, Source: "model-override"}, cfg.BaseURL, cfg.APIKey, cfg.Model)
	}
	e.mu.RLock()
	caps, learned := e.caps, e.learned
	e.mu.RUnlock()
	resolved := agent.ResolveContextWindowWithRuntime(cfg.Model, e.tomlConfig().ContextWindow, 0, caps, learned, cfg.BaseURL, cfg.APIKey)
	if resolved.Tokens <= 0 {
		return agent.ContextWindowResolution{Tokens: agent.DefaultContextWindow(), Source: "fallback"}
	}
	return resolved
}

// An older usage row may omit its context window. Resolve that row's identity,
// not an unrelated model currently selected in the GUI.
func (e *Engine) statsContextFromUsage(u session.UsageRecord, preview usageIdentity) statsContextView {
	if u.ContextWindow > 0 {
		return contextFromUsage(u)
	}
	resolved := agent.ContextWindowResolution{Tokens: preview.ContextWindow, Source: preview.WindowSource}
	if u.Model != preview.Model || u.Provider != preview.Provider || u.ProviderType != preview.ProviderType || u.EndpointHost != preview.EndpointHost {
		cfg := config.Config{Provider: u.ProviderType, Model: u.Model}
		if u.EndpointHost != "" {
			host := u.EndpointHost
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			cfg.BaseURL = "https://" + host
		}
		for _, p := range e.providerManager().Configured() {
			if p.Name == u.Provider && p.Type == u.ProviderType && endpointHost(p.BaseURL) == u.EndpointHost {
				cfg.BaseURL = p.BaseURL
				cfg.APIKey = p.APIKey
				break
			}
		}
		resolved = e.statsContextWindow(cfg, u.Provider)
	}
	u.ContextWindow = resolved.Tokens
	out := contextFromUsage(u)
	out.WindowSource = resolved.Source
	return out
}

// runtimeProviderForConfig matches RuntimeSelection against one captured
// config, so a concurrent model switch cannot join one endpoint to another
// provider's scoped budget.
func runtimeProviderForConfig(cfg config.Config, caps *llm.CapabilityRegistry, configured []config.ProviderConf) string {
	provider := ""
	for _, p := range configured {
		if p.Type == cfg.Provider && strings.TrimRight(p.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
			provider = p.Name
			if p.Model == cfg.Model {
				break
			}
		}
	}
	if provider == "" && caps != nil {
		provider = caps.Provider(cfg.Model)
	}
	if provider == "" {
		provider = cfg.Provider
	}
	return provider
}

// contextWindowConfigForProvider is built once per GUI loop. Workers use their
// own configured connection, while an unknown identity cannot borrow the
// coordinator's endpoint/credential-scoped loaded-instance capacity.
func contextWindowConfigForProvider(active config.Config, activeProvider string, configured []config.ProviderConf) func(string) config.Config {
	connections := make(map[string]config.ProviderConf, len(configured))
	for _, p := range configured {
		connections[p.Name] = p
	}
	return func(provider string) config.Config {
		provider = strings.TrimSpace(provider)
		if provider == "" || provider == activeProvider {
			return active
		}
		out := active
		out.Provider, out.BaseURL, out.APIKey = provider, "", ""
		if p, ok := connections[provider]; ok {
			out.Provider, out.BaseURL, out.APIKey = p.Type, p.BaseURL, p.APIKey
		}
		return out
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
