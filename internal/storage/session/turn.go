package session

import (
	"context"
	"fmt"
	"time"
)

// TurnSummary is the compact, durable telemetry shown below one assistant
// response. AssistantSeq ties it to the transcript without storing prompts or
// duplicating message content.
type TurnSummary struct {
	SessionID      string
	AssistantSeq   int
	DurationMS     int64
	Input          int64
	Output         int64
	CachedInput    int64
	Reasoning      int64
	HasCachedInput bool
	HasReasoning   bool
	ToolCalls      int
	CreatedAt      time.Time
}

// AppendTurnSummary attaches telemetry to the latest assistant message in a
// session. The lookup and insert share one transaction so a resumed turn can
// never be attached to a response from another concurrent append.
func (s *Store) AppendTurnSummary(ctx context.Context, turn TurnSummary) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.AppendTurnSummary: nil store")
	}
	if turn.SessionID == "" {
		return fmt.Errorf("session.Store.AppendTurnSummary: empty session id")
	}
	turn.DurationMS = max(turn.DurationMS, 0)
	turn.Input = max(turn.Input, 0)
	turn.Output = max(turn.Output, 0)
	turn.CachedInput = min(max(turn.CachedInput, 0), turn.Input)
	turn.Reasoning = min(max(turn.Reasoning, 0), turn.Output)
	turn.ToolCalls = max(turn.ToolCalls, 0)
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("session.Store.AppendTurnSummary begin: %w", err)
	}
	defer tx.Rollback()
	if turn.AssistantSeq <= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM messages WHERE session_id = ? AND role = 'assistant'`, turn.SessionID).Scan(&turn.AssistantSeq); err != nil {
			return fmt.Errorf("session.Store.AppendTurnSummary assistant: %w", err)
		}
	}
	if turn.AssistantSeq <= 0 {
		return fmt.Errorf("session.Store.AppendTurnSummary: no assistant message")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_turns (
		session_id, assistant_seq, duration_ms, input_tokens, output_tokens,
		cached_input_tokens, reasoning_tokens, has_cached_input, has_reasoning,
		tool_calls, created_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(session_id, assistant_seq) DO UPDATE SET
		duration_ms=excluded.duration_ms, input_tokens=excluded.input_tokens,
		output_tokens=excluded.output_tokens, cached_input_tokens=excluded.cached_input_tokens,
		reasoning_tokens=excluded.reasoning_tokens, has_cached_input=excluded.has_cached_input,
		has_reasoning=excluded.has_reasoning, tool_calls=excluded.tool_calls,
		created_at=excluded.created_at`,
		turn.SessionID, turn.AssistantSeq, turn.DurationMS, turn.Input, turn.Output,
		turn.CachedInput, turn.Reasoning, boolInt(turn.HasCachedInput), boolInt(turn.HasReasoning),
		turn.ToolCalls, turn.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("session.Store.AppendTurnSummary insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("session.Store.AppendTurnSummary commit: %w", err)
	}
	return nil
}

// ReadTurnSummaries returns persisted per-response telemetry in transcript
// order. Sessions created before this table existed simply return an empty
// slice.
func (s *Store) ReadTurnSummaries(ctx context.Context, sessionID string) ([]TurnSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, assistant_seq, duration_ms,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, tool_calls, created_at
		FROM session_turns WHERE session_id = ? ORDER BY assistant_seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TurnSummary{}
	for rows.Next() {
		var turn TurnSummary
		var cached, reasoning int
		var created int64
		if err := rows.Scan(&turn.SessionID, &turn.AssistantSeq, &turn.DurationMS,
			&turn.Input, &turn.Output, &turn.CachedInput, &turn.Reasoning,
			&cached, &reasoning, &turn.ToolCalls, &created); err != nil {
			return nil, err
		}
		turn.HasCachedInput = cached != 0
		turn.HasReasoning = reasoning != 0
		turn.CreatedAt = time.Unix(0, created).UTC()
		out = append(out, turn)
	}
	return out, rows.Err()
}
