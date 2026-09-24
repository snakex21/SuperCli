package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	maxToolOutputBytes  = 16 * 1024 * 1024
	maxSavedOutputBytes = 64 * 1024 * 1024
	maxSavedOutputs     = 256
)

// SaveToolOutput retains one immutable output in the existing portable database.
// This is a bounded cache, not part of the model projection or full-text index.
// New results evict the oldest rows using only their small size metadata.
func (w *Writer) SaveToolOutput(ctx context.Context, handle, text string) error {
	if handle == "" || len(handle) > 96 || len(text) > maxToolOutputBytes {
		return fmt.Errorf("invalid tool output or size limit exceeded")
	}
	tx, err := w.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO tool_outputs(handle,session_id,content,bytes) VALUES(?,?,?,?)",
		handle, w.sessionID, []byte(text), len(text)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tool_outputs WHERE id IN (
  SELECT id FROM (
   SELECT id, ROW_NUMBER() OVER (ORDER BY id DESC) AS position,
    SUM(bytes) OVER (ORDER BY id DESC) AS total_bytes FROM tool_outputs
  ) WHERE position > ? OR total_bytes > ?
 )`, maxSavedOutputs, maxSavedOutputBytes); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadToolOutput resolves an opaque reference carried by conversation history.
// References may come from a resumed/forked session or a worker, so lookup uses
// the handle rather than the currently selected session. There is no list API.
// Deleting the owning session removes its cache rows; evicted references expire.
func (w *Writer) ReadToolOutput(ctx context.Context, handle string) (string, error) {
	// Scan directly into the final string: []byte would clone the blob before
	// a second copy converts it to the immutable text returned to callers.
	var text string
	err := w.store.db.QueryRowContext(ctx, "SELECT content FROM tool_outputs WHERE handle = ?", handle).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("unknown or expired output handle %q", handle)
	}
	if err != nil {
		return "", err
	}
	return text, nil
}
