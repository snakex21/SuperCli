package session

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Fork creates a child session and copies the transcript through throughSeq.
// A zero sequence means the full transcript. Usage is deliberately not copied:
// the branch only accounts for calls made after it diverges.
func (s *Store) Fork(ctx context.Context, sourceID string, throughSeq int, provider, model, reasoning string) (Session, error) {
	source, err := s.Get(strings.TrimSpace(sourceID))
	if err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(model) == "" {
		model = source.Model
	}
	if strings.TrimSpace(provider) == "" {
		provider = source.Provider
	}
	if reasoning == "" {
		reasoning = source.ReasoningEffort
	}
	id, now := newID(), time.Now().UTC()
	ns := now.UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	title := strings.TrimSpace(source.Title)
	if title == "" {
		title = "Branch"
	} else {
		title += " · branch"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,cwd,title,model,provider,reasoning_effort,parent_id,created_at,updated_at,message_count,token_in,token_out) VALUES(?,?,?,?,?,?,?,?,?,?,0,0)`, id, source.Cwd, title, model, provider, reasoning, source.ID, ns, ns, 0); err != nil {
		return Session{}, fmt.Errorf("fork session: %w", err)
	}
	limitClause, args := "", []any{id, source.ID}
	if throughSeq > 0 {
		limitClause = " AND seq <= ?"
		args = append(args, throughSeq)
	}
	q := `INSERT INTO messages(session_id,seq,role,content,parts_json,tool_call_id,tool_calls_json,name,created_at) SELECT ?,seq,role,content,parts_json,tool_call_id,tool_calls_json,name,created_at FROM messages WHERE session_id=?` + limitClause + ` ORDER BY seq`
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return Session{}, fmt.Errorf("fork messages: %w", err)
	}
	n, _ := res.RowsAffected()
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET message_count=? WHERE id=?`, n, id); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return Session{ID: id, Cwd: source.Cwd, Title: title, Model: model, Provider: provider, ReasoningEffort: reasoning, ParentID: source.ID, CreatedAt: now, UpdatedAt: now, MessageCount: int(n)}, nil
}

// Children returns direct branches for a session.
func (s *Store) Children(ctx context.Context, parentID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,cwd,title,model,provider,reasoning_effort,IFNULL(parent_id,''),created_at,updated_at,message_count,token_in,token_out FROM sessions WHERE parent_id=? ORDER BY created_at`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var v Session
		var created, updated int64
		if err := rows.Scan(&v.ID, &v.Cwd, &v.Title, &v.Model, &v.Provider, &v.ReasoningEffort, &v.ParentID, &created, &updated, &v.MessageCount, &v.TokenIn, &v.TokenOut); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(0, created).UTC()
		v.UpdatedAt = time.Unix(0, updated).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}
