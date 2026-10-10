package session

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// TryUpdateUsage adds observed counters once without waiting for SQLite's writer.
// It is for interrupted turns: ordinary writes retain the shared pool's policy.
// The caller owns the Store lifetime; the temporary pool does no migrations or
// journal-mode changes and never alters settings on a shared connection.
func (s *Store) TryUpdateUsage(ctx context.Context, sessionID string, in, out int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil || strings.TrimSpace(s.root) == "" {
		return fmt.Errorf("session.Store.TryUpdateUsage: store is not open")
	}
	if sessionID == "" {
		return fmt.Errorf("session.Store.TryUpdateUsage: sessionID is empty")
	}
	// Close keeps s.db non-nil. Borrow and immediately release a live handle so
	// this method cannot reopen a Store that was already closed by its owner.
	live, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	if err := live.Close(); err != nil {
		return err
	}

	db, err := sql.Open("sqlite", filepath.Join(s.root, "sessions.db")+"?_pragma=busy_timeout(0)&_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := db.ExecContext(ctx,
		`UPDATE sessions SET token_in = token_in + ?, token_out = token_out + ?, updated_at = ? WHERE id = ?`,
		in, out, time.Now().UTC().UnixNano(), sessionID,
	)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}
