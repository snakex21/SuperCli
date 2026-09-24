package session

import (
	"context"
	"time"
)

// PreserveLegacyUsage keeps pre-ledger CLI totals visible when a resumed
// conversation starts receiving detailed usage records. The single conditional
// insert is idempotent, including concurrent resume attempts.
func (s *Store) PreserveLegacyUsage(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
 INSERT INTO session_usage(session_id,call_seq,provider,model,input_tokens,output_tokens,source,created_at)
 SELECT id,1,IFNULL(provider,''),model,token_in,token_out,'legacy',?
 FROM sessions
 WHERE id=? AND (token_in>0 OR token_out>0)
 AND NOT EXISTS(SELECT 1 FROM session_usage WHERE session_id=sessions.id)`, time.Now().UTC().UnixNano(), id)
	return err
}
