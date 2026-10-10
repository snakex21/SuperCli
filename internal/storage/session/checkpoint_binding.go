package session

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// CheckpointBinding is an immutable owner, with a one-way resolved flag. It is
// written with the first summary only when a worker outlives its response.
// Sequences alone cannot identify a turn after rewind or database recreation.
type CheckpointBinding struct {
	Key           string `json:"key"`
	UserSeq       int    `json:"user_seq"`
	UserMessageID int64  `json:"user_message_id,string"`
	Resolved      bool   `json:"resolved,omitempty"`
}

func (b CheckpointBinding) encoded(resolved bool) (string, error) {
	if len(b.Key) != 32 || b.UserSeq <= 0 || b.UserMessageID <= 0 {
		return "", errors.New("invalid checkpoint summary owner")
	}
	if _, err := hex.DecodeString(b.Key); err != nil {
		return "", errors.New("invalid checkpoint summary key")
	}
	b.Resolved = resolved
	data, err := json.Marshal(b)
	return string(data), err
}

// Only canonical bindings can participate in CAS. Malformed or legacy data
// remains readable, but is never guessed into a different owner.
func decodeCheckpointBinding(raw string) *CheckpointBinding {
	if raw == "" {
		return nil
	}
	var b CheckpointBinding
	if json.Unmarshal([]byte(raw), &b) != nil {
		return nil
	}
	canonical, err := b.encoded(b.Resolved)
	if err != nil || canonical != raw {
		return nil
	}
	return &b
}

// ResolveCheckpointChanges updates only this pending owner and physical row,
// atomically with checking the original user receipt and assistant message.
// It never inserts a replacement summary or changes its usage counters.
func (s *Store) ResolveCheckpointChanges(ctx context.Context, id string, seq int, rowID int64, binding CheckpointBinding, changes []FileChange) error {
	if s == nil || s.db == nil || id == "" || seq <= binding.UserSeq || rowID <= 0 || binding.Resolved {
		return errors.New("invalid pending checkpoint response")
	}
	pending, err := binding.encoded(false)
	if err != nil {
		return err
	}
	resolved, _ := binding.encoded(true)
	encoded, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE session_turns SET file_changes_json = ?, checkpoint_binding = ?
		WHERE id = ? AND session_id = ? AND assistant_seq = ? AND checkpoint_binding = ?
		AND EXISTS (SELECT 1 FROM messages WHERE session_id = ? AND seq = ? AND id = ? AND role = 'user')
		AND EXISTS (SELECT 1 FROM messages WHERE session_id = ? AND seq = ? AND role = 'assistant')`,
		string(encoded), resolved, rowID, id, seq, pending,
		id, binding.UserSeq, binding.UserMessageID, id, seq)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
