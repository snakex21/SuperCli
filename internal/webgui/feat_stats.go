package webgui

import (
	"context"
	"strings"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/account/usagecost"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type statsView struct {
	// Compatibility fields used by the compact sidebar.
	Model        string `json:"model"`
	SessionToken int64  `json:"session_tokens"`
	DailyToken   int64  `json:"daily_tokens"`

	Session       statsSessionView       `json:"session"`
	Tokens        statsTokensView        `json:"tokens"`
	Context       statsContextView       `json:"context"`
	ActiveContext statsActiveContextView `json:"active_context"`
	Cost          statsCostView          `json:"cost"`
	// Pricing is the current editable USD quote for this session's model.
	// Cost keeps the immutable historical amounts and rates.
	Pricing         statsCostView            `json:"pricing"`
	Telemetry       statsTelemetryView       `json:"telemetry"`
	LastTurn        *statsLastTurnView       `json:"last_turn,omitempty"`
	GenerationSpeed statsGenerationSpeedView `json:"generation_speed"`
}

// ActiveContext describes the model selected for future requests. The separate
// Context field keeps the last main request's estimate and its own denominator.
type statsActiveContextView struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	Window           int    `json:"window"`
	WindowSource     string `json:"window_source"`
	CompactThreshold int    `json:"compact_threshold"`
}

func (e *Engine) statsActiveContext(cfg config.Config) statsActiveContextView {
	// Match RuntimeSelection using this request's captured config, rather than
	// rereading e.cfg while another browser request may be switching models.
	provider := ""
	for _, p := range e.providerManager().Configured() {
		if p.Type == cfg.Provider && strings.TrimRight(p.BaseURL, "/") == strings.TrimRight(cfg.BaseURL, "/") {
			provider = p.Name
			if p.Model == cfg.Model {
				break
			}
		}
	}
	if provider == "" {
		e.mu.RLock()
		caps := e.caps
		e.mu.RUnlock()
		if caps != nil {
			provider = caps.Provider(cfg.Model)
		}
	}
	if provider == "" {
		provider = cfg.Provider
	}
	window := e.statsContextWindow(cfg, provider)
	return statsActiveContextView{
		Provider: provider, Model: cfg.Model, Window: window.Tokens,
		WindowSource: window.Source, CompactThreshold: agent.AutoCompactThreshold(window.Tokens),
	}
}

