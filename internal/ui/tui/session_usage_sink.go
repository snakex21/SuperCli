package tui

import (
	"context"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// Captures the destination when the run starts: a late usage frame cannot be
// attributed to a subsequently opened conversation.
func (m Model) sessionUsageSink() llm.CallSink {
	store, id, provider := m.sessionStore, m.sessionID, m.activeProviderName()
	if store == nil || id == "" {
		return nil
	}
	return func(s llm.CallStat) {
		if s.TokensIn == 0 && s.TokensOut == 0 {
			return
		}
		name := s.Provider
		if name == "" {
			name = provider
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = store.AppendUsage(ctx, session.UsageRecord{
			SessionID: id, Provider: name, Model: s.Model,
			Input: int64(s.TokensIn), Output: int64(s.TokensOut), CachedInput: int64(s.TokensCached), Reasoning: int64(s.TokensReasoning),
			HasCachedInput: s.TokensCached > 0, HasReasoning: s.TokensReasoning > 0,
			TTFTMS: s.TTFT.Milliseconds(), PrefillEvaluated: int64(s.PrefillEvaluated),
			PrefillTokensPerSecond: s.PrefillTokensPerSecond, PrefillBudget: s.PrefillBudget, PrefillBudgetSource: s.PrefillBudgetSource,
			ContextSystem: s.Request.System, ContextUser: s.Request.User, ContextAssistant: s.Request.Assistant, ContextTool: s.Request.Tool, ContextOther: s.Request.Other,
			Source: s.Purpose,
		})
	}
}
