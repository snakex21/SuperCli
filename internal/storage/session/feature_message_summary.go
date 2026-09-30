package session

import (
	"context"

	"supercli/internal/llm"
)

// MessageSummary estimates the legacy transcript without retaining its text.
// Counts and token estimates come from the same SQLite snapshot.
type MessageSummary struct {
	Counts    MessageCounts
	Breakdown llm.RequestBreakdown
}

// ReadMessageSummary decodes one row at a time. Invalid historical encodings
// are skipped exactly as with ReadMessages followed by Encoded.ToMessage.
func (s *Store) ReadMessageSummary(ctx context.Context, sessionID string) (MessageSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT role, content, IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'') FROM messages WHERE session_id = ? ORDER BY seq ASC",
		sessionID)
	if err != nil {
		return MessageSummary{}, err
	}
	defer rows.Close()
	var out MessageSummary
	for rows.Next() {
		var row Encoded
		if err := rows.Scan(&row.Role, &row.Content, &row.PartsJSON, &row.ToolCallID, &row.ToolCallsJSON, &row.Name); err != nil {
			return MessageSummary{}, err
		}
		message, err := row.ToMessage()
		if err != nil {
			continue
		}
		switch message.Role {
		case llm.RoleUser:
			out.Counts.User++
		case llm.RoleAssistant:
			out.Counts.Assistant++
			out.Counts.ToolCalls += len(message.ToolCalls)
		case llm.RoleTool:
			out.Counts.Tool++
		}
		estimate := llm.EstimateRequestBreakdown([]llm.Message{message}, nil)
		out.Breakdown.System += estimate.System
		out.Breakdown.User += estimate.User
		out.Breakdown.Assistant += estimate.Assistant
		out.Breakdown.Tool += estimate.Tool
		out.Breakdown.Other += estimate.Other
	}
	if err := rows.Err(); err != nil {
		return MessageSummary{}, err
	}
	return out, nil
}