type statsSessionView struct {
	ID            string `json:"id,omitempty"`
	Title         string `json:"title,omitempty"`
	Provider      string `json:"provider,omitempty"`
	ProviderType  string `json:"provider_type,omitempty"`
	Model         string `json:"model"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	Messages      int    `json:"messages"`
	UserMessages  int    `json:"user_messages"`
	AssistantMsgs int    `json:"assistant_messages"`
	ToolMessages  int    `json:"tool_messages"`
	ToolCalls     int    `json:"tool_calls"`
}

type statsTokensView struct {
	Input          int64 `json:"input"`
	EvaluatedInput int64 `json:"evaluated_input"`
	CachedInput    int64 `json:"cached_input"`
	Output         int64 `json:"output"`
	Reasoning      int64 `json:"reasoning"`
	Total          int64 `json:"total"`
	HasCached      bool  `json:"has_cached"`
	HasReasoning   bool  `json:"has_reasoning"`
}

type statsContextView struct {
	Window           int                   `json:"window"`
	WindowSource     string                `json:"window_source,omitempty"`
	HasSnapshot      bool                  `json:"has_snapshot"`
	EstimatedUsed    int                   `json:"estimated_used"`
	Percent          int                   `json:"percent"`
	CompactThreshold int                   `json:"compact_threshold"`
	Breakdown        statsContextBreakdown `json:"breakdown"`
	// RequestsToday is the daily completion-request count for the
	// active endpoint (request_budget.json). Metered free tiers such
	// as OpenCode Zen's ~100/day show it next to the context gauge;
	// zero when the counter is uninitialized or nothing was sent.
	RequestsToday int `json:"requests_today"`
}

type statsContextBreakdown struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	Tools     int `json:"tools"`
	Other     int `json:"other"`
}

type statsCostView = usagecost.Summary

type statsTelemetryView struct {
	Scope       string `json:"scope,omitempty"`
	Samples     int    `json:"samples"`
	Steps       int    `json:"steps"`
	DurationMS  int64  `json:"duration_ms"`
	AverageMS   int64  `json:"average_ms"`
	ModelMS     int64  `json:"model_ms"`
	ToolsMS     int64  `json:"tools_ms"`
	CLIMS       int64  `json:"cli_ms"`
	PersistMS   int64  `json:"persist_ms"`
	ModelCalls  int    `json:"model_calls"`
	HelperCalls int    `json:"helper_calls"`
	// Aux* answer one question: how much of a reply is inference the
	// user never asked for. AuxCalls counts helper model calls charged
	// to the measured turns, AuxMS their wall time, AuxShare that time
	// as a percentage of total turn duration.
	AuxCalls int   `json:"aux_calls"`
	AuxMS    int64 `json:"aux_ms"`
	AuxShare int   `json:"aux_share"`
	// OffTurn* are model calls made outside any agent turn (titles, run
	// summaries, folder/document indexing, vision) since the app started.
	// They have no turn to be charged to, so they are reported separately.
	OffTurnCalls  int   `json:"off_turn_calls"`
	OffTurnMS     int64 `json:"off_turn_ms"`
	FailedCalls   int   `json:"failed_calls"`
	CanceledCalls int   `json:"canceled_calls"`
	ToolFailures  int   `json:"tool_failures"`
	// NoOpSearches is the stall signature: search_code calls that returned
	// nothing. A run full of them is a discovery loop, not diligence.
	NoOpSearches int `json:"noop_searches"`
	// Failures breaks the aggregate down per tool, so "5 schema-arg
	// errors from ctx_execute" is visible without opening a transcript.
	Failures        []statsToolFailureView `json:"tool_failures_by_tool,omitempty"`
	Bottleneck      string                 `json:"bottleneck,omitempty"`
	BottleneckShare int                    `json:"bottleneck_share"`
	Signals         []string               `json:"signals,omitempty"`
	Tools           []statsToolTimingView  `json:"tools,omitempty"`
}

type statsToolFailureView struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type statsToolTimingView struct {
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
}

// stats returns persisted per-call usage when available and falls back to the
// legacy session aggregate for conversations created before session_usage.
func (e *Engine) stats(ctx context.Context, sessionID string) (statsView, error) {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	preview := e.usageIdentity(cfg, "model")
	sv := statsView{
		Model:           cfg.Model,
		Context:         contextFromUsage(session.UsageRecord{ContextWindow: preview.ContextWindow}),
		ActiveContext:   e.statsActiveContext(cfg),
		GenerationSpeed: statsGenerationSpeedView{Scope: "session"},
		Session: statsSessionView{
			Provider: preview.Provider, ProviderType: preview.ProviderType, Model: cfg.Model,
		},
	}
	sv.Context.WindowSource = preview.WindowSource
	sv.Context.RequestsToday = llm.ProviderRequestsTodayFlexible(cfg.BaseURL)

	store, err := e.sessionStore()
	if err != nil {
		sv.Cost = resolveStatsCost(e.tomlConfig(), nil, usageRecordFromIdentity(preview))
		sv.Pricing = sv.Cost
		sv.Cost.Currency = config.EffectiveCostCurrency(e.tomlConfig())
		return sv, nil
	}

	var meta session.Session
	var legacyBreakdown llm.RequestBreakdown
	tc := e.tomlConfig()
	var exchangeCache *fx.Cache
	if config.EffectiveCostCurrency(tc) != "USD" {
		if rates := e.historyRates(); rates != nil {
			exchangeCache, _ = rates.Cache()
			if exchangeCache != nil {
				_ = exchangeCache.Refresh(ctx)
			}
		}
	}
	var usage statsUsageRead
	var legacyUsage *session.UsageRecord
	if sessionID != "" {
		meta, err = store.Get(sessionID)
		if err != nil {
			return sv, err
		}
		if !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return sv, errSessionOutsideWorkspace
		}
		usage, err = readStatsUsage(ctx, store, sessionID, tc, exchangeCache)
		if err != nil {
			return sv, err
		}
		sv.LastTurn, err = readStatsLastTurn(ctx, store, sessionID)
		if err != nil {
			return sv, err
		}
		if usage.HasMain {
			counts, readErr := store.ReadMessageCounts(ctx, sessionID)
			if readErr != nil {
				return sv, readErr
			}
			sv.Session = summarizeSession(meta, nil)
			sv.Session.UserMessages = counts.User
			sv.Session.AssistantMsgs = counts.Assistant
			sv.Session.ToolMessages = counts.Tool
			sv.Session.ToolCalls = counts.ToolCalls
		} else {
			// Sessions without a main-call snapshot still need an estimate,
			// without retaining a second full copy of historical tool output.
			summary, readErr := store.ReadMessageSummary(ctx, sessionID)
			if readErr != nil {
				return sv, readErr
			}
			sv.Session = summarizeSession(meta, nil)
			sv.Session.UserMessages = summary.Counts.User
			sv.Session.AssistantMsgs = summary.Counts.Assistant
			sv.Session.ToolMessages = summary.Counts.Tool
			sv.Session.ToolCalls = summary.Counts.ToolCalls
			legacyBreakdown = summary.Breakdown
		}
	}

	if usage.Records > 0 {
		sv.Tokens = usage.Tokens
		sv.GenerationSpeed = usage.GenerationSpeed
	}
	if usage.HasMain {
		last := usage.Main
		sv.Session.Provider = last.Provider
		sv.Session.ProviderType = last.ProviderType
		sv.Session.Model = last.Model
		sv.Model = last.Model
		sv.Context = e.statsContextFromUsage(last, preview)
	} else if sessionID != "" {
		fallback := e.legacyUsageIdentity(meta.Model)
		fallbackRecord := usageRecordFromIdentity(fallback)
		fallbackRecord.SessionID = meta.ID
		fallbackRecord.Input = int64(meta.TokenIn)
		fallbackRecord.Output = int64(meta.TokenOut)
		if sv.Session.Provider == "" {
			sv.Session.Provider = fallback.Provider
			sv.Session.ProviderType = fallback.ProviderType
		}
		if sv.Session.Model == "" {
			sv.Session.Model = meta.Model
		}
		if usage.Records == 0 {
			legacyUsage = &fallbackRecord
			sv.Tokens.Input = fallbackRecord.Input
			sv.Tokens.Output = fallbackRecord.Output
		}
		sv.Context = contextFromUsage(session.UsageRecord{
			ContextWindow: preview.ContextWindow, ContextSystem: legacyBreakdown.System,
			ContextUser: legacyBreakdown.User, ContextAssistant: legacyBreakdown.Assistant,
			ContextTool: legacyBreakdown.Tool, ContextOther: legacyBreakdown.Other,
		})
		sv.Context.WindowSource = preview.WindowSource
	}

	// Daily request quota for the active endpoint — independent of
	// which branch filled sv.Context above, and key-free by design
	// (keyless providers like Zen's public tier count identically).
	e.mu.RLock()
	activeBaseURL := e.cfg.BaseURL
	e.mu.RUnlock()
	sv.Context.RequestsToday = llm.ProviderRequestsTodayFlexible(activeBaseURL)

	sv.Tokens.Total = sv.Tokens.Input + sv.Tokens.Output
	sv.Tokens.EvaluatedInput = sv.Tokens.Input - sv.Tokens.CachedInput
	if sv.Tokens.EvaluatedInput < 0 {
		sv.Tokens.EvaluatedInput = 0
	}
	sv.SessionToken = sv.Tokens.Total
	// Cross-session diagnosis is deliberately a bounded, panel-time query.
	// Normal turns do not aggregate history and the cap prevents old stores
	// from turning observability into a new performance problem.
	if recent, recentErr := store.ReadRecentTurnTelemetry(ctx, time.Now().Add(-7*24*time.Hour), 2000); recentErr == nil {
		sv.Telemetry = summarizeTelemetry(recent, sv.Tokens)
		sv.Telemetry.Scope = "7d"
	}
	// Out-of-turn work is counted in memory for this app run, not read from
	// session_turns, so it is attached after the turn aggregation.
	offCalls, offUs := e.offTurnSnapshot()
	sv.Telemetry.OffTurnCalls = offCalls
	sv.Telemetry.OffTurnMS = offUs / 1000

	now := time.Now()
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	ledger, _ := e.creditStorage(ctx)
	sv.DailyToken = dailyTokenTotal(ctx, store, ledger, localMidnight)

	if usage.Records > 0 {
		sv.Cost = usage.Cost
	} else if legacyUsage != nil {
		sv.Cost = resolveStatsCost(tc, []session.UsageRecord{*legacyUsage}, session.UsageRecord{})
		if config.EffectiveCostCurrency(tc) != "USD" && sv.Cost.Amount != nil && *sv.Cost.Amount > 0 {
			// A pre-ledger aggregate has no per-call dates. A single current
			// exchange rate would manufacture a historical converted amount.
			sv.Cost.Amount, sv.Cost.Source, sv.Cost.Partial = nil, "fx_missing", true
		}
	} else {
		sv.Cost = resolveStatsCost(tc, nil, usageRecordFromIdentity(preview))
	}
	sv.Cost.Currency = config.EffectiveCostCurrency(tc)
	if sv.Cost.MissingFXCalls > 0 {
		pending, generation := e.historyRates().State()
		sv.Cost.RatesPending, sv.Cost.RatesGeneration = pending, generation
	}
	pricingIdentity := usageRecordFromIdentity(preview)
	if usage.HasMain {
		pricingIdentity = usage.Main
	} else if legacyUsage != nil {
		pricingIdentity = *legacyUsage
	}
	pricingIdentity.PriceSnapshot = nil
	pricingIdentity.Input, pricingIdentity.Output, pricingIdentity.CachedInput = 0, 0, 0
	sv.Pricing = resolveStatsCost(tc, nil, pricingIdentity)
	return sv, nil
}
