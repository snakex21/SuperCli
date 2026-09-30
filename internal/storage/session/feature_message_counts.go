package session

import (
	"context"
	"database/sql"

	"supercli/internal/llm"
)

// MessageCounts counts structurally valid transcript messages, matching
// ReadMessages followed by ToMessage. Invalid encoded messages are skipped.
type MessageCounts struct {
	User, Assistant, Tool, ToolCalls int
}

// Only rows with no serialized parts/calls need no JSON decoding. NULL content
// and unusual role types remain on the old validation path, preserving errors.
const plainCountEncoding = `IFNULL(parts_json,'')='' AND IFNULL(tool_calls_json,'')=''
 AND content IS NOT NULL AND typeof(role)='text'`
const messageCountsQuery = `SELECT role,COUNT(*),1,'','',''
 FROM messages WHERE session_id=? AND ` + plainCountEncoding + ` AND (
 (role COLLATE BINARY IN ('user','assistant') AND octet_length(content)>0)
 OR (role COLLATE BINARY='tool' AND octet_length(tool_call_id)>0))
 GROUP BY role
 UNION ALL
 SELECT role,0,octet_length(content)>0,
 IFNULL(parts_json,''),IFNULL(tool_call_id,''),IFNULL(tool_calls_json,'')
 FROM messages WHERE session_id=? AND NOT (` + plainCountEncoding + `)`

// ReadMessageCounts aggregates plain messages inside SQLite and validates
// structured/legacy rows with ToMessage as before. Both branches belong to one
// read statement, so concurrent appends cannot produce a mixed snapshot.
// octet_length reads stored byte length without copying full content, including
// embedded NUL. No payload or cached counter is retained after this call.
func (s *Store) ReadMessageCounts(ctx context.Context, sessionID string) (MessageCounts, error) {
	statement, err := s.messageCountStatement(ctx)
	if err != nil {
		return MessageCounts{}, err
	}
	rows, err := statement.QueryContext(ctx, sessionID, sessionID)
	if err != nil {
		return MessageCounts{}, err
	}
	defer rows.Close()
	var out MessageCounts
	for rows.Next() {
		var row Encoded
		var count int
		var hasContent bool
		if err := rows.Scan(&row.Role, &count, &hasContent, &row.PartsJSON, &row.ToolCallID, &row.ToolCallsJSON); err != nil {
			return MessageCounts{}, err
		}
		role := llm.Role(row.Role)
		if count == 0 {
			if hasContent {
				row.Content = "x"
			}
			msg, err := row.ToMessage()
			if err != nil {
				continue
			}
			role = msg.Role
			count = 1
			if role == llm.RoleAssistant {
				out.ToolCalls += len(msg.ToolCalls)
			}
		}
		switch role {
		case llm.RoleUser:
			out.User += count
		case llm.RoleAssistant:
			out.Assistant += count
		case llm.RoleTool:
			out.Tool += count
		}
	}
	if err := rows.Err(); err != nil {
		return MessageCounts{}, err
	}
	return out, nil
}

// Preparing lazily keeps startup free of unused UI work and avoids compiling
// the aggregate query again on every statistics refresh. A failed or cancelled
// preparation is not cached; subsequent calls can retry normally.
func (s *Store) messageCountStatement(ctx context.Context) (*sql.Stmt, error) {
	s.countsMu.Lock()
	defer s.countsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.countsStmt != nil {
		return s.countsStmt, nil
	}
	statement, err := s.db.PrepareContext(ctx, messageCountsQuery)
	if err == nil {
		s.countsStmt = statement
	}
	return statement, err
}
