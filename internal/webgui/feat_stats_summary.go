package webgui

import (
	"math"
	"sort"
	"strings"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func summarizeTelemetry(turns []session.TurnSummary, tokens statsTokensView) statsTelemetryView {
	var out statsTelemetryView
	toolUS := map[string]int64{}
	var modelUS, toolsUS, cliUS, persistUS int64
	for _, turn := range turns {
		if len(turn.Phases) == 0 {
			continue // legacy rows are not false zero-duration samples
		}
		out.Samples++
		out.Steps += turn.Steps
		out.DurationMS += turn.DurationMS
		out.ModelCalls += turn.ModelCalls
		out.HelperCalls += turn.HelperCalls
		out.FailedCalls += turn.FailedCalls
		out.CanceledCalls += turn.CanceledCalls
		out.ToolFailures += turn.ToolFailures
		for name, us := range turn.Phases {
			switch {
			case name == "request_encode", name == "backend_wait", name == "stream_total", strings.HasPrefix(name, "model:"):
				modelUS += us
			case name == "tool_execution":
				toolsUS += us
			case name == "context_prepare", name == "next_turn_prepare":
				cliUS += us
			case name == "session_persist":
				persistUS += us
			case strings.HasPrefix(name, "tool:"):
				toolUS[strings.TrimPrefix(name, "tool:")] += us
			}
		}
	}
	if out.Samples == 0 {
		return out
	}
	out.ModelMS, out.ToolsMS, out.CLIMS, out.PersistMS = modelUS/1000, toolsUS/1000, cliUS/1000, persistUS/1000
	out.AverageMS = out.DurationMS / int64(out.Samples)
	pipeline := out.ModelMS + out.ToolsMS + out.CLIMS
	type part struct {
		name string
		ms   int64
	}
	parts := []part{{"model", out.ModelMS}, {"tools", out.ToolsMS}, {"cli", out.CLIMS}}
	sort.Slice(parts, func(i, j int) bool { return parts[i].ms > parts[j].ms })
	out.Bottleneck = parts[0].name
	if pipeline > 0 {
		out.BottleneckShare = int(math.Round(float64(parts[0].ms) * 100 / float64(pipeline)))
	}
	if out.Samples < 5 {
		out.Signals = append(out.Signals, "collect_more")
	}
	if out.Bottleneck == "model" && out.BottleneckShare >= 75 {
		out.Signals = append(out.Signals, "model_bound")
	}
	if out.Bottleneck == "cli" && out.BottleneckShare >= 15 && out.CLIMS/int64(out.Samples) >= 25 {
		out.Signals = append(out.Signals, "cli_overhead")
	}
	if out.Steps > out.Samples*3 {
		out.Signals = append(out.Signals, "many_steps")
	}
	if out.ToolFailures > 0 {
		out.Signals = append(out.Signals, "tool_failures")
	}
	if out.FailedCalls > 0 || out.CanceledCalls > 0 {
		out.Signals = append(out.Signals, "model_failures")
	}
	if tokens.Input >= 2000 && tokens.HasCached && tokens.CachedInput*2 < tokens.Input {
		out.Signals = append(out.Signals, "low_cache")
	}
	if out.PersistMS/int64(out.Samples) >= 25 {
		out.Signals = append(out.Signals, "slow_persist")
	}
	for name, us := range toolUS {
		out.Tools = append(out.Tools, statsToolTimingView{Name: name, DurationMS: us / 1000})
	}
	sort.Slice(out.Tools, func(i, j int) bool {
		if out.Tools[i].DurationMS == out.Tools[j].DurationMS {
			return out.Tools[i].Name < out.Tools[j].Name
		}
		return out.Tools[i].DurationMS > out.Tools[j].DurationMS
	})
	if len(out.Tools) > 5 {
		out.Tools = out.Tools[:5]
	}
	return out
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
	out := statsContextView{Window: u.ContextWindow, CompactThreshold: agent.AutoCompactThreshold(u.ContextWindow)}
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
