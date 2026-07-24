package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// QueuedTask is a user prompt waiting to be run in a workspace. Queue rows are
// intentionally independent from a browser connection, so restarting the GUI
// never loses work the user already staged.
type QueuedTask struct {
	ID        string    `json:"id"`
	Cwd       string    `json:"cwd"`
	SessionID string    `json:"session_id,omitempty"`
	Prompt    string    `json:"prompt"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) EnqueueTask(ctx context.Context, cwd, sessionID, prompt string) (QueuedTask, error) {
	cwd, prompt = strings.TrimSpace(cwd), strings.TrimSpace(prompt)
	if cwd == "" || prompt == "" {
		return QueuedTask{}, fmt.Errorf("session.Store.EnqueueTask: cwd and prompt are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QueuedTask{}, err
	}
	defer tx.Rollback()
	var pos int
	if err := tx.QueryRowContext(ctx, `SELECT IFNULL(MAX(position),0)+1 FROM prompt_queue WHERE cwd=?`, cwd).Scan(&pos); err != nil {
		return QueuedTask{}, err
	}
	id, now := newID(), time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_queue(id,cwd,session_id,prompt,position,created_at) VALUES(?,?,?,?,?,?)`, id, cwd, nullable(sessionID), prompt, pos, now.UnixNano()); err != nil {
		return QueuedTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return QueuedTask{}, err
	}
	return QueuedTask{ID: id, Cwd: cwd, SessionID: sessionID, Prompt: prompt, Position: pos, CreatedAt: now}, nil
}

func (s *Store) ListQueuedTasks(ctx context.Context, cwd string) ([]QueuedTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,cwd,IFNULL(session_id,''),prompt,position,created_at FROM prompt_queue WHERE cwd=? ORDER BY position,created_at`, strings.TrimSpace(cwd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedTask{}
	for rows.Next() {
		var q QueuedTask
		var ns int64
		if err := rows.Scan(&q.ID, &q.Cwd, &q.SessionID, &q.Prompt, &q.Position, &ns); err != nil {
			return nil, err
		}
		q.CreatedAt = time.Unix(0, ns).UTC()
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Store) DeleteQueuedTask(ctx context.Context, cwd, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM prompt_queue WHERE id=? AND cwd=?`, strings.TrimSpace(id), strings.TrimSpace(cwd))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return s.normalizeQueue(ctx, cwd)
}

func (s *Store) MoveQueuedTask(ctx context.Context, cwd, id string, position int) error {
	items, err := s.ListQueuedTasks(ctx, cwd)
	if err != nil {
		return err
	}
	if position < 0 {
		position = 0
	}
	if position >= len(items) {
		position = len(items) - 1
	}
	from := -1
	for i := range items {
		if items[i].ID == id {
			from = i
			break
		}
	}
	if from < 0 {
		return sql.ErrNoRows
	}
	item := items[from]
	items = append(items[:from], items[from+1:]...)
	items = append(items, QueuedTask{})
	copy(items[position+1:], items[position:])
	items[position] = item
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE prompt_queue SET position=? WHERE id=? AND cwd=?`, i+1, items[i].ID, cwd); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) normalizeQueue(ctx context.Context, cwd string) error {
	items, err := s.ListQueuedTasks(ctx, cwd)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE prompt_queue SET position=? WHERE id=?`, i+1, items[i].ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
