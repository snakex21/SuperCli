package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ReplaceQueuedTasks atomically replaces a snapshot of rows. Missing rows abort
// the transaction, so two devices merging the same queue cannot duplicate it.
// Attachments transfer ownership; no staged file is removed here.
func (s *Store) ReplaceQueuedTasks(ctx context.Context, cwd string, ids []string, batches []QueuedTask) ([]QueuedTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	pos := 0
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("duplicate queue id")
		}
		seen[id] = true
		var p int
		if err := tx.QueryRowContext(ctx, `SELECT position FROM prompt_queue WHERE cwd=? AND id=?`, cwd, id).Scan(&p); err != nil {
			return nil, err
		}
		if pos == 0 || p < pos {
			pos = p
		}
	}
	if len(ids) == 0 || len(batches) == 0 {
		return nil, fmt.Errorf("empty queue replacement")
	}
	// Leave later/unselected rows ordered after the replacement batches.
	if _, err := tx.ExecContext(ctx, `UPDATE prompt_queue SET position=position+? WHERE cwd=? AND position>=?`, len(batches), cwd, pos); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM prompt_queue WHERE cwd=? AND id=?`, cwd, id); err != nil {
			return nil, err
		}
	}
	out := make([]QueuedTask, len(batches))
	for i, batch := range batches {
		batch.ID, batch.Cwd, batch.Position, batch.CreatedAt = "merged-"+newID(), cwd, pos+i, time.Now().UTC()
		batch.Merged = true
		raw, err := json.Marshal(batch.Attachments)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_queue(id,cwd,session_id,prompt,position,created_at,attachments_json) VALUES(?,?,?,?,?,?,?)`, batch.ID, cwd, nullable(batch.SessionID), batch.Prompt, batch.Position, batch.CreatedAt.UnixNano(), string(raw)); err != nil {
			return nil, err
		}
		out[i] = batch
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
