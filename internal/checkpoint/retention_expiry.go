package checkpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrHistoryExpired = errors.New("checkpoint history has expired; this conversation point cannot be fully restored")

const retentionExpiryName = ".retention-expired.json"

type retentionExpirySession struct {
	ThroughSeq int  `json:"through_seq"`
	UnknownSeq bool `json:"unknown_sequence,omitempty"`
}

type retentionExpiryDocument struct {
	fields   map[string]json.RawMessage
	sessions map[string]map[string]json.RawMessage
}

// Keep unknown fields and integer values as raw JSON. This file is a permanent
// conservative rewind boundary, not a list inferred from the remaining records.
func retentionDecodeExpiry(data []byte) (retentionExpiryDocument, error) {
	d := retentionExpiryDocument{fields: map[string]json.RawMessage{}, sessions: map[string]map[string]json.RawMessage{}}
	if len(data) == 0 {
		d.fields["version"] = json.RawMessage("1")
		return d, nil
	}
	if len(data) > retentionMaxMetadata || json.Unmarshal(data, &d.fields) != nil || d.fields == nil {
		return d, ErrStoreInventory
	}
	var version int
	if json.Unmarshal(d.fields["version"], &version) != nil || version != 1 || json.Unmarshal(d.fields["sessions"], &d.sessions) != nil || d.sessions == nil {
		return d, ErrStoreInventory
	}
	for id, raw := range d.sessions {
		if strings.TrimSpace(id) == "" {
			return d, ErrStoreInventory
		}
		if _, err := retentionDecodeExpirySession(raw); err != nil {
			return d, err
		}
	}
	return d, nil
}

func retentionDecodeExpirySession(raw map[string]json.RawMessage) (retentionExpirySession, error) {
	var session retentionExpirySession
	if raw == nil || len(raw["through_seq"]) == 0 || bytes.Equal(bytes.TrimSpace(raw["through_seq"]), []byte("null")) || json.Unmarshal(raw["through_seq"], &session.ThroughSeq) != nil || session.ThroughSeq < 0 {
		return session, ErrStoreInventory
	}
	if value, exists := raw["unknown_sequence"]; exists {
		value = bytes.TrimSpace(value)
		if (!bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false"))) || json.Unmarshal(value, &session.UnknownSeq) != nil {
			return session, ErrStoreInventory
		}
	}
	return session, nil
}

func retentionAdvanceExpiry(data []byte, expired []Record) ([]byte, error) {
	d, err := retentionDecodeExpiry(data)
	if err != nil {
		return nil, err
	}
	for _, record := range expired {
		if strings.TrimSpace(record.SessionID) == "" || record.UserSeq < 0 {
			return nil, ErrStoreInventory // An unidentifiable boundary cannot be guessed.
		}
		raw := d.sessions[record.SessionID]
		if raw == nil {
			raw = map[string]json.RawMessage{"through_seq": json.RawMessage("0")}
			d.sessions[record.SessionID] = raw
		}
		session, err := retentionDecodeExpirySession(raw)
		if err != nil {
			return nil, err
		}
		if record.UserSeq == 0 {
			session.UnknownSeq = true
		}
		if record.UserSeq > session.ThroughSeq {
			session.ThroughSeq = record.UserSeq
		}
		raw["through_seq"], _ = json.Marshal(session.ThroughSeq)
		if session.UnknownSeq {
			raw["unknown_sequence"] = json.RawMessage("true")
		}
	}
	d.fields["sessions"], err = json.Marshal(d.sessions)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(d.fields)
	if err != nil || len(result) > retentionMaxMetadata {
		return nil, ErrStoreInventory
	}
	return result, nil
}

func retentionReadExpiry(root, path string) ([]byte, string, error) {
	if err := retentionSafePath(root, path); err != nil {
		return nil, "", err
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, "", nil
	}
	data, err := retentionRead(path, retentionMaxMetadata)
	if err != nil {
		return nil, "", err
	}
	if _, err := retentionDecodeExpiry(data); err != nil {
		return nil, "", err
	}
	return data, retentionSHA(data), nil
}

// Caller owns the StoreGate (normally inside lockStore), and calls this before
// any restore, transcript rewind or branch operation. Checking only retained
// Record.UserSeq values would silently restore an incomplete suffix after GC.
// Legacy expired records with no sequence conservatively block From operations
// for their session; exact retained checkpoint IDs still support Undo/Redo.
func (m *Manager) requireRetentionHistoryFromLocked(sessionID string, fromSeq int) error {
	if strings.TrimSpace(sessionID) == "" || fromSeq <= 0 {
		return errors.New("session id and positive user sequence are required")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(m.meta)))
	data, _, err := retentionReadExpiry(root, filepath.Join(filepath.Dir(m.meta), retentionExpiryName))
	if err != nil {
		return err
	}
	d, err := retentionDecodeExpiry(data)
	if err != nil {
		return err
	}
	raw := d.sessions[sessionID]
	if raw == nil {
		return nil
	}
	session, err := retentionDecodeExpirySession(raw)
	if err != nil {
		return err
	}
	if session.UnknownSeq || fromSeq <= session.ThroughSeq {
		return fmt.Errorf("%w (session boundary %d)", ErrHistoryExpired, session.ThroughSeq)
	}
	return nil
}

// Called under the StoreGate only after transcript truncation is durably
// committed. Removed messages can reuse their old SQL sequence numbers, so the
// frontier must no longer claim that their old checkpoints belong to new turns.
// This is a conservative maximum, never a fabricated exact map of expired IDs.
func (m *Manager) forgetRetentionHistoryFromLocked(sessionID string, cutoff int) error {
	if strings.TrimSpace(sessionID) == "" || cutoff <= 0 {
		return errors.New("session id and positive user sequence are required")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(m.meta)))
	path := filepath.Join(filepath.Dir(m.meta), retentionExpiryName)
	data, _, err := retentionReadExpiry(root, path)
	if err != nil {
		return err
	}
	d, err := retentionDecodeExpiry(data)
	if err != nil {
		return err
	}
	raw := d.sessions[sessionID]
	if raw == nil {
		return nil
	}
	session, err := retentionDecodeExpirySession(raw)
	if err != nil {
		return err
	}
	if cutoff == 1 {
		delete(d.sessions, sessionID) // Includes unknown legacy sequence state.
	} else if session.ThroughSeq >= cutoff {
		raw["through_seq"], _ = json.Marshal(cutoff - 1)
	} else {
		return nil
	}
	d.fields["sessions"], err = json.Marshal(d.sessions)
	if err != nil {
		return err
	}
	updated, err := json.Marshal(d.fields)
	if err != nil || len(updated) > retentionMaxMetadata {
		return ErrStoreInventory
	}
	return retentionAtomicWrite(root, path, updated)
}
