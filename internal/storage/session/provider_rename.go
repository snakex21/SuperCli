package session

import (
	"context"
	"fmt"
	"strings"
)

// RenameProvider points saved sessions at a renamed provider: the runtime
// selection snapshot (sessions.provider) and the per-session context-model
// binding. Usage and billing history keep the name that was in effect when
// the call was made. Both tables change in one transaction. It returns the
// number of session rows updated.
func (s *Store) RenameProvider(oldName, newName string) (int64, error) {
	oldName, newName = strings.TrimSpace(oldName), strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return 0, fmt.Errorf("session.Store.RenameProvider: old and new names are required")
	}
	if oldName == newName {
		return 0, nil
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE sessions SET provider = ? WHERE provider = ?`, newName, oldName)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(`UPDATE session_context_models SET provider = ? WHERE provider = ?`, newName, oldName); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
