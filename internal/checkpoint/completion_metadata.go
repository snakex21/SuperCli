package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

type CompletionIdentity struct {
	Key           string
	SessionID     string
	UserSeq       int
	UserMessageID int64
}

// withCompletionStore does one nonblocking admission. It does not run Git,
// retention, polling or pending-worker completion while opening a transcript.
// The callback must not reenter checkpoint APIs.
func (m *Manager) withCompletionStore(ctx context.Context, fn func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.mu.TryLock() {
		return ErrStoreBusy
	}
	defer m.mu.Unlock()
	lease, err := m.gate.TryAcquire(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return fn()
}

// WithUserReceipt serializes a late telemetry update with rewind/delete, using
// the original immutable prompt identity. SQL must repeat its own atomic CAS.
func (m *Manager) WithUserReceipt(ctx context.Context, id CompletionIdentity, apply func() error) error {
	if id.Key == "" || apply == nil {
		return ErrUserMessageChanged
	}
	return m.withCompletionStore(ctx, func() error {
		if err := m.requireUserReceiptLocked(ctx, id.SessionID, id.UserSeq, id.UserMessageID); err != nil {
			return err
		}
		return apply()
	})
}

// WithCompletionRecords reads fresh, bounded metadata once for unresolved
// owners already present in a transcript page. Missing/expired/legacy records
// cannot be reconstructed. Duplicate keys are refused rather than selecting
// the latest record. The store gate spans receipt validation and the SQL CAS.
func (m *Manager) WithCompletionRecords(ctx context.Context, owners []CompletionIdentity, apply func(CompletionIdentity, Record) error) error {
	if len(owners) == 0 {
		return nil
	}
	if apply == nil {
		return errors.New("checkpoint completion callback is required")
	}
	return m.withCompletionStore(ctx, func() error {
		journal := filepath.Join(filepath.Dir(m.gate.path), filepath.FromSlash(retentionJournalName))
		if _, err := os.Lstat(journal); err == nil {
			return ErrStoreBusy // Never use metadata from a prepared retention transaction.
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := m.reloadRecordsLocked(); err != nil {
			return err
		}
		wanted := make(map[string]int, len(owners))
		for _, owner := range owners {
			if owner.Key != "" && owner.SessionID != "" && owner.UserSeq > 0 && owner.UserMessageID > 0 {
				wanted[owner.Key] = -1
			}
		}
		for i := range m.records {
			r := &m.records[i]
			if index, ok := wanted[r.CompletionKey]; ok {
				if index != -1 {
					wanted[r.CompletionKey] = -2
				} else {
					wanted[r.CompletionKey] = i
				}
			}
		}
		for _, owner := range owners {
			index, ok := wanted[owner.Key]
			if !ok || index < 0 {
				continue
			}
			r := m.records[index]
			if r.SessionID != owner.SessionID || r.UserSeq != owner.UserSeq || r.UserMessageID != owner.UserMessageID {
				continue
			}
			if err := m.requireUserReceiptLocked(ctx, owner.SessionID, owner.UserSeq, owner.UserMessageID); err != nil {
				if errors.Is(err, ErrUserMessageChanged) {
					continue
				}
				return err
			}
			if err := apply(owner, r); err != nil {
				return err
			}
		}
		return ctx.Err()
	})
}
