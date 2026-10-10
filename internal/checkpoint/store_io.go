package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Every private repository and metadata mutation participates in one portable
// store transaction. Different GUI engines, TUI instances and processes cannot
// replace each other's record lists or use a private Git index concurrently.
// The gate is held for filesystem work only, never while a model is running.
func (m *Manager) lockStore(ctx context.Context) (func() error, error) {
	if err := m.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	// Open always initializes the gate; the lazy path supports internal legacy
	// migration fixtures without changing the transaction contract.
	if m.gate == nil {
		gate, err := NewStoreGate(filepath.Dir(filepath.Dir(filepath.Dir(m.repo))))
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		m.gate = gate
	}
	transaction, err := m.gate.Acquire(ctx)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// Recover a prepared metadata/ref transaction before another writer can
	// append or rewind its input. The absent-journal path is one small lookup.
	if err := resumeRetentionJournalLocked(ctx, filepath.Dir(m.gate.path)); err != nil {
		// Recovery may already have published metadata before ref cleanup failed.
		// Refresh that durable state while the gate is still held.
		reloadErr := m.reloadRecordsLocked()
		m.mu.Unlock()
		return nil, errors.Join(err, reloadErr, transaction.Close())
	}
	if err := m.reloadRecordsLocked(); err != nil {
		m.mu.Unlock()
		return nil, errors.Join(err, transaction.Close())
	}
	return func() error {
		m.mu.Unlock()
		return transaction.Close()
	}, nil
}

func (m *Manager) reloadRecordsLocked() error {
	data, err := readCheckpointMetadata(m.meta)
	if os.IsNotExist(err) {
		m.records = nil
		m.repoReady = false
		return nil
	}
	if err != nil {
		return fmt.Errorf("read checkpoint records: %w", err)
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("read checkpoint records: %w", err)
	}
	// Publish only a complete read. A stale manager cannot silently replace
	// malformed metadata, checkpoints added by another process, or Undone flags.
	m.records = records
	m.filterApplicationDataRecords()
	return nil
}
