package webgui

import (
	"context"
	"math"
	"net"
	"strings"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type statsView struct {
	// Compatibility fields used by the compact sidebar.
	Model        string `json:"model"`
	SessionToken int64  `json:"session_tokens"`
	DailyToken   int64  `json:"daily_tokens"`

	Session statsSessionView `json:"session"`
	Tokens  statsTokensView  `json:"tokens"`
	Context statsContextView `json:"context"`
	Cost    statsCostView    `json:"cost"`
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
	Window        int                   `json:"window"`
	EstimatedUsed int                   `json:"estimated_used"`
	Percent       int                   `json:"percent"`
	Breakdown     statsContextBreakdown `json:"breakdown"`
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

	store, err := session.OpenStore(e.dataDir)
	if err != nil {
		sv.Cost = resolveStatsCost(e.tomlConfig(), nil, usageRecordFromIdentity(preview))
		return sv, nil
	}
	defer store.Close()

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

func summarizeSession(meta session.Session, messages []llm.Message) statsSessionView {
	out := statsSessionView{
		ID: meta.ID, Title: meta.Title, Model: meta.Model,
		CreatedAt: meta.CreatedAt.Format(time.RFC3339), UpdatedAt: meta.UpdatedAt.Format(time.RFC3339),
		Messages: meta.MessageCount,
	}
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			out.UserMessages++
		case llm.RoleAssistant:
			out.AssistantMsgs++
			out.ToolCalls += len(msg.ToolCalls)
		case llm.RoleTool:
			out.ToolMessages++
		}
	}
	return out
}

func contextFromUsage(u session.UsageRecord) statsContextView {
	out := statsContextView{Window: u.ContextWindow}
	out.Breakdown = statsContextBreakdown{
		User: u.ContextUser, Assistant: u.ContextAssistant, Tools: u.ContextTool,
		Other: u.ContextSystem + u.ContextOther,
	}
	out.EstimatedUsed = out.Breakdown.User + out.Breakdown.Assistant + out.Breakdown.Tools + out.Breakdown.Other
	if out.Window > 0 {
		out.Percent = int(math.Min(100, math.Round(float64(out.EstimatedUsed)*100/float64(out.Window))))
	}
	return out
}

func contextFromMessages(messages []llm.Message, window int) statsContextView {
	b := llm.EstimateRequestBreakdown(messages, nil)
	return contextFromUsage(session.UsageRecord{
		ContextWindow: window, ContextSystem: b.System, ContextUser: b.User,
		ContextAssistant: b.Assistant, ContextTool: b.Tool, ContextOther: b.Other,
	})
}

func usageRecordFromIdentity(id usageIdentity) session.UsageRecord {
	return session.UsageRecord{
		Provider: id.Provider, ProviderType: id.ProviderType, EndpointHost: id.EndpointHost,
		Model: id.Model, ContextWindow: id.ContextWindow, Source: id.Source,
	}
}

func (e *Engine) legacyUsageIdentity(model string) usageIdentity {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	if model == "" || model == cfg.Model {
		return e.usageIdentity(cfg, "legacy")
	}
	id := usageIdentity{Model: model, Source: "legacy"}
	providerName := ""
	if e.caps != nil {
		providerName = e.caps.Provider(model)
		if info, ok := e.caps.Get(model); ok {
			id.ContextWindow = info.ContextLength
		}
	}
	for _, p := range e.providerManager().Configured() {
		if p.Name == providerName {
			id.Provider = p.Name
			id.ProviderType = p.Type
			id.EndpointHost = endpointHost(p.BaseURL)
			return id
		}
	}
	id.Provider = providerName
	return id
}

type usageQuote struct {
	state      string
	amount     float64
	source     string
	rate       credits.Rate
	rateKnown  bool
	cacheKnown bool
}

