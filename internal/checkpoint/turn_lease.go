package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const turnLeaseDirectory = "leases"

// A Turn owns one unique native lock, independent of the short StoreGate.
// It covers accepted work before the first snapshot, gaps between tool calls,
// and completion/recovery. Process exit releases the handle, never Git roots.
type turnLease struct {
	path  string
	owner *StoreIO
}

func (m *Manager) turnLeaseDir() string {
	return filepath.Join(filepath.Dir(m.repo), turnLeaseDirectory)
}

func checkTurnLeaseDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("checkpoint lease directory must be a directory, not a link or special file")
	}
	return nil
}

// Caller owns StoreIO. No local StoreGate token survives this function.
func (m *Manager) newTurnLeaseLocked(ctx context.Context, id string) (*turnLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validTurnLeaseID(id) {
		return nil, errors.New("invalid checkpoint lease owner id")
	}
	// Clear removes the workspace store directory. The same cached Manager
	// must be able to admit a later turn before its first Git capture.
	root := filepath.Dir(m.repo)
	if err := checkTurnLeaseDir(root); os.IsNotExist(err) {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := checkTurnLeaseDir(root); err != nil {
		return nil, err
	}
	dir := m.turnLeaseDir()
	if err := checkTurnLeaseDir(dir); os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := checkTurnLeaseDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".lock")
	// Refuse a collision rather than sharing an old owner's inode. Creation and
	// acquisition are serialized with restore/Clear by the short store gate.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("checkpoint lease create: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	unlock, err := checkpointStoreLock(path)
	if err != nil {
		// Never unlink an inode that a native busy result says somebody holds.
		// An unlocked empty receipt is harmless and may be removed by Clear.
		return nil, err
	}
	lease := &turnLease{path: path, owner: &StoreIO{unlock: unlock}}
	if err := ctx.Err(); err != nil {
		// No invocation/Fn has been admitted and no snapshot exists yet. A
		// successful cleanup can safely discard this empty reservation now.
		cleanupErr := lease.releaseLocked()
		if cleanupErr == nil {
			return nil, err
		}
		return lease, errors.Join(err, cleanupErr)
	}
	return lease, nil
}

func (t *Turn) ensureLifetimeLease(ctx context.Context) error {
	if err := t.mu.LockContext(ctx); err != nil {
		return err
	}
	defer t.mu.Unlock()
	if t.manager == nil || !filepath.IsAbs(t.manager.repo) {
		return errors.New("checkpoint lifetime lease requires an initialized portable store")
	}
	if t.active != nil && t.active.lease != nil {
		return ctx.Err()
	}
	if err := t.ensureActivePinsLocked(); err != nil {
		return err
	}
	unlock, err := t.manager.lockStore(ctx)
	if err != nil {
		return err
	}
	lease, acquireErr := t.manager.newTurnLeaseLocked(ctx, t.active.id)
	if lease != nil {
		t.active.lease = lease
	}
	unlockErr := unlock()
	if lease != nil {
		// This hook runs outside StoreIO, under Turn.mu. It must not acquire
		// Turn.mu or invoke Complete; retaining is idempotent per Turn.
		t.manager.retainTurn(t)
	}
	return errors.Join(acquireErr, unlockErr)
}

// Caller owns StoreIO; no other cooperating process can probe/create a lease
// between close and unlink. IDs are unique and never reused. A failed unlink is
// retryable with the same (already closed) handle; no snapshot is recaptured.
func (l *turnLease) releaseLocked() error {
	if l == nil {
		return nil
	}
	if err := l.owner.Close(); err != nil {
		return err
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
