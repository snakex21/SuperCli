package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TurnSummary is the compact, durable telemetry shown below one assistant
// response. AssistantSeq ties it to the transcript without storing prompts or
// duplicating message content.
type TurnSummary struct {
	SessionID       string
	AssistantSeq    int
	DurationMS      int64
	Input           int64
	Output          int64
	CachedInput     int64
	Reasoning       int64
	HasCachedInput  bool
	HasReasoning    bool
	ToolCalls       int
	ToolFailures    int
	Steps           int
	ModelCalls      int
	FailedCalls     int
	CanceledCalls   int
	BackgroundCalls int
	HelperCalls     int
	Phases          map[string]int64
	FileChanges     []FileChange
	CreatedAt       time.Time
}

type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
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
	turn.ToolFailures = max(turn.ToolFailures, 0)
	turn.Steps = max(turn.Steps, 0)
	turn.ModelCalls = max(turn.ModelCalls, 0)
	turn.FailedCalls = max(turn.FailedCalls, 0)
	turn.CanceledCalls = max(turn.CanceledCalls, 0)
	turn.BackgroundCalls = max(turn.BackgroundCalls, 0)
	turn.HelperCalls = max(turn.HelperCalls, 0)
	phases, err := json.Marshal(turn.Phases)
	if err != nil {
		return fmt.Errorf("session.Store.AppendTurnSummary phases: %w", err)
	}
	fileChanges, err := json.Marshal(turn.FileChanges)
	if err != nil {
		return fmt.Errorf("session.Store.AppendTurnSummary file changes: %w", err)
	}
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
		tool_calls, tool_failures, steps, model_calls, failed_model_calls,
		canceled_model_calls, background_calls, helper_calls, phases_json,
		file_changes_json, created_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(session_id, assistant_seq) DO UPDATE SET
		duration_ms=excluded.duration_ms, input_tokens=excluded.input_tokens,
		output_tokens=excluded.output_tokens, cached_input_tokens=excluded.cached_input_tokens,
		reasoning_tokens=excluded.reasoning_tokens, has_cached_input=excluded.has_cached_input,
		has_reasoning=excluded.has_reasoning, tool_calls=excluded.tool_calls,
		tool_failures=excluded.tool_failures, steps=excluded.steps,
		model_calls=excluded.model_calls, failed_model_calls=excluded.failed_model_calls,
		canceled_model_calls=excluded.canceled_model_calls,
		background_calls=excluded.background_calls, helper_calls=excluded.helper_calls,
		phases_json=excluded.phases_json, file_changes_json=excluded.file_changes_json,
		created_at=excluded.created_at`,
		turn.SessionID, turn.AssistantSeq, turn.DurationMS, turn.Input, turn.Output,
		turn.CachedInput, turn.Reasoning, boolInt(turn.HasCachedInput), boolInt(turn.HasReasoning),
		turn.ToolCalls, turn.ToolFailures, turn.Steps, turn.ModelCalls, turn.FailedCalls,
		turn.CanceledCalls, turn.BackgroundCalls, turn.HelperCalls, string(phases), string(fileChanges),
		turn.CreatedAt.UnixNano())
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
	return s.ReadTurnSummariesRange(ctx, sessionID, 0, 0)
}

// ReadRecentTurnSummaries returns a bounded cross-session sample for local
// performance diagnosis. It is called only when the Usage panel is opened.
func (s *Store) ReadRecentTurnSummaries(ctx context.Context, since time.Time, limit int) ([]TurnSummary, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, assistant_seq, duration_ms,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, tool_calls, tool_failures, steps,
		model_calls, failed_model_calls, canceled_model_calls, background_calls,
		helper_calls, phases_json, file_changes_json, created_at
		FROM session_turns WHERE created_at >= ? ORDER BY created_at DESC LIMIT ?`, since.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTurnSummaries(rows)
}

// ReadTurnSummariesRange returns telemetry attached to assistant messages in
// the inclusive sequence range. Non-positive bounds are left open.
func (s *Store) ReadTurnSummariesRange(ctx context.Context, sessionID string, fromSeq, toSeq int) ([]TurnSummary, error) {
	query := `SELECT session_id, assistant_seq, duration_ms,
		input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
		has_cached_input, has_reasoning, tool_calls, tool_failures, steps,
		model_calls, failed_model_calls, canceled_model_calls, background_calls,
		helper_calls, phases_json, file_changes_json, created_at
		FROM session_turns WHERE session_id = ?`
	args := []any{sessionID}
	if fromSeq > 0 {
		query += ` AND assistant_seq >= ?`
		args = append(args, fromSeq)
	}
	if toSeq > 0 {
		query += ` AND assistant_seq <= ?`
		args = append(args, toSeq)
	}
	query += ` ORDER BY assistant_seq`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTurnSummaries(rows)
}

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanTurnSummaries(rows rowScanner) ([]TurnSummary, error) {
	out := []TurnSummary{}
	for rows.Next() {
		var turn TurnSummary
		var cached, reasoning int
		var phases, fileChanges string
		var created int64
		if err := rows.Scan(&turn.SessionID, &turn.AssistantSeq, &turn.DurationMS,
			&turn.Input, &turn.Output, &turn.CachedInput, &turn.Reasoning,
			&cached, &reasoning, &turn.ToolCalls, &turn.ToolFailures, &turn.Steps,
			&turn.ModelCalls, &turn.FailedCalls, &turn.CanceledCalls,
			&turn.BackgroundCalls, &turn.HelperCalls, &phases, &fileChanges, &created); err != nil {
			return nil, err
		}
		if phases != "" {
			_ = json.Unmarshal([]byte(phases), &turn.Phases)
		}
		if fileChanges != "" {
			_ = json.Unmarshal([]byte(fileChanges), &turn.FileChanges)
		}
		turn.HasCachedInput = cached != 0
		turn.HasReasoning = reasoning != 0
		turn.CreatedAt = time.Unix(0, created).UTC()
		out = append(out, turn)
	}
	return out, rows.Err()
}
