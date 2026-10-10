package session

import (
	"context"
	"database/sql"
	"time"
)

const billingContextCursor = "request_context_migration_id"
const billingLegacyMigration = "legacy_aggregate_migration_v1"

// Enrich surviving old journal rows once. Deleted conversations have no
// remaining request-shape estimate; their coverage stays explicitly unknown.
// No token counts, identities or frozen prices are rewritten.
func (s *Store) migrateBillingUsageDetails(ctx context.Context) error {
	cursor, end, err := s.billingCursorRange(ctx, billingContextCursor, "billing_usage")
	if err != nil {
		return err
	}
	for cursor < end {
		rows, err := s.db.QueryContext(ctx, `SELECT b.id, MAX(u.ctx_tool_tokens,0),
   (u.ctx_system_tokens>0 OR u.ctx_user_tokens>0 OR u.ctx_assistant_tokens>0 OR u.ctx_tool_tokens>0 OR u.ctx_other_tokens>0)
   FROM billing_usage b JOIN session_usage u ON u.session_id=b.session_id AND u.call_seq=b.call_seq
   WHERE b.id>? AND b.id<=? AND b.has_context_estimate=0 ORDER BY b.id LIMIT ?`, cursor, end, billingBackfillBatch)
		if err != nil {
			return err
		}
		type detail struct{ id, tool, known int64 }
		var batch []detail
		for rows.Next() {
			var d detail
			if err = rows.Scan(&d.id, &d.tool, &d.known); err != nil {
				break
			}
			batch = append(batch, d)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		next := end
		if len(batch) != 0 {
			next = batch[len(batch)-1].id
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, d := range batch {
			if _, err = tx.ExecContext(ctx, `UPDATE billing_usage SET ctx_tool_tokens=?,has_context_estimate=? WHERE id=? AND has_context_estimate=0`, d.tool, d.known, d.id); err != nil {
				break
			}
		}
		if err == nil {
			err = saveBillingCursor(ctx, tx, billingContextCursor, next)
		}
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
		if err != nil {
			return err
		}
		cursor = next
	}
	return nil
}

// Capture unequivocal pre-ledger totals at store upgrade, before deletion is
// exposed. Existing measured/journal usage prevents synthetic records. Partial
// discrepancies are reported separately instead of inventing missing calls.
func (s *Store) migrateLegacyBillingAggregates(ctx context.Context) error {
	var done int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM billing_meta WHERE name=?),0)`, billingLegacyMigration).Scan(&done); err != nil || done != 0 {
		return err
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions s WHERE (token_in>0 OR token_out>0)
   AND NOT EXISTS(SELECT 1 FROM session_usage u WHERE u.session_id=s.id)
   AND NOT EXISTS(SELECT 1 FROM billing_usage b WHERE b.session_id=s.id)
   ORDER BY id LIMIT ?`, billingBackfillBatch)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			_, err = s.db.ExecContext(ctx, `INSERT INTO billing_meta(name,value) VALUES(?,1) ON CONFLICT(name) DO NOTHING`, billingLegacyMigration)
			return err
		}
		// PreserveLegacyUsage performs the conditional insert and raw journal
		// together. Each small transaction is safe with a one-connection pool.
		for _, id := range ids {
			if err = s.PreserveLegacyUsage(ctx, id); err != nil {
				return err
			}
		}
	}
}

// LegacyUsageGap exposes counters only, without attributing old session totals
// to its last selected model or treating a discrepancy as an API call.
type LegacyUsageGap struct {
	SessionID string
	Input     int64
	Output    int64
}

func (s *Store) VisitLegacyUsageGaps(ctx context.Context, sessionID string, visit func(LegacyUsageGap)) error {
	query := `SELECT s.id, MAX(s.token_in-COALESCE(b.input,0),0), MAX(s.token_out-COALESCE(b.output,0),0)
   FROM sessions s LEFT JOIN (SELECT session_id,SUM(input_tokens) AS input,SUM(output_tokens) AS output
   FROM billing_usage`
	var args []any
	if sessionID != "" {
		query += ` WHERE session_id=?`
		args = append(args, sessionID)
	}
	query += ` GROUP BY session_id) b ON b.session_id=s.id WHERE (s.token_in>COALESCE(b.input,0) OR s.token_out>COALESCE(b.output,0))`
	if sessionID != "" {
		query += ` AND s.id=?`
		args = append(args, sessionID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var gap LegacyUsageGap
		if err := rows.Scan(&gap.SessionID, &gap.Input, &gap.Output); err != nil {
			return err
		}
		if visit != nil {
			visit(gap)
		}
	}
	return rows.Err()
}

// LatestMainUsage selects one main model call, optionally bounded to a saved
// response's timestamp and start. An unmeasured later response must not inherit
// an earlier response's model. Helpers cannot supply the main-call identity.
func (s *Store) LatestMainUsage(ctx context.Context, sessionID string, before time.Time, after ...time.Time) (UsageRecord, bool, error) {
	query := `SELECT session_id,call_seq,provider,provider_type,endpoint_host,model,
   input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,has_cached_input,has_reasoning,created_at
   FROM session_usage WHERE session_id=? AND source IN ('','model','main')`
	args := []any{sessionID}
	if !before.IsZero() {
		query += ` AND created_at<=?`
		args = append(args, before.UnixNano())
	}
	if len(after) > 0 && !after[0].IsZero() {
		query += ` AND created_at>=?`
		args = append(args, after[0].UnixNano())
	}
	query += ` ORDER BY call_seq DESC LIMIT 1`
	var u UsageRecord
	var cached, reasoning int
	var created int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
		&u.Input, &u.Output, &u.CachedInput, &u.Reasoning, &cached, &reasoning, &created)
	if err == sql.ErrNoRows {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	u.HasCachedInput, u.HasReasoning, u.CreatedAt = cached != 0, reasoning != 0, time.Unix(0, created).UTC()
	return u, true, nil
}
