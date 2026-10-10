package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
)

// UserReceiptValidator checks an immutable persisted user-message row. A
// sequence alone is not an identity: rewinds can reuse it while a worker ends.
type UserReceiptValidator func(context.Context, string, int, int64) (bool, error)

var ErrUserMessageChanged = errors.New("checkpoint user message no longer belongs to this conversation tail")

func (m *Manager) SetUserReceiptValidator(validate UserReceiptValidator) {
	m.mu.Lock()
	m.userReceiptValidator = validate
	m.mu.Unlock()
}

func (m *Manager) requireUserReceiptLocked(ctx context.Context, sessionID string, seq int, id int64) error {
	if seq <= 0 || id <= 0 || m.userReceiptValidator == nil {
		return ErrUserMessageChanged
	}
	current, err := m.userReceiptValidator(ctx, sessionID, seq, id)
	if err != nil {
		return err
	}
	if !current {
		return ErrUserMessageChanged
	}
	return nil
}

// Validation is metadata-only. Legacy records cannot be guessed or backfilled
// from a reused sequence. Explicit Undo(record.ID) remains available.
func (m *Manager) validatedPreviewFromLocked(ctx context.Context, sessionID string, seq int) (BatchResult, error) {
	preview := m.previewFromLocked(sessionID, seq)
	if m.userReceiptValidator == nil {
		return preview, nil
	}
	result := BatchResult{}
	for _, record := range preview.Records {
		if record.UserMessageID <= 0 {
			continue
		}
		current, err := m.userReceiptValidator(ctx, record.SessionID, record.UserSeq, record.UserMessageID)
		if err != nil {
			return BatchResult{}, err
		}
		if current {
			result.Records = append(result.Records, record)
		}
	}
	result.Files = batchFiles(result.Records)
	return result, nil
}

func (m *Manager) PreviewFromContext(ctx context.Context, sessionID string, seq int) (out BatchResult, err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err = m.requireRetentionHistoryFromLocked(sessionID, seq); err != nil {
		return BatchResult{}, err
	}
	return m.validatedPreviewFromLocked(ctx, sessionID, seq)
}

// RewindTranscript holds the same short store gate across identity validation,
// optional file restore and the exact SQL truncation. The callback must only
// truncate the selected immutable message and must not reenter checkpoint APIs.
func (m *Manager) RewindTranscript(ctx context.Context, sessionID string, seq int, id int64, rewindFiles bool, truncate func(context.Context) error) (out BatchResult, err error) {
	if truncate == nil {
		return BatchResult{}, errors.New("transcript truncate callback is required")
	}
	if err := m.RetryRecordedCompletions(ctx); err != nil {
		return BatchResult{}, err
	}
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	truncated := false
	defer func() {
		err = errors.Join(err, unlock())
		// No Turn.mu under Manager.mu / store gate. Immutable row validation
		// protects an independent manager even before its owner is detached.
		if truncated {
			m.detachPendingUserSequences(sessionID, seq)
		}
	}()
	if err := m.requireUserReceiptLocked(ctx, sessionID, seq, id); err != nil {
		return BatchResult{}, err
	}
	if rewindFiles {
		if err := m.requireNoActiveTurnLocked(ctx); err != nil {
			return BatchResult{}, err
		}
		if err := m.requireRetentionHistoryFromLocked(sessionID, seq); err != nil {
			return BatchResult{}, err
		}
		preview, previewErr := m.validatedPreviewFromLocked(ctx, sessionID, seq)
		if previewErr != nil {
			return BatchResult{}, previewErr
		}
		out, err = m.undoPreviewLocked(ctx, preview)
		if err != nil {
			return out, err
		}
	}
	if err := truncate(ctx); err != nil {
		_, rollbackErr := m.redoBatchLocked(context.WithoutCancel(ctx), out)
		return out, errors.Join(err, rollbackErr)
	}
	truncated = true
	// SQL is already committed. A cleanup failure must not redo files or
	// pretend that the removed messages still exist.
	return out, m.forgetFromLocked(context.WithoutCancel(ctx), sessionID, seq)
}

func (m *Manager) undoPreviewLocked(ctx context.Context, preview BatchResult) (BatchResult, error) {
	applied := BatchResult{Files: preview.Files}
	for _, record := range preview.Records {
		result, err := m.restoreLockedWithRename(ctx, record.ID, false, os.Rename)
		if err != nil {
			applied.Conflicts = append(applied.Conflicts, result.Conflicts...)
			_, rollbackErr := m.redoBatchLocked(context.WithoutCancel(ctx), applied)
			if rollbackErr != nil {
				return applied, fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
			}
			return applied, err
		}
		applied.Records = append(applied.Records, result.Record)
	}
	return applied, nil
}

func batchFiles(records []Record) []string {
	files := make(map[string]struct{})
	for _, record := range records {
		for _, path := range record.Files {
			files[path] = struct{}{}
		}
	}
	result := make([]string, 0, len(files))
	for path := range files {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (m *Manager) forgetFromLocked(ctx context.Context, sessionID string, userSeq int) error {
	if err := m.dirtyStandaloneUsageLocked(ctx); err != nil {
		return err
	}
	if err := m.forgetRetentionHistoryFromLocked(sessionID, userSeq); err != nil {
		return err
	}
	kept := make([]Record, 0, len(m.records))
	forgotten := make([]Record, 0)
	changed := false
	for _, record := range m.records {
		if record.SessionID == sessionID && record.UserSeq >= userSeq && record.UserSeq > 0 {
			changed = true
			forgotten = append(forgotten, record)
			continue
		}
		kept = append(kept, record)
	}
	if !changed {
		return nil
	}
	previous := m.records
	m.records = kept
	if err := m.saveLocked(); err != nil {
		m.records = previous
		return err
	}
	return m.unpinRecordsLocked(ctx, forgotten)
}

func (c *Controller) SetUserReceiptValidator(validate UserReceiptValidator) {
	if c != nil && c.manager != nil {
		c.manager.SetUserReceiptValidator(validate)
	}
}

func (t *Turn) SetUserMessageReceipt(seq int, id int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= 0 || id <= 0 || t.detachedUserSeq || t.userMessageID != 0 {
		return
	}
	t.userSeq, t.userMessageID = seq, id
}

func (c *Controller) SetUserMessageReceipt(seq int, id int64) {
	if c == nil {
		return
	}
	if turn := c.currentTurn(); turn != nil {
		turn.SetUserMessageReceipt(seq, id)
	}
}
