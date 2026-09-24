package session

import (
	"context"
	"slices"
)

// ReadDialogueExcerpt returns the earliest usable user message (when outside
// the tail) and the last tailCount usable user/assistant messages, in sequence
// order without duplicates. Other roles are filtered in SQL before their large
// payloads are read. Both edges come from one read transaction.
//
// usable must be a pure predicate; nil accepts every dialogue row. Scanning
// stops at enough usable rows, rather than assuming every text/parts row is
// meaningful. Empty, malformed or reasoning-only messages can be skipped by
// the caller without silently losing an older useful reply.
func (s *Store) ReadDialogueExcerpt(ctx context.Context, sessionID string, tailCount int, usable func(Encoded) bool) ([]Encoded, error) {
	if tailCount <= 0 {
		tailCount = 8
	}
	if tailCount > 500 {
		tailCount = 500
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	read := func(filterOrder string, limit int) ([]Encoded, error) {
		rows, err := tx.QueryContext(ctx, `SELECT session_id, seq, role, content,
   IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,'')
   FROM messages WHERE session_id = ? AND `+filterOrder, sessionID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]Encoded, 0, limit)
		for rows.Next() {
			var m Encoded
			if err := rows.Scan(&m.SessionID, &m.Seq, &m.Role, &m.Content,
				&m.PartsJSON, &m.ToolCallID, &m.ToolCallsJSON, &m.Name); err != nil {
				return nil, err
			}
			if usable != nil && !usable(m) {
				continue
			}
			out = append(out, m)
			if len(out) == limit {
				break
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, ctx.Err()
	}
	tail, err := read("role IN ('user','assistant') ORDER BY seq DESC", tailCount)
	if err != nil {
		return nil, err
	}
	slices.Reverse(tail)
	if len(tail) == tailCount {
		first, err := read("role = 'user' ORDER BY seq ASC", 1)
		if err != nil {
			return nil, err
		}
		if len(first) > 0 && first[0].Seq < tail[0].Seq {
			tail = append(first, tail...)
		}
	}
	return tail, nil
}
