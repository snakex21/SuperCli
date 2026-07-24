package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SaveMessageAttachments durably associates file paths with the user message
// that sent them. This is display metadata and is not injected into later
// model turns.
func (s *Store) SaveMessageAttachments(ctx context.Context, sessionID string, userSeq int, paths []string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.SaveMessageAttachments: nil store")
	}
	if strings.TrimSpace(sessionID) == "" || userSeq <= 0 {
		return fmt.Errorf("session.Store.SaveMessageAttachments: session id and positive sequence are required")
	}
	clean := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		clean = append(clean, path)
	}
	if len(clean) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM message_attachments WHERE session_id = ? AND user_seq = ?`, sessionID, userSeq)
		return err
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return fmt.Errorf("session.Store.SaveMessageAttachments marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO message_attachments (session_id, user_seq, paths_json, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, user_seq) DO UPDATE SET
			paths_json = excluded.paths_json, created_at = excluded.created_at`,
		sessionID, userSeq, string(raw), time.Now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("session.Store.SaveMessageAttachments: %w", err)
	}
	return nil
}

// ReadMessageAttachmentsRange returns attachment paths keyed by user-message
// sequence. Non-positive bounds are left open.
func (s *Store) ReadMessageAttachmentsRange(ctx context.Context, sessionID string, fromSeq, toSeq int) (map[int][]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session.Store.ReadMessageAttachmentsRange: nil store")
	}
	query := `SELECT user_seq, paths_json FROM message_attachments WHERE session_id = ?`
	args := []any{sessionID}
	if fromSeq > 0 {
		query += ` AND user_seq >= ?`
		args = append(args, fromSeq)
	}
	if toSeq > 0 {
		query += ` AND user_seq <= ?`
		args = append(args, toSeq)
	}
	query += ` ORDER BY user_seq`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int][]string)
	for rows.Next() {
		var seq int
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			return nil, err
		}
		var paths []string
		if json.Unmarshal([]byte(raw), &paths) == nil && len(paths) > 0 {
			out[seq] = paths
		}
	}
	return out, rows.Err()
}
