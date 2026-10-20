package session

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"supercli/internal/llm"
)

const maxRequestTraceJSON = 4096

func encodeRequestTrace(trace *llm.RequestTrace) string {
	safe := llm.SafeRequestTrace(trace)
	if safe == nil {
		return ""
	}
	data, err := json.Marshal(safe)
	if err != nil || len(data) > maxRequestTraceJSON {
		return ""
	}
	return string(data)
}

func decodeRequestTrace(data string) *llm.RequestTrace {
	if data == "" || len(data) > maxRequestTraceJSON {
		return nil
	}
	var trace llm.RequestTrace
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&trace); err != nil {
		return nil
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil
	}
	return llm.SafeRequestTrace(&trace)
}

// ReadRecentUsage returns only measured session rows, newest first. The existing
// (session_id, call_seq) index bounds the query without loading session history.
// Traces disappear with the session and are never sourced from billing history.
func (s *Store) ReadRecentUsage(ctx context.Context, sessionID string, limit int) ([]UsageRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, usageSelect+` WHERE session_id = ? AND source != 'legacy' ORDER BY call_seq DESC LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUsageRows(rows)
}
