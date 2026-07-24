// Package session persists agent conversations to SQLite and
// reconstructs them on resume. F2.c covers sessions + messages;
// checkpointing lands in F2.d.
package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type HistoryHit struct {
	SessionID string
	Seq       int
	Role      string
	// Snippet is the FTS5 snippet() output: a short excerpt of
	// content with match terms wrapped in <mark>...</mark>.
	Snippet string
	// CreatedAt is the message timestamp in UTC.
	CreatedAt time.Time
}

// SearchHistory runs an FTS5 query against messages.content.
// Filters (all optional, all combine with AND):
//
//	sessionID — limit to a single session
//	role      — one of "system" | "user" | "assistant" | "tool"
//	since, until — created_at range (UTC, half-open: since <= t < until)
//
//	limit — defaults to 20, capped at 100
//
// Results are ordered by FTS5 rank then created_at DESC.
// An empty query returns an error (use ReadMessages for full
// dumps). The caller is responsible for any escaping; the
// FTS5 MATCH operator accepts a query expression like
//
//	"konspekt OR refaktoryzacja"   (boolean)
//	"\"exact phrase\""             (exact)
//	"prefix*"                      (prefix)
//
// but does NOT accept raw LIKE wildcards.
func (s *Store) SearchHistory(ctx context.Context, query, sessionID, role string, since, until time.Time, limit int) ([]HistoryHit, error) {
	if query == "" {
		return nil, fmt.Errorf("session.Store.SearchHistory: query is empty")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	where := []string{"m.id = f.rowid"}
	args := []any{query}
	if sessionID != "" {
		where = append(where, "m.session_id = ?")
		args = append(args, sessionID)
	}
	if role != "" {
		where = append(where, "m.role = ?")
		args = append(args, role)
	}
	if !since.IsZero() {
		where = append(where, "m.created_at >= ?")
		args = append(args, since.UTC().UnixNano())
	}
	if !until.IsZero() {
		where = append(where, "m.created_at <= ?")
		args = append(args, until.UTC().UnixNano())
	}
	q := fmt.Sprintf(`
		SELECT m.session_id, m.seq, m.role,
		       snippet(messages_fts, 0, '<mark>', '</mark>', '...', 16),
		       m.created_at
		FROM messages_fts f, messages m
		WHERE messages_fts MATCH ? AND %s
		ORDER BY rank, m.created_at DESC
		LIMIT ?
	`, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("session.Store.SearchHistory: query: %w", err)
	}
	defer rows.Close()
	out := make([]HistoryHit, 0, limit)
	for rows.Next() {
		var h HistoryHit
		var created int64
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Role, &h.Snippet, &created); err != nil {
			return nil, fmt.Errorf("session.Store.SearchHistory: scan: %w", err)
		}
		h.CreatedAt = time.Unix(0, created).UTC()
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session.Store.SearchHistory: rows: %w", err)
	}
	return out, nil
}

// migrate creates the sessions and messages tables.
