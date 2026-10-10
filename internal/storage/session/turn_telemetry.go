package session

import (
	"context"
	"database/sql"
	"time"
)

// LatestTurnTelemetry reads only the counters shown in the stats panel. It
// does not decode checkpoint, file-change, diagnostic or phase JSON.
func (s *Store) LatestTurnTelemetry(ctx context.Context, sessionID string) (TurnSummary, bool, error) {
	var turn TurnSummary
	var cached, reasoning int
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT session_id,assistant_seq,duration_ms,input_tokens,output_tokens,
   cached_input_tokens,reasoning_tokens,has_cached_input,has_reasoning,tool_calls,created_at
   FROM session_turns WHERE session_id=? ORDER BY assistant_seq DESC LIMIT 1`, sessionID).
		Scan(&turn.SessionID, &turn.AssistantSeq, &turn.DurationMS, &turn.Input, &turn.Output,
			&turn.CachedInput, &turn.Reasoning, &cached, &reasoning, &turn.ToolCalls, &created)
	if err == sql.ErrNoRows {
		return turn, false, nil
	}
	if err != nil {
		return turn, false, err
	}
	turn.HasCachedInput, turn.HasReasoning = cached != 0, reasoning != 0
	turn.CreatedAt = time.Unix(0, created).UTC()
	return turn, true, nil
}

// ReadRecentTurnTelemetry returns the same bounded window and counters as
// ReadRecentTurnSummaries for aggregate usage statistics. It leaves RowID,
// CheckpointBinding and FileChanges empty: those payloads are needed only by
// transcript readers and deferred checkpoint recovery, not by aggregation.
// Select constants for the skipped columns to reuse the full row scanner
// without loading or decoding potentially large file-change JSON.
func (s *Store) ReadRecentTurnTelemetry(ctx context.Context, since time.Time, limit int) ([]TurnSummary, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT 0, '', session_id, assistant_seq, duration_ms,
        input_tokens, output_tokens, cached_input_tokens, reasoning_tokens,
        has_cached_input, has_reasoning, tool_calls, tool_failures, steps,
        model_calls, failed_model_calls, canceled_model_calls, background_calls,
        helper_calls, aux_calls, aux_us, phases_json, '', tool_diag_json, created_at
        FROM session_turns WHERE created_at >= ? ORDER BY created_at DESC LIMIT ?`, since.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTurnSummaries(rows)
}
