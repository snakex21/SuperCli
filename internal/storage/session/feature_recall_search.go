package session

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// HistoryWorkspaces returns recorded workspace names without reading transcripts.
// Callers can resolve case and filesystem aliases before selecting project history.
func (s *Store) HistoryWorkspaces(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT cwd FROM sessions WHERE cwd<>'' ORDER BY cwd`)
	if err != nil {
		return nil, fmt.Errorf("session.Store.HistoryWorkspaces: %w", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var cwd string
		if err := rows.Scan(&cwd); err != nil {
			return nil, err
		}
		result = append(result, cwd)
	}
	return result, rows.Err()
}

// SearchSessionMatches returns the best matching message from each of up to
// limit different conversations in the allowed workspaces. Filtering and
// grouping precede the limit so foreign/current conversations or many hits
// from one long conversation cannot hide other useful sessions. An empty
// workspace allowlist returns no results, never an unrestricted search.
func (s *Store) SearchSessionMatches(ctx context.Context, query string, workspaces []string, excludeSession string, limit int) ([]HistoryHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("session.Store.SearchSessionMatches: query is empty")
	}
	if len(workspaces) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	placeholders := make([]string, len(workspaces))
	args := []any{query}
	for i, cwd := range workspaces {
		placeholders[i] = "?"
		args = append(args, cwd)
	}
	args = append(args, excludeSession, limit, query)
	// Materialize only compact rank metadata; snippet() is evaluated only for the
	// selected messages in the outer FTS context, not for every matching row.
	statement := `WITH matched AS MATERIALIZED (
  SELECT m.id,m.session_id,m.created_at,f.rank AS score
  FROM messages_fts f JOIN messages m ON m.id=f.rowid
  JOIN sessions s ON s.id=m.session_id
  WHERE messages_fts MATCH ? AND s.cwd IN (` + strings.Join(placeholders, ",") + `) AND m.session_id<>?
 ), ranked AS (
  SELECT *,row_number() OVER (PARTITION BY session_id ORDER BY score,created_at DESC,id DESC) AS position
  FROM matched
 ), chosen AS (
  SELECT id,score FROM ranked WHERE position=1 ORDER BY score,created_at DESC,id DESC LIMIT ?
 )
 SELECT m.session_id,m.seq,m.role,snippet(messages_fts,0,'<mark>','</mark>','...',16),m.created_at
 FROM chosen c JOIN messages m ON m.id=c.id JOIN messages_fts f ON f.rowid=m.id
 WHERE messages_fts MATCH ? ORDER BY c.score,m.created_at DESC,m.id DESC`
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("session.Store.SearchSessionMatches: %w", err)
	}
	defer rows.Close()
	result := make([]HistoryHit, 0, limit)
	for rows.Next() {
		var hit HistoryHit
		var created int64
		if err := rows.Scan(&hit.SessionID, &hit.Seq, &hit.Role, &hit.Snippet, &created); err != nil {
			return nil, err
		}
		hit.CreatedAt = time.Unix(0, created).UTC()
		result = append(result, hit)
	}
	return result, rows.Err()
}
