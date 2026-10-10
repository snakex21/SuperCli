package session

import (
	"context"
	"database/sql"
)

// TranscriptMessage keeps the physical ID in the same SQLite snapshot as its
// payload. A separate lookup by Seq could identify a replacement after rewind.
// The persisted/LLM Encoded contract remains unchanged.
type TranscriptMessage struct {
	Encoded
	MessageID int64
}

const transcriptSelect = `SELECT id, session_id, seq, role, content, IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'') FROM messages WHERE session_id = ?`

func (s *Store) readTranscriptMessages(ctx context.Context, capacity int, query string, args ...any) ([]TranscriptMessage, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TranscriptMessage
	if capacity > 0 {
		out = make([]TranscriptMessage, 0, capacity)
	}
	for rows.Next() {
		var m TranscriptMessage
		if err := rows.Scan(&m.MessageID, &m.SessionID, &m.Seq, &m.Role, &m.Content, &m.PartsJSON, &m.ToolCallID, &m.ToolCallsJSON, &m.Name); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ReadTranscriptMessages(ctx context.Context, sessionID string) ([]TranscriptMessage, error) {
	return s.readTranscriptMessages(ctx, 0, transcriptSelect+` ORDER BY seq ASC`, sessionID)
}

func (s *Store) ReadTranscriptMessageAt(ctx context.Context, sessionID string, seq int) (TranscriptMessage, error) {
	rows, err := s.readTranscriptMessages(ctx, 1, transcriptSelect+` AND seq = ?`, sessionID, seq)
	if err != nil {
		return TranscriptMessage{}, err
	}
	if len(rows) == 0 {
		return TranscriptMessage{}, sql.ErrNoRows
	}
	return rows[0], nil
}

func (s *Store) ReadTranscriptMessagesBefore(ctx context.Context, sessionID string, beforeSeq, limit int) ([]TranscriptMessage, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query, args := transcriptSelect, []any{sessionID}
	if beforeSeq > 0 {
		query += ` AND seq < ?`
		args = append(args, beforeSeq)
	}
	args = append(args, limit+1)
	out, err := s.readTranscriptMessages(ctx, limit+1, query+` ORDER BY seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out[limit] = TranscriptMessage{}
		out = out[:limit]
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out, hasMore, nil
}
