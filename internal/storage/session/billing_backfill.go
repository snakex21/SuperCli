package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const billingBackfillBatch = 128
const billingRawCursor = "raw_usage_migration_id"
const billingPriceCursor = "price_backfill_id"

type billingBackfillRecord struct {
	id       int64
	u        UsageRecord
	usageDay string
}

// BackfillBilling freezes older raw journal rows once, including calls whose
// conversation was deleted after migration. Durable high-watermarks bound later
// scans to new IDs. Invoke at store setup, never on every stats request.
func (s *Store) BackfillBilling(ctx context.Context, quote func(UsageRecord) PriceSnapshot) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.BackfillBilling: nil store")
	}
	if quote == nil {
		return fmt.Errorf("session.Store.BackfillBilling: nil quote")
	}
	if err := s.migrateRawBillingUsage(ctx); err != nil {
		return err
	}
	cursor, end, err := s.billingCursorRange(ctx, billingPriceCursor, "billing_usage")
	if err != nil {
		return err
	}
	for cursor < end {
		rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, call_seq, provider, provider_type, endpoint_host, model,
   input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, has_cached_input, has_reasoning, source, created_at, usage_day
   FROM billing_usage WHERE id > ? AND id <= ? AND price_snapshot IS NULL ORDER BY id LIMIT ?`, cursor, end, billingBackfillBatch)
		if err != nil {
			return err
		}
		batch, err := scanBillingBackfillBatch(rows, true)
		if err != nil {
			return err
		}
		next := billingBatchEnd(batch, end)
		prices := make([]string, len(batch))
		// No rows or transaction remain open while quoting. Even a one-
		// connection pool permits the callback to consult this same Store.
		for i := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			snapshot := quote(batch[i].u)
			snapshot.Legacy, snapshot.UsageDay = true, batch[i].usageDay
			prices[i], err = encodePriceSnapshot(&snapshot, batch[i].u.CreatedAt)
			if err != nil {
				return err
			}
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for i := range batch {
			// Another backfiller may have won while the callback ran. Only the
			// first completed quote becomes permanent; never replace a price.
			if _, err = tx.ExecContext(ctx, `UPDATE billing_usage SET price_snapshot=? WHERE id=? AND price_snapshot IS NULL`, prices[i], batch[i].id); err != nil {
				break
			}
		}
		if err == nil {
			err = saveBillingCursor(ctx, tx, billingPriceCursor, next)
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

// Migration captures raw counters before the application exposes deletion.
// It requires no price lookup and preserves legacy nil-price read semantics.
func (s *Store) migrateRawBillingUsage(ctx context.Context) error {
	cursor, end, err := s.billingCursorRange(ctx, billingRawCursor, "session_usage")
	if err != nil {
		return err
	}
	for cursor < end {
		rows, err := s.db.QueryContext(ctx, `SELECT id, session_id, call_seq, provider, provider_type, endpoint_host, model,
   input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, has_cached_input, has_reasoning, source, created_at,
   MAX(ctx_tool_tokens,0), (ctx_system_tokens>0 OR ctx_user_tokens>0 OR ctx_assistant_tokens>0 OR ctx_tool_tokens>0 OR ctx_other_tokens>0),
   duration_ms, ttft_ms, has_timing
   FROM session_usage u WHERE id > ? AND id <= ?
   AND NOT EXISTS(SELECT 1 FROM billing_usage b WHERE b.session_id=u.session_id AND b.call_seq=u.call_seq)
   ORDER BY id LIMIT ?`, cursor, end, billingBackfillBatch)
		if err != nil {
			return err
		}
		batch, err := scanBillingBackfillBatch(rows, false)
		if err != nil {
			return err
		}
		next := billingBatchEnd(batch, end)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for i := range batch {
			if err = insertBillingUsage(ctx, tx, batch[i].u, "", true); err != nil {
				break
			}
		}
		if err == nil {
			err = saveBillingCursor(ctx, tx, billingRawCursor, next)
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

func (s *Store) billingCursorRange(ctx context.Context, name, table string) (cursor, end int64, err error) {
	// table comes only from the two package-local migration callers.
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM billing_meta WHERE name=?),0),
  COALESCE((SELECT MAX(id) FROM `+table+`),0)`, name).Scan(&cursor, &end)
	return
}

func saveBillingCursor(ctx context.Context, tx *sql.Tx, name string, next int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO billing_meta(name,value) VALUES(?,?)
  ON CONFLICT(name) DO UPDATE SET value=MAX(billing_meta.value,excluded.value)`, name, next)
	return err
}

func billingBatchEnd(batch []billingBackfillRecord, end int64) int64 {
	if len(batch) == 0 {
		return end
	}
	return batch[len(batch)-1].id
}

func scanBillingBackfillBatch(rows *sql.Rows, withDay bool) ([]billingBackfillRecord, error) {
	defer rows.Close()
	batch := make([]billingBackfillRecord, 0, billingBackfillBatch)
	for rows.Next() {
		var item billingBackfillRecord
		var cachedKnown, reasoningKnown int
		var contextKnown, timingKnown int
		var created int64
		u := &item.u
		fields := []any{&item.id, &u.SessionID, &u.CallSeq, &u.Provider, &u.ProviderType, &u.EndpointHost, &u.Model,
			&u.Input, &u.Output, &u.CachedInput, &u.Reasoning, &cachedKnown, &reasoningKnown, &u.Source, &created}
		if withDay {
			fields = append(fields, &item.usageDay)
		} else {
			fields = append(fields, &u.ContextTool, &contextKnown, &u.DurationMS, &u.TTFTMS, &timingKnown)
		}
		if err := rows.Scan(fields...); err != nil {
			return nil, err
		}
		u.HasCachedInput, u.HasReasoning = cachedKnown != 0, reasoningKnown != 0
		u.ContextEstimateKnown = contextKnown != 0
		u.HasTiming = timingKnown != 0
		u.CreatedAt = time.Unix(0, created).UTC()
		batch = append(batch, item)
	}
	return batch, rows.Err()
}
