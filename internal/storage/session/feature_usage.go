package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UsageRecord is one completed model request attributed to a persisted
// session. CachedInput is a subset of Input; Reasoning is a subset of Output.
// Context fields are estimates of the request payload and are never prompts.
type UsageRecord struct {
	SessionID      string
	CallSeq        int
	Provider       string
	ProviderType   string
	EndpointHost   string
	Model          string
	Input          int64
	Output         int64
	CachedInput    int64
	Reasoning      int64
	HasCachedInput bool
	HasReasoning   bool
	TTFTMS         int64
	// Timing is measured per completed request, never inferred from a turn's
	// wall clock. Older rows default to unknown even if they retain TTFT.
	DurationMS             int64
	HasTiming              bool
	PrefillEvaluated       int64
	PrefillTokensPerSecond float64
	PrefillBudget          int
	PrefillBudgetSource    string
	ContextWindow          int
	ContextSystem          int
	ContextUser            int
	ContextAssistant       int
	ContextTool            int
	ContextOther           int
	// ContextEstimateKnown is journal coverage for the estimated tool-role
	// request context. It does not describe provider-reported token usage.
	ContextEstimateKnown bool
	Source               string
	CreatedAt            time.Time
	PriceSnapshot        *PriceSnapshot
}

// AppendUsage persists one provider-reported usage record. call_seq is
// allocated transactionally so parallel model calls cannot overwrite each
// other. The aggregate sessions.token_in/token_out remains owned by Writer
// for backwards compatibility and is intentionally not modified here.
func (s *Store) AppendUsage(ctx context.Context, u UsageRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.AppendUsage: nil store")
	}
	if u.SessionID == "" {
		return fmt.Errorf("session.Store.AppendUsage: empty session id")
	}
	if u.Input < 0 {
		u.Input = 0
	}
	if u.Output < 0 {
		u.Output = 0
	}
	if u.CachedInput < 0 {
		u.CachedInput = 0
	}
	if u.CachedInput > u.Input {
		u.CachedInput = u.Input
	}
	if u.Reasoning < 0 {
		u.Reasoning = 0
	}
	if u.Reasoning > u.Output {
		u.Reasoning = u.Output
	}
	if u.Source == "" {
		u.Source = "model"
	}
	if u.DurationMS <= 0 || u.TTFTMS < 0 || u.TTFTMS > u.DurationMS {
		u.DurationMS, u.HasTiming = 0, false
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}

	seqExpr := "?"
	args := []any{u.SessionID}
	if u.CallSeq <= 0 {
		// Allocate and insert in one SQLite statement. A separate SELECT then
		// INSERT can race when parallel workers finish at the same time.
		seqExpr = `(SELECT MAX(
			COALESCE((SELECT MAX(call_seq) FROM session_usage WHERE session_id = ?), 0),
			COALESCE((SELECT MAX(call_seq) FROM billing_usage WHERE session_id = ?), 0)) + 1)`
		args = append(args, u.SessionID, u.SessionID)
	} else {
		args = append(args, u.CallSeq)
	}
	args = append(args,
		u.Provider, u.ProviderType, u.EndpointHost, u.Model,
		u.Input, u.Output, u.CachedInput, u.Reasoning,
		boolInt(u.HasCachedInput), boolInt(u.HasReasoning),
		u.TTFTMS, u.DurationMS, boolInt(u.HasTiming), u.PrefillEvaluated, u.PrefillTokensPerSecond, u.PrefillBudget, u.PrefillBudgetSource,
		u.ContextWindow,
		u.ContextSystem, u.ContextUser, u.ContextAssistant, u.ContextTool, u.ContextOther,
		u.Source, u.CreatedAt.UnixNano(),
	)
	query := `INSERT INTO session_usage (
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning,
		ttft_ms, duration_ms, has_timing, prefill_evaluated_tokens, prefill_tokens_per_second,
		prefill_budget_tokens, prefill_budget_source,
		context_window, ctx_system_tokens, ctx_user_tokens, ctx_assistant_tokens, ctx_tool_tokens, ctx_other_tokens,
		source, created_at
	) VALUES (?,` + seqExpr + `,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	priceJSON, err := encodePriceSnapshot(u.PriceSnapshot, u.CreatedAt)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// INSERT is the first statement, keeping automatic sequence allocation
	// under SQLite's writer lock even when parallel workers complete.
	if err := tx.QueryRowContext(ctx, query+" RETURNING call_seq", args...).Scan(&u.CallSeq); err != nil {
		return fmt.Errorf("session.Store.AppendUsage insert: %w", err)
	}
	// Legacy callers without a price retain a raw journal row. Their quote is
	// still nil until explicit backfill, but deletion cannot erase the call.
	if err := insertBillingUsage(ctx, tx, u, priceJSON, false); err != nil {
		return fmt.Errorf("session.Store.AppendUsage billing: %w", err)
	}
	return tx.Commit()
}

// ReadUsage returns model calls for a session in call order.
func (s *Store) ReadUsage(ctx context.Context, sessionID string) ([]UsageRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning,
		ttft_ms, duration_ms, has_timing, prefill_evaluated_tokens, prefill_tokens_per_second,
		prefill_budget_tokens, prefill_budget_source,
		context_window, ctx_system_tokens,
		ctx_user_tokens, ctx_assistant_tokens, ctx_tool_tokens, ctx_other_tokens,
		source, created_at,
		(SELECT price_snapshot FROM billing_usage b
		 WHERE b.session_id = session_usage.session_id AND b.call_seq = session_usage.call_seq)
		FROM session_usage WHERE session_id = ? ORDER BY call_seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageRecord{}
	for rows.Next() {
		var u UsageRecord
		var cachedKnown, reasoningKnown, timingKnown int
		var created int64
		var priceJSON sql.NullString
		if err := rows.Scan(
			&u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning,
			&cachedKnown, &reasoningKnown,
			&u.TTFTMS, &u.DurationMS, &timingKnown, &u.PrefillEvaluated, &u.PrefillTokensPerSecond,
			&u.PrefillBudget, &u.PrefillBudgetSource,
			&u.ContextWindow,
			&u.ContextSystem, &u.ContextUser, &u.ContextAssistant, &u.ContextTool, &u.ContextOther,
			&u.Source, &created, &priceJSON,
		); err != nil {
			return nil, err
		}
		u.HasCachedInput = cachedKnown != 0
		u.HasReasoning = reasoningKnown != 0
		u.HasTiming = timingKnown != 0
		u.CreatedAt = time.Unix(0, created).UTC()
		if u.PriceSnapshot, err = decodePriceSnapshot(priceJSON); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageSince sums persisted model calls since the supplied instant.
func (s *Store) UsageSince(ctx context.Context, since time.Time) (input, output int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM session_usage WHERE created_at >= ?`, since.UnixNano()).Scan(&input, &output)
	return
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
