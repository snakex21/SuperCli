package session

import (
	"context"
	"time"
)

// StartBackgroundTask installs the single store-owned maintenance loop. Opening
// a raw store (tests, exports, read-only helpers) never schedules deletions.
func (s *Store) StartBackgroundTask(fn func(context.Context)) {
	s.countsMu.Lock()
	defer s.countsMu.Unlock()
	if s.housekeepingClosed || s.housekeepingDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.housekeepingCancel = cancel
	s.housekeepingDone = make(chan struct{})
	go func() { defer close(s.housekeepingDone); fn(ctx) }()
}

func (s *Store) SessionIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func days(n int) time.Duration {
	if n > 36500 {
		n = 36500
	}
	return time.Duration(n) * 24 * time.Hour
}
