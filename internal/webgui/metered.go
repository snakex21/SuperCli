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

// meteredProvider records provider-reported usage after each real model call.
// It stores only counters and request-shape estimates, never prompts, URLs,
// headers, or credentials.
type meteredProvider struct {
	inner     llm.Provider
	store     *session.Store
	sessionID string
	identity  usageIdentity
}

func newMeteredProvider(inner llm.Provider, store *session.Store, sessionID string, identity usageIdentity) llm.Provider {
	if inner == nil || store == nil || sessionID == "" {
		return inner
	}
	identity.Model = inner.Name()
	return &meteredProvider{inner: inner, store: store, sessionID: sessionID, identity: identity}
}

func (p *meteredProvider) Name() string { return p.inner.Name() }

func (p *meteredProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	in, err := p.inner.Complete(ctx, msgs, tools)
	if err != nil {
		return nil, err
	}
	out := make(chan llm.Delta)
	breakdown := estimateRequestBreakdown(msgs, tools)
	go func() {
		var final *llm.Usage
		defer func() {
			if final != nil {
				p.recordUsage(*final, breakdown)
			}
			close(out)
		}()
		for d := range in {
			if d.Usage != nil {
				copyUsage := *d.Usage
				final = &copyUsage
			}
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p *meteredProvider) recordUsage(final llm.Usage, breakdown requestBreakdown) {
	recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.store.AppendUsage(recordCtx, session.UsageRecord{
		SessionID:        p.sessionID,
		Provider:         p.identity.Provider,
		ProviderType:     p.identity.ProviderType,
		EndpointHost:     p.identity.EndpointHost,
		Model:            p.inner.Name(),
		Input:            int64(final.Input),
		Output:           int64(final.Output),
		CachedInput:      int64(final.CachedInput),
		Reasoning:        int64(final.Reasoning),
		HasCachedInput:   final.CachedInput > 0,
		HasReasoning:     final.Reasoning > 0,
		ContextWindow:    p.identity.ContextWindow,
		ContextSystem:    breakdown.system,
		ContextUser:      breakdown.user,
		ContextAssistant: breakdown.assistant,
		ContextTool:      breakdown.tools,
		ContextOther:     breakdown.other,
		Source:           p.identity.Source,
	})
}

type requestBreakdown struct {
	system    int
	user      int
	assistant int
	tools     int
	other     int
}

func estimateRequestBreakdown(msgs []llm.Message, tools []llm.ToolDef) requestBreakdown {
	var out requestBreakdown
	for _, msg := range msgs {
		tokens := llm.EstimateMessageTokens(msg)
		switch msg.Role {
		case llm.RoleSystem:
			out.system += tokens
		case llm.RoleUser:
			out.user += tokens
		case llm.RoleTool:
			out.tools += tokens
		case llm.RoleAssistant:
			toolTokens := 0
			if len(msg.ToolCalls) > 0 {
				toolOnly := llm.Message{Role: llm.RoleAssistant, ToolCalls: msg.ToolCalls}
				toolTokens = llm.EstimateMessageTokens(toolOnly) - 16
				if toolTokens < 0 {
					toolTokens = 0
				}
				if toolTokens > tokens {
					toolTokens = tokens
				}
			}
			out.tools += toolTokens
			out.assistant += tokens - toolTokens
		default:
			out.other += tokens
		}
	}
	for _, tool := range tools {
		text := strings.TrimSpace(tool.Name + " " + tool.Description + " " + tool.Schema)
		if text != "" {
			out.tools += llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: text})
		}
	}
	return out
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
