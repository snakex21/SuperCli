package session

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PreserveLegacyUsage keeps pre-ledger CLI totals visible when a resumed
// conversation starts receiving detailed usage records. The single conditional
// insert is idempotent, including concurrent resume attempts.
func (s *Store) PreserveLegacyUsage(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	u := UsageRecord{SessionID: id}
	var created int64
	err = tx.QueryRowContext(ctx, `
 INSERT INTO session_usage(session_id,call_seq,provider,model,input_tokens,output_tokens,source,created_at)
 SELECT id,1,IFNULL(provider,''),model,MAX(token_in,0),MAX(token_out,0),'legacy',updated_at
 FROM sessions
 WHERE id=? AND (token_in>0 OR token_out>0)
 AND NOT EXISTS(SELECT 1 FROM session_usage WHERE session_id=sessions.id)
 AND NOT EXISTS(SELECT 1 FROM billing_usage WHERE session_id=sessions.id)
 RETURNING call_seq,provider,model,input_tokens,output_tokens,source,created_at`, id).
		Scan(&u.CallSeq, &u.Provider, &u.Model, &u.Input, &u.Output, &u.Source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	u.CreatedAt = time.Unix(0, created).UTC()
	if err := insertBillingUsage(ctx, tx, u, "", false); err != nil {
		return err
	}
	return tx.Commit()
}
