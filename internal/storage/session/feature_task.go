package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	// Attachments uses the /api/chat representation: file paths, normally
	// staged uploads below <workspace>/.supercli/attachments. File bytes are
	// never copied into the queue row.
	Attachments []string `json:"attachments,omitempty"`
}

func (s *Store) EnqueueTask(ctx context.Context, cwd, sessionID, prompt string) (QueuedTask, error) {
	return s.EnqueueTaskWithAttachments(ctx, cwd, sessionID, prompt, nil)
}

// EnqueueTaskWithAttachments stores a queued prompt together with the file
// paths that must be sent with it once the queue dispatches the task.
func (s *Store) EnqueueTaskWithAttachments(ctx context.Context, cwd, sessionID, prompt string, attachments []string) (QueuedTask, error) {
	cwd, prompt = strings.TrimSpace(cwd), strings.TrimSpace(prompt)
	attachments = cleanQueuedAttachments(attachments)
	rawAttachments, err := json.Marshal(attachments)
	if err != nil {
		return QueuedTask{}, fmt.Errorf("session.Store.EnqueueTask attachments: %w", err)
	}
	if cwd == "" || (prompt == "" && len(attachments) == 0) {
		return QueuedTask{}, fmt.Errorf("session.Store.EnqueueTask: cwd and prompt or attachments are required")
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO prompt_queue(id,cwd,session_id,prompt,position,created_at,attachments_json) VALUES(?,?,?,?,?,?,?)`, id, cwd, nullable(sessionID), prompt, pos, now.UnixNano(), string(rawAttachments)); err != nil {
		return QueuedTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return QueuedTask{}, err
	}
	out := QueuedTask{ID: id, Cwd: cwd, SessionID: sessionID, Prompt: prompt, Position: pos, CreatedAt: now}
	if len(attachments) > 0 {
		out.Attachments = attachments
	}
	return out, nil
}

func cleanQueuedAttachments(paths []string) []string {
	clean := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		clean = append(clean, path)
	}
	return clean
}

// AttachmentPathInUse reports whether a transcript message or another queued
// task still references path. Callers use it before deleting staged files.
func (s *Store) AttachmentPathInUse(ctx context.Context, path string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM message_attachments, json_each(message_attachments.paths_json) WHERE json_each.value = ?
		UNION ALL SELECT 1 FROM prompt_queue, json_each(prompt_queue.attachments_json) WHERE json_each.value = ? LIMIT 1`, path, path).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) ListQueuedTasks(ctx context.Context, cwd string) ([]QueuedTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,cwd,IFNULL(session_id,''),prompt,position,created_at,IFNULL(attachments_json,'[]') FROM prompt_queue WHERE cwd=? ORDER BY position,created_at`, strings.TrimSpace(cwd))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedTask{}
	for rows.Next() {
		var q QueuedTask
		var ns int64
		var rawAttachments string
		if err := rows.Scan(&q.ID, &q.Cwd, &q.SessionID, &q.Prompt, &q.Position, &ns, &rawAttachments); err != nil {
			return nil, err
		}
		q.CreatedAt = time.Unix(0, ns).UTC()
		if rawAttachments != "" && rawAttachments != "[]" {
			if err := json.Unmarshal([]byte(rawAttachments), &q.Attachments); err != nil {
				return nil, fmt.Errorf("session.Store.ListQueuedTasks attachments %s: %w", q.ID, err)
			}
			q.Attachments = cleanQueuedAttachments(q.Attachments)
			if len(q.Attachments) == 0 {
				q.Attachments = nil
			}
		}
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

func (s *Store) UpdateQueuedTask(ctx context.Context, cwd, id, prompt string) error {
	cwd, id, prompt = strings.TrimSpace(cwd), strings.TrimSpace(id), strings.TrimSpace(prompt)
	if cwd == "" || id == "" || prompt == "" {
		return fmt.Errorf("session.Store.UpdateQueuedTask: cwd, id and prompt are required")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE prompt_queue SET prompt=? WHERE id=? AND cwd=?`, prompt, id, cwd)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
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
