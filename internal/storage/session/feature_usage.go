package session

import (
	"context"
	"fmt"
	"time"
)

// UsageRecord is one completed model request attributed to a persisted
// session. CachedInput is a subset of Input; Reasoning is a subset of Output.
// Context fields are estimates of the request payload and are never prompts.
type UsageRecord struct {
	SessionID              string
	CallSeq                int
	Provider               string
	ProviderType           string
	EndpointHost           string
	Model                  string
	Input                  int64
	Output                 int64
	CachedInput            int64
	Reasoning              int64
	HasCachedInput         bool
	HasReasoning           bool
	TTFTMS                 int64
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
	Source                 string
	CreatedAt              time.Time
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
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}

	seqExpr := "?"
	args := []any{u.SessionID}
	if u.CallSeq <= 0 {
		// Allocate and insert in one SQLite statement. A separate SELECT then
		// INSERT can race when parallel workers finish at the same time.
		seqExpr = `(SELECT COALESCE(MAX(call_seq), 0) + 1 FROM session_usage WHERE session_id = ?)`
		args = append(args, u.SessionID)
	} else {
		args = append(args, u.CallSeq)
	}
	args = append(args,
		u.Provider, u.ProviderType, u.EndpointHost, u.Model,
		u.Input, u.Output, u.CachedInput, u.Reasoning,
		boolInt(u.HasCachedInput), boolInt(u.HasReasoning),
		u.TTFTMS, u.PrefillEvaluated, u.PrefillTokensPerSecond, u.PrefillBudget, u.PrefillBudgetSource,
		u.ContextWindow,
		u.ContextSystem, u.ContextUser, u.ContextAssistant, u.ContextTool, u.ContextOther,
		u.Source, u.CreatedAt.UnixNano(),
	)
	query := `INSERT INTO session_usage (
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning,
		ttft_ms, prefill_evaluated_tokens, prefill_tokens_per_second,
		prefill_budget_tokens, prefill_budget_source,
		context_window, ctx_system_tokens, ctx_user_tokens, ctx_assistant_tokens, ctx_tool_tokens, ctx_other_tokens,
		source, created_at
	) VALUES (?,` + seqExpr + `,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("session.Store.AppendUsage insert: %w", err)
	}
	return nil
}

// ReadUsage returns model calls for a session in call order.
func (s *Store) ReadUsage(ctx context.Context, sessionID string) ([]UsageRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning,
		ttft_ms, prefill_evaluated_tokens, prefill_tokens_per_second,
		prefill_budget_tokens, prefill_budget_source,
		context_window, ctx_system_tokens,
		ctx_user_tokens, ctx_assistant_tokens, ctx_tool_tokens, ctx_other_tokens,
		source, created_at
		FROM session_usage WHERE session_id = ? ORDER BY call_seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageRecord{}
	for rows.Next() {
		var u UsageRecord
		var cachedKnown, reasoningKnown int
		var created int64
		if err := rows.Scan(
			&u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning,
			&cachedKnown, &reasoningKnown,
			&u.TTFTMS, &u.PrefillEvaluated, &u.PrefillTokensPerSecond,
			&u.PrefillBudget, &u.PrefillBudgetSource,
			&u.ContextWindow,
			&u.ContextSystem, &u.ContextUser, &u.ContextAssistant, &u.ContextTool, &u.ContextOther,
			&u.Source, &created,
		); err != nil {
			return nil, err
		}
		u.HasCachedInput = cachedKnown != 0
		u.HasReasoning = reasoningKnown != 0
		u.CreatedAt = time.Unix(0, created).UTC()
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
