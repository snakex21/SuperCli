package checkpoint

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Only the current short StoreIO transaction owns this receipt. It must never
// survive release of the gate or retain an agent, request, or workspace history.
type checkpointUsageTransaction struct {
	receipt *StoreUsageWrite
	growth  StoreUsageGrowth
}

func (m *Manager) usageCounterLocked() (*StoreUsageCounter, error) {
	if m.usageCounter != nil {
		return m.usageCounter, nil
	}
	counter, err := NewStoreUsageCounter(m.gate)
	if err == nil {
		m.usageCounter = counter
	}
	return counter, err
}

// Nested helpers accumulate in their caller's receipt. Only its owner can
// publish successful accounting; a failed write leaves the durable dirty bit.
func (m *Manager) beginUsageLocked(ctx context.Context) (func(bool) error, error) {
	if m.usageTransaction != nil {
		return func(bool) error { return nil }, nil
	}
	counter, err := m.usageCounterLocked()
	if err != nil {
		return nil, err
	}
	receipt, err := counter.BeginLocked(ctx)
	if err != nil {
		return nil, err
	}
	// Reftable updates and deletions append stack data instead of writing
	// 41-byte loose refs. Existing non-files stores require a fresh census;
	// new stores explicitly select the files backend at git init.
	if _, probeErr := os.Lstat(filepath.Join(m.repo, "reftable")); !os.IsNotExist(probeErr) {
		if probeErr != nil {
			return nil, probeErr // The durable dirty receipt remains.
		}
		if err := receipt.RequireCensus(); err != nil {
			return nil, err
		}
	}
	tx := &checkpointUsageTransaction{receipt: receipt}
	m.usageTransaction = tx
	return func(success bool) error {
		m.usageTransaction = nil
		if !success {
			return nil
		}
		return receipt.FinishLocked(ctx, tx.growth)
	}, nil
}

func (m *Manager) accountObjectLocked(bytes int64) error {
	if m.usageTransaction == nil {
		return nil // Standalone object-encoder fixtures have no store receipt.
	}
	return m.usageTransaction.receipt.AddPublishedBlobBytes(bytes)
}

func (m *Manager) accountRefsLocked(writes int64) error {
	if m.usageTransaction == nil {
		return nil
	}
	bound, err := StoreUsageRefBound(writes)
	if err != nil {
		return err
	}
	next, err := retentionAdd(m.usageTransaction.growth.RefBytes, bound)
	if err == nil {
		m.usageTransaction.growth.RefBytes = next
	}
	return err
}

// Disabling new reflogs does not disable appends to a legacy existing log.
// Check only the exact touched refs and HEAD alias, without walking the repo.
func (m *Manager) prepareRefUpdatesLocked(refs ...string) error {
	if m.usageTransaction == nil {
		return nil
	}
	refs = append(refs, "HEAD")
	hasLog := false
	for _, ref := range refs {
		path := filepath.Join(m.repo, "logs", filepath.FromSlash(ref))
		if err := retentionSafePath(filepath.Dir(m.gate.path), path); err != nil {
			return err
		}
		_, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		hasLog = true
	}
	if hasLog {
		return m.requireUsageCensusLocked()
	}
	return nil
}

func (m *Manager) accountMetadataLocked(bytes int64) error {
	if m.usageTransaction == nil {
		return nil
	}
	next, err := retentionAdd(m.usageTransaction.growth.MetadataBytes, bytes)
	if err == nil {
		m.usageTransaction.growth.MetadataBytes = next
	}
	return err
}

// Infrequent standalone Undo/Redo/Forget metadata writes invalidate the cheap
// total before publication. They need no graph census or post-write accounting
// failure that could masquerade as a failed durable metadata rename.
func (m *Manager) dirtyStandaloneUsageLocked(ctx context.Context) error {
	if m.usageTransaction != nil {
		return nil
	}
	counter, err := m.usageCounterLocked()
	if err != nil {
		return err
	}
	_, err = counter.BeginLocked(ctx)
	return err
}

func (m *Manager) requireUsageCensusLocked() error {
	if m.usageTransaction == nil {
		return nil
	}
	return m.usageTransaction.receipt.RequireCensus()
}

func (m *Manager) accountSnapshotTreeShapeLocked(shape StoreUsageTreeShape) error {
	if m.usageTransaction == nil {
		return nil
	}
	// commitTreeLocked uses exactly these identities, date, message and a SHA1
	// tree. This accounts native Git trees without reading their object files.
	const commit = "tree 0000000000000000000000000000000000000000\nauthor SuperCli <checkpoint@local> 0 +0000\ncommitter SuperCli <checkpoint@local> 0 +0000\n\nSuperCli checkpoint\n"
	bound, err := shape.Bound(int64(len(commit)))
	if err != nil {
		return err
	}
	next, err := retentionAdd(m.usageTransaction.growth.TreeCommitBytes, bound)
	if err == nil {
		m.usageTransaction.growth.TreeCommitBytes = next
	}
	return err
}

// Count output already passing through zlib; no object reread, directory scan,
// Stat per blob, retained buffer, or second compression is required.
type checkpointCountingWriter struct {
	writer io.Writer
	bytes  int64
}

func (w *checkpointCountingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}

// Invoked after the borrow barrier and successful lease/ref cleanup. A clean
// completion reads a bounded tiny file. Collection has its own dirty receipt
// and shares this gate; it never reenters Manager accounting hooks.
func (m *Manager) completeRetainedUsage(ctx context.Context, limit int64) (err error) {
	unlock, err := m.lockStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	counter, err := m.usageCounterLocked()
	if err != nil {
		return err
	}
	dataDir := filepath.Dir(m.gate.path)
	completion, err := counter.CompleteManagedLocked(ctx, limit, func(ctx context.Context) (StoreBudgetResult, error) {
		return EnforceManagedStoreBudgetLocked(ctx, dataDir, limit)
	})
	// A collector can have durably expired records even if a subsequent
	// cleanup step failed. Never retain the pre-collection metadata in memory.
	if completion.Collected {
		return errors.Join(err, m.reloadRecordsLocked())
	}
	return err
}