func resolveStatsCost(tc config.TomlConfig, usage []session.UsageRecord, preview session.UsageRecord) statsCostView {
	out := statsCostView{Currency: "USD", CacheDiscountKnown: true}
	actualCalls := len(usage)
	if len(usage) == 0 {
		usage = []session.UsageRecord{preview}
	}
	var manual, estimated, free, subscription, local, unknown int
	var amount float64
	var firstRate *credits.Rate
	firstSource := ""
	sameRate := true
	for _, u := range usage {
		q := quoteUsage(tc, u)
		switch q.state {
		case "manual":
			manual++
			amount += q.amount
		case "estimated":
			estimated++
			amount += q.amount
		case "free":
			free++
		case "subscription":
			subscription++
		case "local":
			local++
		default:
			unknown++
		}
		if q.rateKnown {
			if firstRate == nil {
				r := q.rate
				firstRate = &r
				firstSource = q.source
			} else if *firstRate != q.rate || firstSource != q.source {
				sameRate = false
			}
			out.CacheDiscountKnown = out.CacheDiscountKnown && q.cacheKnown
		}
	}
	out.Calls = actualCalls
	if actualCalls > 0 {
		out.UnknownCalls = unknown
		out.IncludedCalls = subscription + local
	}
	priced := manual + estimated
	switch {
	case priced > 0:
		out.State = "estimated"
		out.Estimated = true
		if manual == len(usage) && estimated == 0 {
			out.State = "manual"
			out.Estimated = false
			out.Manual = true
		}
		out.Amount = floatPtr(amount)
		out.Partial = unknown+subscription+local+free > 0
	case free == len(usage):
		out.State = "free"
		out.Amount = floatPtr(0)
		out.Source = "free"
	case subscription == len(usage):
		out.State = "subscription"
		out.Source = "subscription"
	case local == len(usage):
		out.State = "local"
		out.Source = "local"
	default:
		out.State = "unknown"
		out.Partial = actualCalls > 0 && unknown != len(usage)
	}
	if firstRate != nil && sameRate && priced == len(usage) {
		in := firstRate.InputPer1k * 1000
		cached := firstRate.CachedInputPer1k * 1000
		output := firstRate.OutputPer1k * 1000
		out.InputPerMillion = floatPtr(in)
		if cached > 0 {
			out.CachedInputPerMillion = floatPtr(cached)
		}
		out.OutputPerMillion = floatPtr(output)
		out.Source = firstSource
	} else if priced > 0 {
		out.Source = "mixed"
	}
	return out
}

func quoteUsage(tc config.TomlConfig, u session.UsageRecord) usageQuote {
	if manual, ok := manualPrice(tc.ModelPrices, u.Provider, u.Model); ok {
		rate := credits.Rate{
			InputPer1k: manual.InputCost / 1000, CachedInputPer1k: manual.CachedInputCost / 1000,
			OutputPer1k: manual.OutputCost / 1000,
		}
		amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
		return usageQuote{state: "manual", amount: amount, source: "manual", rate: rate, rateKnown: true, cacheKnown: cacheKnown}
	}
	if u.ProviderType == config.ProviderCodex {
		return usageQuote{state: "subscription", source: "subscription"}
	}
	if u.ProviderType == config.ProviderEcho || isLocalEndpointHost(u.EndpointHost) {
		return usageQuote{state: "local", source: "local"}
	}
	if llm.IsFreeModelID(u.Model) {
		return usageQuote{state: "free", source: "free"}
	}

	rateProvider := u.Provider
	if u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai" {
		rateProvider = "openrouter"
	}
	rate, source, ok := credits.LookupRateForProvider(rateProvider, u.Model)
	if !ok {
		return usageQuote{state: "unknown"}
	}
	endpointRate := strings.Contains(source, "(endpoint)")
	isOpenRouter := u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai"
	if isOpenRouter && !endpointRate {
		return usageQuote{state: "unknown"}
	}
	if !isOfficialMeteredHost(u.EndpointHost) {
		return usageQuote{state: "unknown"}
	}
	amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
	sourceLabel := "official"
	if endpointRate {
		sourceLabel = "provider"
	} else if strings.Contains(source, "(fetched)") {
		sourceLabel = "catalog"
	}
	return usageQuote{state: "estimated", amount: amount, source: sourceLabel, rate: rate, rateKnown: true, cacheKnown: cacheKnown}
}

func manualPrice(prices []config.ModelPriceConf, provider, model string) (config.ModelPriceConf, bool) {
	for _, p := range prices {
		if p.Provider == provider && strings.EqualFold(p.Model, model) {
			return p, true
		}
	}
	for _, p := range prices {
		if p.Provider == "" && strings.EqualFold(p.Model, model) {
			return p, true
		}
	}
	return config.ModelPriceConf{}, false
}

func isLocalEndpointHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func isOfficialMeteredHost(host string) bool {
	switch strings.TrimSpace(strings.ToLower(host)) {
	case "api.openai.com", "api.anthropic.com", "api.deepseek.com",
		"generativelanguage.googleapis.com", "api.mistral.ai", "api.groq.com",
		"api.together.xyz", "api.x.ai", "openrouter.ai", "www.openrouter.ai":
		return true
	default:
		return false
	}
}

func floatPtr(v float64) *float64 { return &v }
