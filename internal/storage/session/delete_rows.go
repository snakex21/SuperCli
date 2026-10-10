package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type quarantinedSessionMedia struct {
	original  string
	directory string
	journal   string
	intent    mediaDeleteIntent
}

// DeleteRows commits the SQL deletion and returns cleanup of only the media
// tree detached before commit. The caller holds StoreGate through this method,
// then releases it before cleanup. Recreating the SID cannot redirect cleanup
// to the replacement's media. It never removes an unowned/orphan media folder.
func (s *Store) DeleteRows(ctx context.Context, id string) (func() error, error) {
	_, cleanup, err := s.deleteRows(ctx, id, false)
	return cleanup, err
}

// DeleteAllRows also clears the queue. Media belonging to a session created
// after this transaction, or to an unknown SID, remains untouched.
func (s *Store) DeleteAllRows(ctx context.Context) (int, func() error, error) {
	return s.deleteRows(ctx, "", true)
}

func (s *Store) deleteRows(ctx context.Context, id string, all bool) (removed int, cleanup func() error, err error) {
	if s == nil || s.db == nil {
		return 0, nil, errors.New("session.Store.DeleteRows: nil store")
	}
	if err := s.RecoverMediaDeletes(ctx); err != nil {
		return 0, nil, err
	}
	if err := s.ensureMediaDeleteSchema(ctx); err != nil {
		return 0, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	committed := false
	commitAttempted := false
	var moved []quarantinedSessionMedia
	s.mediaMu.Lock()
	defer func() {
		if !committed {
			if !commitAttempted {
				// Before a commit attempt, retain SQLite's writer through the
				// directory rollback. No SQL commit could have become visible.
				err = errors.Join(err, restoreQuarantinedSessionMedia(moved))
			} else {
				// An uncertain Commit error must not undo a visible deletion.
				// Release the failed SQL transaction before reading its marker.
				_ = tx.Rollback()
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				for _, media := range moved {
					visible, markerErr := s.mediaDeleteCommitted(cleanupCtx, media)
					if markerErr != nil || visible {
						err = errors.Join(err, markerErr, fmt.Errorf("%w: commit outcome requires recovery for %s", ErrMediaDeleteRecovery, media.intent.OperationID))
						continue // durable intent and captured bytes remain
					}
					err = errors.Join(err, restoreQuarantinedSessionMedia([]quarantinedSessionMedia{media}))
				}
			}
		}
		_ = tx.Rollback()
		s.mediaMu.Unlock()
	}()
	query, args := `UPDATE sessions SET updated_at = updated_at`, []any{}
	if !all {
		query += ` WHERE id = ?`
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return 0, nil, err
	}
	query = `SELECT id FROM sessions`
	if !all {
		query += ` WHERE id = ?`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, nil, err
	}
	var ids []string
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil {
			break
		}
		ids = append(ids, sid)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return 0, nil, err
	}
	if all {
		if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_queue`); err != nil {
			return 0, nil, err
		}
	}
	query = `DELETE FROM sessions`
	if !all {
		query += ` WHERE id = ?`
	}
	deleted, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, nil, err
	}
	if n, err := deleted.RowsAffected(); err != nil || n != int64(len(ids)) {
		return 0, nil, errors.Join(err, errors.New("session deletion did not remove the selected rows"))
	}
	for _, sid := range ids {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		original := s.sessionMediaDir(sid)
		if _, err := os.Lstat(original); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return 0, nil, err
		}
		media, err := s.prepareMediaDelete(ctx, tx, filepath.Base(original))
		if media.journal != "" {
			moved = append(moved, media)
		}
		if err != nil {
			return 0, nil, err
		}
		if err := renameMediaDelete(original, media.directory); err != nil {
			return 0, nil, fmt.Errorf("quarantine session media: %w", err)
		}
	}
	commitAttempted = true
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	committed = true
	return len(ids), func() error {
		// No request/transaction is retained by this post-gate cleanup.
		s.mediaMu.Lock()
		defer s.mediaMu.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result error
		for _, media := range moved {
			result = errors.Join(result, s.finishCommittedMediaDelete(cleanupCtx, media))
		}
		return result
	}, nil
}

func restoreQuarantinedSessionMedia(moved []quarantinedSessionMedia) error {
	var result error
	for i := len(moved) - 1; i >= 0; i-- {
		media := moved[i]
		if _, err := os.Lstat(media.directory); os.IsNotExist(err) {
			// Before-rename failure or an already restored operation. Never
			// touch a new original when there are no captured bytes to restore.
			result = errors.Join(result, retireMediaDeleteJournal(media))
			continue
		} else if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if _, err := os.Lstat(media.original); !os.IsNotExist(err) {
			result = errors.Join(result, fmt.Errorf("%w: media rollback preserves quarantine %s: original path is occupied or unavailable", ErrMediaDeleteRecovery, media.directory))
			continue
		}
		parent := filepath.Dir(media.original)
		_, parentErr := os.Lstat(parent)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if os.IsNotExist(parentErr) {
			if err := syncMediaDeleteDir(filepath.Dir(parent)); err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		if err := renameMediaDelete(media.directory, media.original); err != nil {
			result = errors.Join(result, fmt.Errorf("restore session media from %s: %w", media.directory, err))
			continue
		}
		result = errors.Join(result, retireMediaDeleteJournal(media))
	}
	return result
}
