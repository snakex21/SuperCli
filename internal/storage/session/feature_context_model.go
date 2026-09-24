package session

import (
	"context"
	"database/sql"
	"errors"
)

// ReadContextModel is deliberately separate from sessions.model: picker
// settings may change several times without any inference taking place.
func (w *Writer) ReadContextModel(ctx context.Context) (provider, model string, err error) {
	err = w.store.db.QueryRowContext(ctx,
		"SELECT provider, model FROM session_context_models WHERE session_id = ?", w.sessionID).Scan(&provider, &model)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func (w *Writer) SaveContextModel(ctx context.Context, provider, model string) error {
	_, err := w.store.db.ExecContext(ctx, `
  INSERT INTO session_context_models(session_id, provider, model) VALUES(?,?,?)
  ON CONFLICT(session_id) DO UPDATE SET provider=excluded.provider, model=excluded.model`,
		w.sessionID, provider, model)
	return err
}
