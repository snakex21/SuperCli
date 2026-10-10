package session

import (
	"context"
	"database/sql"
)

// VisitUsageForStats streams the fields used by the stats panel in call order.
// The callback runs synchronously while rows are open; it must not query Store.
func (s *Store) VisitUsageForStats(ctx context.Context, sessionID string, visit func(UsageRecord)) error {
	rows, err := s.db.QueryContext(ctx, "SELECT provider, provider_type, endpoint_host, model, "+
		"input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, has_cached_input, has_reasoning, "+
		"ttft_ms, duration_ms, has_timing, "+
		"context_window, ctx_system_tokens, ctx_user_tokens, ctx_assistant_tokens, ctx_tool_tokens, ctx_other_tokens, source, "+
		"(SELECT price_snapshot FROM billing_usage b WHERE b.session_id=session_usage.session_id AND b.call_seq=session_usage.call_seq) "+
		"FROM session_usage WHERE session_id = ? ORDER BY call_seq", sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u UsageRecord
		var cachedKnown, reasoningKnown, timingKnown int
		var priceJSON sql.NullString
		if err := rows.Scan(&u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning, &cachedKnown, &reasoningKnown,
			&u.TTFTMS, &u.DurationMS, &timingKnown,
			&u.ContextWindow, &u.ContextSystem, &u.ContextUser, &u.ContextAssistant, &u.ContextTool, &u.ContextOther, &u.Source, &priceJSON); err != nil {
			return err
		}
		u.HasCachedInput = cachedKnown != 0
		u.HasReasoning = reasoningKnown != 0
		u.HasTiming = timingKnown != 0
		if u.PriceSnapshot, err = decodePriceSnapshot(priceJSON); err != nil {
			return err
		}
		visit(u)
	}
	return rows.Err()
}
