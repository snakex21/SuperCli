package memory

import (
	"fmt"
	"strings"
)

// Retain keeps the newest keep entries in one scope and removes older rows
// from the live SQLite, FTS and vector indexes. SQLite deletion and the mirror
// outbox commit together; Markdown is regenerated once afterward. Durable
// scopes are only trimmed when a caller explicitly asks; automatic callers use
// this for task-log/raw-log/scratch retention.
func (s *Store) Retain(scope string, keep int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("memory.Store.Retain: store is not open")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return 0, fmt.Errorf("memory.Store.Retain: scope is empty")
	}
	if keep < 0 {
		return 0, fmt.Errorf("memory.Store.Retain: keep must be >= 0")
	}
	rows, err := s.db.Query(`
		SELECT id FROM memory_entries
		WHERE scope = ?
		ORDER BY updated_at DESC, created_at DESC, id
		LIMIT -1 OFFSET ?`, scope, keep)
	if err != nil {
		return 0, fmt.Errorf("memory.Store.Retain(%s): list overflow: %w", scope, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM memory_entries WHERE id = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM memory_fts WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := enqueueMirrorTx(tx, scope); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The vector table is optional; deletion is deliberately best-effort.
	for _, id := range ids {
		s.afterDelete(id)
	}
	if err := s.drainMirrorOutboxLocked(); err != nil {
		return len(ids), fmt.Errorf("memory.Store.Retain(%s): %w", scope, err)
	}
	return len(ids), nil
}
