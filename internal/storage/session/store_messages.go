// Package session persists agent conversations to SQLite and
// reconstructs them on resume. F2.c covers sessions + messages;
// checkpointing lands in F2.d.
package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (s *Store) TruncateFrom(ctx context.Context, sessionID string, fromSeq int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("session.Store.TruncateFrom: nil store")
	}
	if strings.TrimSpace(sessionID) == "" || fromSeq <= 0 {
		return 0, fmt.Errorf("session.Store.TruncateFrom: session id and positive sequence are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, sessionID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, sql.ErrNoRows
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ? AND seq >= ?`, sessionID, fromSeq)
	if err != nil {
		return 0, fmt.Errorf("truncate messages: %w", err)
	}
	removed64, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if removed64 == 0 {
		return 0, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_turns WHERE session_id = ? AND assistant_seq >= ?`, sessionID, fromSeq); err != nil {
		return 0, fmt.Errorf("truncate turn summaries: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_attachments WHERE session_id = ? AND user_seq >= ?`, sessionID, fromSeq); err != nil {
		return 0, fmt.Errorf("truncate message attachments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_context_projections WHERE session_id = ?`, sessionID); err != nil {
		return 0, fmt.Errorf("invalidate context projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET
		message_count = (SELECT COUNT(*) FROM messages WHERE session_id = ?),
		updated_at = ? WHERE id = ?`, sessionID, time.Now().UTC().UnixNano(), sessionID); err != nil {
		return 0, fmt.Errorf("update truncated session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(removed64), nil
}

// AppendMessage adds a message to a session, assigning the next
// seq number. tokenIn/tokenOut are optional (zero is fine).
func (s *Store) AppendMessage(ctx context.Context, sessionID string, msg Encoded) error {
	if sessionID == "" {
		return fmt.Errorf("session.Store.AppendMessage: sessionID is empty")
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("session.Store.AppendMessage: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var nextSeq int
	if err := tx.QueryRow(
		`SELECT IFNULL(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&nextSeq); err != nil {
		return fmt.Errorf("append: next seq: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`INSERT INTO messages(session_id, seq, role, content, parts_json, tool_call_id, tool_calls_json, name, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		sessionID, nextSeq, msg.Role, msg.Content, msg.PartsJSON, msg.ToolCallID, msg.ToolCallsJSON, msg.Name, now.UnixNano(),
	); err != nil {
		return fmt.Errorf("append: insert: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET message_count = message_count + 1, updated_at = ? WHERE id = ?`,
		now.UnixNano(), sessionID,
	); err != nil {
		return fmt.Errorf("append: bump: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// LatestMessageSeq returns the latest transcript sequence for one role. It is
// used to associate out-of-band turn metadata (such as workspace checkpoints)
// with the user message that initiated the turn.
func (s *Store) LatestMessageSeq(ctx context.Context, sessionID, role string) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("session.Store.LatestMessageSeq: nil store")
	}
	var seq int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM messages WHERE session_id = ? AND role = ?`,
		sessionID, role,
	).Scan(&seq)
	return seq, err
}

// RecentSession is a /resume listing entry. It is derived from
// the messages table directly (GROUP BY session_id) so sessions
// written by the F13 writer — which never created a sessions
// row — still show up.
type RecentSession struct {
	ID              string
	StartedAt       time.Time
	FirstUserMsg    string
	MessageCount    int
	Title           string
	Model           string
	Provider        string
	ReasoningEffort string
	ParentID        string
	// Cwd is the working directory recorded for the session (from the
	// sessions row), or "" when no sessions row exists (F13 writer
	// sessions are message-only). Populated via a LEFT JOIN so the
	// listing can attribute a session to a project.
	Cwd string
}

// ListRecent returns up to limit sessions that have at least
// one message, most recent first, with the first user message
// as a snippet source. Sessions from any project are included.
func (s *Store) ListRecent(ctx context.Context, limit int) ([]RecentSession, error) {
	return s.listRecent(ctx, "", limit)
}

// ListRecentByCwd is ListRecent filtered to sessions whose recorded
// working directory equals cwd — the backing for the TUI "current
// project" session view. Sessions with no sessions row (message-only)
// have an unknown cwd and are therefore excluded from a project filter;
// they still appear in the unfiltered ListRecent ("all") view. An empty
// cwd disables the filter and behaves like ListRecent.
func (s *Store) ListRecentByCwd(ctx context.Context, cwd string, limit int) ([]RecentSession, error) {
	return s.listRecent(ctx, cwd, limit)
}

func (s *Store) listRecent(ctx context.Context, cwd string, limit int) ([]RecentSession, error) {
	if limit <= 0 {
		limit = 10
	}
	// LEFT JOIN sessions so the cwd is available for display and
	// filtering while message-only sessions (no sessions row) still
	// appear in the unfiltered view.
	query := `
		SELECT m.session_id,
		       MIN(m.created_at) AS started,
		       COUNT(*) AS n,
		       IFNULL((SELECT content FROM messages
		               WHERE session_id = m.session_id AND role = 'user'
		                 AND content IS NOT NULL AND content <> ''
		               ORDER BY seq LIMIT 1), ''),
		       IFNULL(s.cwd, ''), IFNULL(s.title, ''), IFNULL(s.model, ''),
		       IFNULL(s.provider, ''), IFNULL(s.reasoning_effort, ''),
		       IFNULL(s.parent_id, '')
		FROM messages m
		LEFT JOIN sessions s ON s.id = m.session_id`
	args := []any{}
	if cwd != "" {
		query += `
		WHERE s.cwd = ?`
		args = append(args, cwd)
	}
	query += `
		GROUP BY m.session_id
		ORDER BY started DESC
		LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session.Store.ListRecent: %w", err)
	}
	defer rows.Close()
	var out []RecentSession
	for rows.Next() {
		var r RecentSession
		var startedNanos int64
		if err := rows.Scan(&r.ID, &startedNanos, &r.MessageCount, &r.FirstUserMsg,
			&r.Cwd, &r.Title, &r.Model, &r.Provider, &r.ReasoningEffort, &r.ParentID); err != nil {
			return nil, fmt.Errorf("session.Store.ListRecent: scan: %w", err)
		}
		r.StartedAt = time.Unix(0, startedNanos).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReadMessages returns all messages for a session, in seq order.
func (s *Store) ReadMessages(ctx context.Context, sessionID string) ([]Encoded, error) {
	rows, err := s.db.Query(
		`SELECT session_id, seq, role, content, IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'') FROM messages WHERE session_id = ? ORDER BY seq ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Encoded
	for rows.Next() {
		var m Encoded
		if err := rows.Scan(&m.SessionID, &m.Seq, &m.Role, &m.Content, &m.PartsJSON, &m.ToolCallID, &m.ToolCallsJSON, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadMessagesBefore returns the newest limit messages with seq < beforeSeq,
// in transcript order. A non-positive beforeSeq starts at the end. One extra
// row is fetched to report whether an older page exists without a COUNT scan.
func (s *Store) ReadMessagesBefore(ctx context.Context, sessionID string, beforeSeq, limit int) ([]Encoded, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT session_id, seq, role, content, IFNULL(parts_json,''),
		IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'')
		FROM messages WHERE session_id = ?`
	args := []any{sessionID}
	if beforeSeq > 0 {
		query += ` AND seq < ?`
		args = append(args, beforeSeq)
	}
	query += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]Encoded, 0, limit+1)
	for rows.Next() {
		var m Encoded
		if err := rows.Scan(&m.SessionID, &m.Seq, &m.Role, &m.Content,
			&m.PartsJSON, &m.ToolCallID, &m.ToolCallsJSON, &m.Name); err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, hasMore, nil
}

// UpdateUsage updates the cumulative token counters for a
// session. It is called after the loop emits a DoneEvent.
func (s *Store) UpdateUsage(sessionID string, in, out int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET token_in = token_in + ?, token_out = token_out + ?, updated_at = ? WHERE id = ?`,
		in, out, time.Now().UTC().UnixNano(), sessionID,
	)
	return err
}

// HistoryHit is one row returned by SearchHistory. F13.
