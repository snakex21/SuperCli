package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// There is deliberately no sessions foreign key: deleting a conversation
// removes its transcript, but cannot erase already accounted model calls.
var billingSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS billing_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		call_seq INTEGER NOT NULL,
		provider TEXT NOT NULL DEFAULT '',
		provider_type TEXT NOT NULL DEFAULT '',
		endpoint_host TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cached_input_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		has_cached_input INTEGER NOT NULL DEFAULT 0,
		has_reasoning INTEGER NOT NULL DEFAULT 0,
		ctx_tool_tokens INTEGER NOT NULL DEFAULT 0,
		has_context_estimate INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		ttft_ms INTEGER NOT NULL DEFAULT 0,
		has_timing INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'model',
		created_at INTEGER NOT NULL,
		usage_day TEXT NOT NULL,
		price_snapshot TEXT,
		UNIQUE(session_id, call_seq)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_billing_usage_created ON billing_usage(created_at, id)`,
	`CREATE INDEX IF NOT EXISTS idx_billing_usage_session ON billing_usage(session_id, created_at, id)`,
	`CREATE TABLE IF NOT EXISTS billing_meta (name TEXT PRIMARY KEY, value INTEGER NOT NULL)`,
}

type billingExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertBillingUsage(ctx context.Context, db billingExecutor, u UsageRecord, priceJSON string, ignoreDuplicate bool) error {
	verb := "INSERT"
	if ignoreDuplicate {
		verb = "INSERT OR IGNORE"
	}
	day := u.CreatedAt.In(time.Local).Format("2006-01-02")
	var priceValue any
	if priceJSON != "" {
		priceValue = priceJSON
		if u.PriceSnapshot != nil && u.PriceSnapshot.UsageDay != "" {
			day = u.PriceSnapshot.UsageDay
		}
	}
	_, err := db.ExecContext(ctx, verb+` INTO billing_usage (
		session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, ctx_tool_tokens, has_context_estimate, duration_ms, ttft_ms, has_timing, source, created_at, usage_day, price_snapshot
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.SessionID, u.CallSeq, u.Provider, u.ProviderType, u.EndpointHost, u.Model,
		u.Input, u.Output, u.CachedInput, u.Reasoning, boolInt(u.HasCachedInput), boolInt(u.HasReasoning),
		max(u.ContextTool, 0), boolInt(hasRequestContextEstimate(u)),
		u.DurationMS, u.TTFTMS, boolInt(u.HasTiming),
		u.Source, u.CreatedAt.UnixNano(), day, priceValue)
	return err
}

// VisitBilling streams immutable accounting records, including calls whose
// conversation has since been deleted. An empty session selects all sessions;
// zero since means no time filter. The callback must not query Store while the
// rows are open, matching VisitUsageForStats's streaming contract.
func (s *Store) VisitBilling(ctx context.Context, sessionID string, since time.Time, visit func(UsageRecord)) error {
	return s.visitBilling(ctx, sessionID, since, true, visit)
}

// VisitTokenUsage reads the same durable calls without selecting or decoding
// immutable pricing JSON. Token panels do not need price or exchange work.
func (s *Store) VisitTokenUsage(ctx context.Context, sessionID string, visit func(UsageRecord)) error {
	return s.visitBilling(ctx, sessionID, time.Time{}, false, visit)
}

func (s *Store) visitBilling(ctx context.Context, sessionID string, since time.Time, withPrice bool, visit func(UsageRecord)) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.VisitBilling: nil store")
	}
	priceColumn := "NULL"
	if withPrice {
		priceColumn = "price_snapshot"
	}
	query := `SELECT session_id, call_seq, provider, provider_type, endpoint_host, model,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, ctx_tool_tokens, has_context_estimate, duration_ms, ttft_ms, has_timing, source, created_at, ` + priceColumn + `
		FROM billing_usage WHERE 1=1`
	var args []any
	if sessionID != "" {
		query += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if !since.IsZero() {
		query += " AND created_at >= ?"
		args = append(args, since.UnixNano())
	}
	query += " ORDER BY created_at, id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u UsageRecord
		var cachedKnown, reasoningKnown int
		var contextKnown, timingKnown int
		var created int64
		var priceJSON sql.NullString
		if err := rows.Scan(&u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning, &cachedKnown, &reasoningKnown, &u.ContextTool, &contextKnown, &u.DurationMS, &u.TTFTMS, &timingKnown, &u.Source, &created, &priceJSON); err != nil {
			return err
		}
		u.HasCachedInput, u.HasReasoning = cachedKnown != 0, reasoningKnown != 0
		u.ContextEstimateKnown = contextKnown != 0
		u.HasTiming = timingKnown != 0
		u.CreatedAt = time.Unix(0, created).UTC()
		if u.PriceSnapshot, err = decodePriceSnapshot(priceJSON); err != nil {
			return err
		}
		if visit != nil {
			visit(u)
		}
	}
	return rows.Err()
}

func hasRequestContextEstimate(u UsageRecord) bool {
	return u.ContextEstimateKnown || u.ContextSystem > 0 || u.ContextUser > 0 ||
		u.ContextAssistant > 0 || u.ContextTool > 0 || u.ContextOther > 0
}
