package credits

import (
	"context"
	"fmt"
	"time"
)

// VisitLedgerSince streams daily accounting fields through the day index.
// The callback runs with rows open and must not query Storage.
func (s *Storage) VisitLedgerSince(ctx context.Context, since time.Time, visit func(LedgerEntry)) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credits: VisitLedgerSince: nil storage")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, ts, input_tokens, output_tokens, source,
		COALESCE(parent_session_id, '') FROM credit_ledger WHERE ts >= ? ORDER BY ts, id`, since.UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.SessionID, &e.TS, &e.Input, &e.Output, &e.Source, &e.ParentSessionID); err != nil {
			return err
		}
		visit(e)
	}
	return rows.Err()
}
