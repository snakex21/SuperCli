package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ReadDiscoveredTools reads the small discovery snapshot, not full message
// history. Old sessions without a snapshot simply start with no discovered tools.
func (s *Store) ReadDiscoveredTools(ctx context.Context, sessionID string) ([]string, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT names_json FROM session_tool_discovery WHERE session_id = ?", sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("tool discovery decode: %w", err)
	}
	return names, nil
}

func (w *Writer) ReadDiscoveredTools(ctx context.Context) ([]string, error) {
	return w.store.ReadDiscoveredTools(ctx, w.sessionID)
}

func (w *Writer) SaveDiscoveredTools(ctx context.Context, names []string) error {
	raw, err := json.Marshal(names)
	if err != nil {
		return err
	}
	_, err = w.store.db.ExecContext(ctx, `
  INSERT INTO session_tool_discovery(session_id,names_json) VALUES(?,?)
  ON CONFLICT(session_id) DO UPDATE SET names_json=excluded.names_json`, w.sessionID, raw)
	return err
}
