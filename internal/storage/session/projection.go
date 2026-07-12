package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"supercli/internal/llm"
)

// SaveContextProjection stores the exact message view currently sent to
// the model and the last transcript sequence it covers. Full messages
// remain untouched in messages for UI/export/search.
func (s *Store) SaveContextProjection(ctx context.Context, sessionID string, msgs []llm.Message) error {
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("projection message %d: %w", i, err)
		}
	}
	raw, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("projection marshal: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var through int
	if err := tx.QueryRowContext(ctx, `SELECT IFNULL(MAX(seq), 0) FROM messages WHERE session_id = ?`, sessionID).Scan(&through); err != nil {
		return fmt.Errorf("projection boundary: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_context_projections(session_id, through_seq, messages_json, updated_at)
		VALUES(?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET
			through_seq=excluded.through_seq,
			messages_json=excluded.messages_json,
			updated_at=excluded.updated_at`, sessionID, through, raw, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("projection save: %w", err)
	}
	return tx.Commit()
}

// ReadModelContext returns the last saved model projection plus messages
// appended after its boundary. A missing or corrupt projection fails open
// to the full transcript: resume must remain usable even after disk damage.
func (s *Store) ReadModelContext(ctx context.Context, sessionID string) ([]llm.Message, error) {
	var through int
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT through_seq, messages_json FROM session_context_projections WHERE session_id = ?`, sessionID).Scan(&through, &raw)
	if err != nil {
		return s.readFullModelContext(ctx, sessionID)
	}
	var out []llm.Message
	if json.Unmarshal(raw, &out) != nil || !validMessages(out) {
		return s.readFullModelContext(ctx, sessionID)
	}
	rows, err := s.readMessagesAfter(ctx, sessionID, through)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		m, err := row.ToMessage()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) readFullModelContext(ctx context.Context, sessionID string) ([]llm.Message, error) {
	rows, err := s.ReadMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Message, 0, len(rows))
	for _, row := range rows {
		m, err := row.ToMessage()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) readMessagesAfter(ctx context.Context, sessionID string, seq int) ([]Encoded, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, seq, role, content, IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'') FROM messages WHERE session_id = ? AND seq > ? ORDER BY seq ASC`, sessionID, seq)
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
	return out, rows.Err()
}

func validMessages(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Validate() != nil {
			return false
		}
	}
	return true
}
