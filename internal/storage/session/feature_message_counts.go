package session

import (
	"context"

	"supercli/internal/llm"
)

// MessageCounts counts structurally valid transcript messages, matching
// ReadMessages followed by ToMessage. Invalid encoded messages are skipped.
type MessageCounts struct {
	User, Assistant, Tool, ToolCalls int
}

// ReadMessageCounts reads only the fields needed for validation and counting.
// Plain content is represented by its emptiness: Message.Validate never inspects
// its text. octet_length uses the stored byte length, including embedded NUL,
// instead of comparing or decoding the whole text. Parts and tool calls are
// still decoded and validated normally, so
// malformed historical rows do not silently change the existing UI counters.
// No message payload or cached count is retained after this call.
func (s *Store) ReadMessageCounts(ctx context.Context, sessionID string) (MessageCounts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role,
  octet_length(content) > 0,
  IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,'')
  FROM messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return MessageCounts{}, err
	}
	defer rows.Close()
	var out MessageCounts
	for rows.Next() {
		var row Encoded
		var hasContent bool
		if err := rows.Scan(&row.Role, &hasContent, &row.PartsJSON, &row.ToolCallID, &row.ToolCallsJSON); err != nil {
			return MessageCounts{}, err
		}
		if hasContent {
			row.Content = "x"
		}
		msg, err := row.ToMessage()
		if err != nil {
			continue
		}
		switch msg.Role {
		case llm.RoleUser:
			out.User++
		case llm.RoleAssistant:
			out.Assistant++
			out.ToolCalls += len(msg.ToolCalls)
		case llm.RoleTool:
			out.Tool++
		}
	}
	if err := rows.Err(); err != nil {
		return MessageCounts{}, err
	}
	return out, nil
}
