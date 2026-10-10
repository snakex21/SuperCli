package session

import (
	"context"

	"supercli/internal/llm"
)

// ReadUserRequestHistory returns a bounded raw human-role history, rather than
// the model context projection. Only text parts are read: image payloads and
// tool output have no role in output-directory authorization.
func (w *Writer) ReadUserRequestHistory(ctx context.Context, limit int) ([]llm.Message, error) {
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	rows, err := w.store.db.QueryContext(ctx, `
		SELECT content, CASE WHEN json_valid(parts_json) THEN
			COALESCE((SELECT group_concat(json_extract(value, '$.Text'), '')
			FROM json_each(parts_json) WHERE json_extract(value, '$.Type') = 'text'), '')
			ELSE '' END
		FROM messages WHERE session_id = ? AND role = 'user'
		ORDER BY seq DESC LIMIT ?`, w.sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []llm.Message
	for rows.Next() {
		var content, partsText string
		if err := rows.Scan(&content, &partsText); err != nil {
			return nil, err
		}
		if partsText != "" {
			content = partsText
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: content})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}
