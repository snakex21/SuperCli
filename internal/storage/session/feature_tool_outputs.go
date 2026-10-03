package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	// SQLite text-to-BLOB casts use the database encoding. Determine it only
	// on the first save; unknown encodings retain the original byte binding.
	w.store.toolOutputEncodingOnce.Do(func() {
		var encoding string
		if err := tx.QueryRowContext(ctx, "PRAGMA encoding").Scan(&encoding); err == nil {
			w.store.toolOutputTextBindSafe = strings.EqualFold(encoding, "UTF-8")
		}
	})
	query := "INSERT INTO tool_outputs(handle,session_id,content,bytes) VALUES(?,?,?,?)"
	var content any
	if w.store.toolOutputTextBindSafe {
		query = "INSERT INTO tool_outputs(handle,session_id,content,bytes) VALUES(?,?,CAST(? AS BLOB),?)"
		content = text
	} else {
		content = []byte(text)
	}
	if _, err := tx.ExecContext(ctx, query, handle, w.sessionID, content, len(text)); err != nil {
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
	// Request text at the driver boundary so Scan does not copy a Go blob
	// into a second string. The stored BLOB and its byte-based limits stay
	// unchanged; embedded NUL and non-UTF-8 bytes are preserved.
	var text string
	err := w.store.db.QueryRowContext(ctx, "SELECT CAST(content AS TEXT) FROM tool_outputs WHERE handle = ?", handle).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("unknown or expired output handle %q", handle)
	}
	if err != nil {
		return "", err
	}
	return text, nil
}
