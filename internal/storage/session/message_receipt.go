package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// MessageReceipt identifies one committed message in this Store's database.
// Seq is its position in a session and can be reused after a rewind. ID is the
// existing AUTOINCREMENT messages.id and must never be inferred from a later
// lookup by session and sequence. A receipt does not contain message payloads.
type MessageReceipt struct {
	Seq int
	ID  int64
}

// ReadUserReceiptAt reads identity only. It does not upgrade an older
// checkpoint's sequence into an identity; callers must retain the original ID.
func (s *Store) ReadUserReceiptAt(ctx context.Context, sessionID string, seq int) (MessageReceipt, error) {
	if s == nil || s.db == nil || strings.TrimSpace(sessionID) == "" || seq <= 0 {
		return MessageReceipt{}, fmt.Errorf("session.Store.ReadUserReceiptAt: store, session id and positive sequence are required")
	}
	var receipt MessageReceipt
	if err := s.db.QueryRowContext(ctx, `SELECT seq, id FROM messages WHERE session_id = ? AND seq = ? AND role = ?`, sessionID, seq, string(llm.RoleUser)).Scan(&receipt.Seq, &receipt.ID); err != nil {
		return MessageReceipt{}, err
	}
	return receipt, nil
}

// TruncateFromExact checks and removes the selected physical user row in one
// SQL transaction. Production callers hold the checkpoint StoreGate too.
// Missing, replaced and non-user receipts return sql.ErrNoRows without writes.
func (s *Store) TruncateFromExact(ctx context.Context, sessionID string, receipt MessageReceipt) (int, error) {
	if receipt.ID <= 0 {
		return 0, sql.ErrNoRows
	}
	return s.truncateFrom(ctx, sessionID, receipt.Seq, &receipt)
}

// IsCurrentUserReceipt checks the exact physical user message, including its
// session and sequence, without reading message content. A missing, replaced,
// foreign-session or non-user message returns false, nil. Invalid arguments
// and SQL errors remain errors; callers must not interpret them as deletion.
//
// This is a point-in-time check, not a lock. A caller coordinating checkpoint
// commit or rewind must hold the shared store gate through this check and its
// operation; all transcript deletions must participate in that same gate.
func (s *Store) IsCurrentUserReceipt(ctx context.Context, sessionID string, receipt MessageReceipt) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("session.Store.IsCurrentUserReceipt: nil store")
	}
	if strings.TrimSpace(sessionID) == "" || receipt.Seq <= 0 || receipt.ID <= 0 {
		return false, fmt.Errorf("session.Store.IsCurrentUserReceipt: session id and positive receipt sequence and id are required")
	}
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM messages WHERE id = ? AND session_id = ? AND seq = ? AND role = ?)`,
		receipt.ID, sessionID, receipt.Seq, string(llm.RoleUser),
	).Scan(&exists)
	return exists, err
}
