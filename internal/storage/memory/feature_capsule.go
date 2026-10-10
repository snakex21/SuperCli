package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ErrTaskLogCapsuleBusy means a best-effort capsule must not wait behind another
// memory writer. The conversation transcript remains the source for a later save.
var ErrTaskLogCapsuleBusy = errors.New("memory task-log capsule writer is busy")

// SaveTaskLogCapsule atomically preserves the creation date, trims old task logs,
// and updates SQLite/FTS plus the durable Markdown outbox. It never waits on the
// Store's write or mirror locks, and does not render Markdown on the chat tail.
// Ordinary memory mutations and startup reconciliation drain that same outbox.
func (s *Store) SaveTaskLogCapsule(ctx context.Context, e Entry, keep int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return fmt.Errorf("memory.SaveTaskLogCapsule: store is unavailable")
	}
	select {
	case <-s.embedStop:
		return fmt.Errorf("memory.SaveTaskLogCapsule: store is closed")
	default:
	}
	if e.Scope != ScopeTaskLog || keep < 1 || keep > MaxTaskLogEntries {
		return fmt.Errorf("memory.SaveTaskLogCapsule: invalid scope or task-log limit")
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if !s.writeMu.TryLock() {
		return ErrTaskLogCapsuleBusy
	}
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	// A private connection fails immediately on another process's SQLite writer.
	// Do not change busy_timeout on the engine's shared pool or retain a pooled
	// connection with a different setting. The existing Store already migrated DB.
	db, err := sql.Open("sqlite", filepath.Join(s.root, "memory.db")+"?_pragma=busy_timeout(0)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var created int64
	err = tx.QueryRowContext(ctx, "SELECT created_at FROM memory_entries WHERE id = ?", e.ID).Scan(&created)
	switch {
	case err == nil:
		e.CreatedAt = time.Unix(created, 0).UTC()
	case errors.Is(err, sql.ErrNoRows):
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
	default:
		return err
	}
	e.UpdatedAt = time.Now().UTC()
	if e.Source == "" {
		e.Source = SourceUser
	}
	filePath, _, err := ScopeFile(s.markdownRoot(), e.Scope)
	if err != nil {
		return err
	}

	// Keep room for this capsule, excluding its existing row from the old tail.
	// Retention and replacement commit together, so a rejected update loses no logs.
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM memory_entries
		WHERE scope = ? AND id != ?
		ORDER BY updated_at DESC, created_at DESC, id
		LIMIT -1 OFFSET ?`, ScopeTaskLog, e.ID, keep-1)
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var vectorTable bool
	if len(removed) > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'memory_vectors'").Scan(&count); err != nil {
			return err
		}
		vectorTable = count > 0
	}
	for _, id := range removed {
		for _, table := range []string{"memory_entries", "memory_fts"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id = ?", id); err != nil {
				return err
			}
		}
		if vectorTable {
			if _, err := tx.ExecContext(ctx, "DELETE FROM memory_vectors WHERE id = ?", id); err != nil {
				return err
			}
		}
	}

	oldScope, err := checkMemoryCapacity(capsuleCapacityReader{ctx, tx}, e)
	if err != nil {
		return err
	}
	for _, table := range []string{"memory_entries", "memory_fts"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE id = ?", e.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_entries(id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Scope, filePath, 0, 0, e.Content, e.TagsCSV(), e.Source, e.CreatedAt.Unix(), e.UpdatedAt.Unix(),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO memory_fts(id, scope, content, tags) VALUES (?,?,?,?)",
		e.ID, e.Scope, e.Content, strings.Join(e.Tags, " "),
	); err != nil {
		return err
	}
	if oldScope != e.Scope {
		if err := enqueueCapsuleMirror(ctx, tx, oldScope); err != nil {
			return err
		}
	}
	if err := enqueueCapsuleMirror(ctx, tx, e.Scope); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.afterPut(e)
	return nil
}

type capsuleCapacityReader struct {
	ctx context.Context
	tx  *sql.Tx
}

func (r capsuleCapacityReader) QueryRow(query string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.ctx, query, args...)
}

func enqueueCapsuleMirror(ctx context.Context, tx *sql.Tx, scope string) error {
	if strings.TrimSpace(scope) == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory_mirror_outbox(scope, generation, enqueued_at)
		VALUES (?, 1, ?)
		ON CONFLICT(scope) DO UPDATE SET
			generation = memory_mirror_outbox.generation + 1,
			enqueued_at = excluded.enqueued_at`, scope, time.Now().UnixNano())
	return err
}
