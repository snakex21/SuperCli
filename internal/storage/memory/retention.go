package memory

import (
	"fmt"
	"strings"
)

// Retain keeps the newest keep entries in one scope and removes older rows
// from the live SQLite, FTS and vector indexes. The Markdown mirror is rewritten
// once, so trimming a legacy oversized scope is O(n), not one whole-file rewrite
// per deleted entry. Durable scopes are only trimmed when a caller explicitly
// asks; automatic callers use this for task-log/raw-log/scratch retention.
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

	remove := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		remove[id] = struct{}{}
	}
	path, _, err := ScopeFile(s.markdownRoot(), scope)
	if err != nil {
		return 0, err
	}
	entries, err := mdRead(path)
	if err != nil {
		return 0, err
	}
	kept := entries[:0]
	for _, entry := range entries {
		if _, drop := remove[entry.ID]; !drop {
			kept = append(kept, entry)
		}
	}
	if err := mdWrite(path, kept); err != nil {
		return 0, fmt.Errorf("memory.Store.Retain(%s): rewrite mirror: %w", scope, err)
	}
	positions, err := mdRead(path)
	if err != nil {
		return 0, err
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
	for _, entry := range positions {
		if _, err := tx.Exec(`UPDATE memory_entries SET file_path = ?, line_start = ?, line_end = ? WHERE id = ?`, path, entry.LineStart, entry.LineEnd, entry.ID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The vector table is optional; deletion is deliberately best-effort.
	for _, id := range ids {
		s.afterDelete(id)
	}
	return len(ids), nil
}
