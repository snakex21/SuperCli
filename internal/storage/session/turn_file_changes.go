package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// UpdateTurnFileChanges updates only the exact persisted response selected by
// the foreground run. Late workers cannot replace newer telemetry or create
// an orphan turn after a conversation was deleted or rewound.
func (s *Store) UpdateTurnFileChanges(ctx context.Context, id string, seq int, rowID int64, changes []FileChange) error {
	if s == nil || s.db == nil || id == "" || seq <= 0 || rowID <= 0 {
		return fmt.Errorf("session.UpdateTurnFileChanges: store, session and response sequence required")
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE session_turns SET file_changes_json = ? WHERE session_id = ? AND assistant_seq = ? AND id = ? AND checkpoint_binding = ''", string(encoded), id, seq, rowID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
