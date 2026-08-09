package webgui

import (
	"context"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

type statsView struct {
	// Compatibility fields used by the compact sidebar.
	Model        string `json:"model"`
	SessionToken int64  `json:"session_tokens"`
	DailyToken   int64  `json:"daily_tokens"`

	Session   statsSessionView   `json:"session"`
	Tokens    statsTokensView    `json:"tokens"`
	Context   statsContextView   `json:"context"`
	Cost      statsCostView      `json:"cost"`
	Telemetry statsTelemetryView `json:"telemetry"`
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
	EstimatedUsed    int                   `json:"estimated_used"`
	Percent          int                   `json:"percent"`
	CompactThreshold int                   `json:"compact_threshold"`
	Breakdown        statsContextBreakdown `json:"breakdown"`
}

type statsContextBreakdown struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	Tools     int `json:"tools"`
	Other     int `json:"other"`
}

type statsCostView struct {
	State                 string   `json:"state"`
	Amount                *float64 `json:"amount"`
	Currency              string   `json:"currency"`
	Source                string   `json:"source,omitempty"`
	Estimated             bool     `json:"estimated"`
	Partial               bool     `json:"partial"`
	Calls                 int      `json:"calls"`
	UnknownCalls          int      `json:"unknown_calls"`
	IncludedCalls         int      `json:"included_calls"`
	InputPerMillion       *float64 `json:"input_per_million,omitempty"`
	CachedInputPerMillion *float64 `json:"cached_input_per_million,omitempty"`
	OutputPerMillion      *float64 `json:"output_per_million,omitempty"`
	CacheDiscountKnown    bool     `json:"cache_discount_known"`
	Manual                bool     `json:"manual"`
}

type statsTelemetryView struct {
	Scope           string                `json:"scope,omitempty"`
	Samples         int                   `json:"samples"`
	Steps           int                   `json:"steps"`
	DurationMS      int64                 `json:"duration_ms"`
	AverageMS       int64                 `json:"average_ms"`
	ModelMS         int64                 `json:"model_ms"`
	ToolsMS         int64                 `json:"tools_ms"`
	CLIMS           int64                 `json:"cli_ms"`
	PersistMS       int64                 `json:"persist_ms"`
	ModelCalls      int                   `json:"model_calls"`
	HelperCalls     int                   `json:"helper_calls"`
	// Aux* answer one question: how much of a reply is inference the
	// user never asked for. AuxCalls counts helper model calls charged
	// to the measured turns, AuxMS their wall time, AuxShare that time
	// as a percentage of total turn duration.
	AuxCalls        int                   `json:"aux_calls"`
	AuxMS           int64                 `json:"aux_ms"`
	AuxShare        int                   `json:"aux_share"`
	// OffTurn* are model calls made outside any agent turn (titles, run
	// summaries, folder/document indexing, vision) since the app started.
	// They have no turn to be charged to, so they are reported separately.
	OffTurnCalls    int                   `json:"off_turn_calls"`
	OffTurnMS       int64                 `json:"off_turn_ms"`
	FailedCalls     int                   `json:"failed_calls"`
	CanceledCalls   int                   `json:"canceled_calls"`
	ToolFailures    int                   `json:"tool_failures"`
	// NoOpSearches is the stall signature: search_code calls that returned
	// nothing. A run full of them is a discovery loop, not diligence.
	NoOpSearches int                     `json:"noop_searches"`
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
		Model: cfg.Model,
		Session: statsSessionView{
			Provider: preview.Provider, ProviderType: preview.ProviderType, Model: cfg.Model,
		},
	}

	store, err := e.sessionStore()
	if err != nil {
		sv.Cost = resolveStatsCost(e.tomlConfig(), nil, usageRecordFromIdentity(preview))
		return sv, nil
	}

	var meta session.Session
	var messages []llm.Message
	var usage []session.UsageRecord
	if sessionID != "" {
		meta, err = store.Get(sessionID)
		if err != nil {
			return sv, err
		}
		if !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return sv, errSessionOutsideWorkspace
		}
		rows, readErr := store.ReadMessages(ctx, sessionID)
		if readErr != nil {
			return sv, readErr
		}
		messages = make([]llm.Message, 0, len(rows))
		for _, row := range rows {
			msg, decodeErr := row.ToMessage()
			if decodeErr == nil {
				messages = append(messages, msg)
			}
		}
		usage, readErr = store.ReadUsage(ctx, sessionID)
		if readErr != nil {
			return sv, readErr
		}
		sv.Session = summarizeSession(meta, messages)
	}

	if len(usage) > 0 {
		last := usage[len(usage)-1]
		sv.Session.Provider = last.Provider
		sv.Session.ProviderType = last.ProviderType
		sv.Session.Model = last.Model
		sv.Model = last.Model
		for _, u := range usage {
			sv.Tokens.Input += u.Input
			sv.Tokens.Output += u.Output
			sv.Tokens.CachedInput += u.CachedInput
			sv.Tokens.Reasoning += u.Reasoning
			sv.Tokens.HasCached = sv.Tokens.HasCached || u.HasCachedInput
			sv.Tokens.HasReasoning = sv.Tokens.HasReasoning || u.HasReasoning
		}
		sv.Context = contextFromUsage(last)
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
		usage = []session.UsageRecord{fallbackRecord}
		sv.Tokens.Input = fallbackRecord.Input
		sv.Tokens.Output = fallbackRecord.Output
		sv.Context = contextFromMessages(messages, fallback.ContextWindow)
	}

	sv.Tokens.Total = sv.Tokens.Input + sv.Tokens.Output
	sv.Tokens.EvaluatedInput = sv.Tokens.Input - sv.Tokens.CachedInput
	if sv.Tokens.EvaluatedInput < 0 {
		sv.Tokens.EvaluatedInput = 0
	}
	sv.SessionToken = sv.Tokens.Total
	// Cross-session diagnosis is deliberately a bounded, panel-time query.
	// Normal turns do not aggregate history and the cap prevents old stores
	// from turning observability into a new performance problem.
	if recent, recentErr := store.ReadRecentTurnSummaries(ctx, time.Now().Add(-7*24*time.Hour), 2000); recentErr == nil {
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
	if in, out, usageErr := store.UsageSince(ctx, localMidnight); usageErr == nil {
		sv.DailyToken = in + out
	}
	// TUI/CLI usage still lives in credit_ledger. Web usage is recorded only in
	// session_usage, so the two totals are disjoint and can be combined.
	if db, dbErr := openDataDB(e.dataDir); dbErr == nil {
		cs := credits.NewStorage(db)
		if cs.Migrate(ctx) == nil {
			if total, totalErr := cs.TotalSince(ctx, localMidnight); totalErr == nil {
				sv.DailyToken += total
			}
		}
		_ = db.Close()
	}

	tc := e.tomlConfig()
	if len(usage) == 0 {
		sv.Cost = resolveStatsCost(tc, nil, usageRecordFromIdentity(preview))
	} else {
		sv.Cost = resolveStatsCost(tc, usage, session.UsageRecord{})
	}
	return sv, nil
}
