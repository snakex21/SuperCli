package session

import (
	"context"
	"time"
)

// VisitUsageSince streams daily accounting fields through the created-at index.
// The callback runs with rows open and must not query Store.
func (s *Store) VisitUsageSince(ctx context.Context, since time.Time, visit func(UsageRecord)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, input_tokens, output_tokens, source, created_at
		FROM session_usage WHERE created_at >= ? ORDER BY created_at, id`, since.UnixNano())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u UsageRecord
		var created int64
		if err := rows.Scan(&u.SessionID, &u.Input, &u.Output, &u.Source, &created); err != nil {
			return err
		}
		u.CreatedAt = time.Unix(0, created)
		visit(u)
	}
	return rows.Err()
}
