package checkpoint

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// activePins uses the same command factory as Manager.gitCommand. Only trusted
// local Git plumbing runs. Every method requires a live StoreIO transaction.
// It does not expire pins or infer owner death from PID, age, or process errors.
type activePins struct {
	id          string
	command     func(context.Context, ...string) *exec.Cmd
	lease       *turnLease
	mayHaveRefs bool
}

func newActivePins(command func(context.Context, ...string) *exec.Cmd) (*activePins, error) {
	id, err := newActivePinID()
	if err != nil {
		return nil, err
	}
	return &activePins{id: id, command: command}, nil
}

func newActivePinID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func (p *activePins) root() string { return "refs/supercli/active/" + p.id }

// publish must run inside the same gate as capture, BEFORE capture returns.
// Replacing the before ref is atomic; failure leaves the previous root intact.
// Expansion must merge the old baseline (existing protected-scope contract).
func (p *activePins) publish(ctx context.Context, side, oid string) error {
	if side != "before" && side != "after" {
		return errors.New("invalid active checkpoint side")
	}
	if len(oid) != 40 || oid == strings.Repeat("0", 40) {
		return errors.New("invalid active checkpoint commit")
	}
	if _, err := hex.DecodeString(oid); err != nil {
		return errors.New("invalid active checkpoint commit")
	}
	// A failed command can still have published durable refs. Set this before
	// the attempt so interrupted admission never mistakes them for RAM-only state.
	p.mayHaveRefs = true
	return p.update(ctx, "update "+p.root()+"/"+side+" "+oid+"\n")
}

// Call only after committed metadata+record refs, or a completed no-op after all
// borrowers/Fn have ended. The transaction deletes both refs atomically.
func (p *activePins) release(ctx context.Context) error {
	return p.update(ctx, "delete "+p.root()+"/before\ndelete "+p.root()+"/after\n")
}

func (p *activePins) update(ctx context.Context, commands string) error {
	// Flush ref updates; object and metadata durability have separate guards.
	cmd := p.command(ctx, "-c", "core.fsync=reference", "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader("start\n" + commands + "prepare\ncommit\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkpoint active refs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t *Turn) ensureActivePinsLocked() error {
	if t.active != nil {
		return nil
	}
	id, err := t.ensureCompletionIdentity()
	if err != nil {
		return err
	}
	t.active = &activePins{id: id, command: t.manager.gitCommand}
	return nil
}

// Caller holds Turn.mu during an admitted mutation or drained completion.
// On a retry after a partial
// capture failure, re-pin the original immutable tree; recapturing workspace
// bytes here would lose the first pre-mutation state. No work on the usual path.
func (t *Turn) ensureBeforePinLocked(ctx context.Context) (err error) {
	if t.beforePinned {
		return ctx.Err()
	}
	if t.before == "" || t.active == nil {
		return errBeforePinNotCurrent
	}
	defer func() { t.beforePinned = err == nil }()
	unlock, err := t.manager.lockStore(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	finishUsage, err := t.manager.beginUsageLocked(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	if err := t.manager.prepareRefUpdatesLocked(t.active.root() + "/before"); err != nil {
		return err
	}
	// update-ref refuses a missing object, leaving the tool blocked rather than
	// substituting a new baseline if recovery of the old one is impossible.
	if err := t.active.publish(ctx, "before", t.before); err != nil {
		return err
	}
	return t.manager.accountRefsLocked(1)
}

func (t *Turn) releaseActivePins(ctx context.Context) (err error) {
	if t.active == nil {
		return nil
	}
	if !t.touched && t.active.lease == nil && !t.active.mayHaveRefs {
		// Cancellation before store admission allocated only a local identity.
		// No snapshot/ref/lease exists to release, so do not reacquire a busy gate.
		return nil
	}
	unlock, err := t.manager.lockStore(ctx)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlock())
		if err == nil {
			t.manager.releaseTurn(t)
		}
	}()
	if t.touched {
		finishUsage, usageErr := t.manager.beginUsageLocked(ctx)
		if usageErr != nil {
			return usageErr
		}
		defer func() { err = errors.Join(err, finishUsage(err == nil)) }()
	}
	if err := t.finishSnapshotLatestLocked(ctx); err != nil {
		return err
	}
	// A mutating-capable worker can finish without making its first snapshot.
	// Its owner lease still needs cleanup without initializing an empty Git repo.
	if _, statErr := os.Stat(filepath.Join(t.manager.repo, "HEAD")); statErr == nil {
		if err := t.manager.prepareRefUpdatesLocked(t.active.root()+"/before", t.active.root()+"/after"); err != nil {
			return err
		}
		if err := t.active.release(ctx); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return t.active.lease.releaseLocked()
}

var ErrActiveTurn = errors.New("checkpoint workspace has a pending mutation; wait for its worker or recovery before restoring or clearing checkpoints")

// A live or interrupted turn's original baseline must not disappear during
// Clear/Undo. Active refs survive process exit; age/PID guesses cannot prove
// that a pending workspace edit is safe to discard.
func (m *Manager) requireNoActiveTurnLocked(ctx context.Context) error {
	pending, err := m.pendingTurnsLocked(ctx)
	if err != nil {
		return err
	}
	for _, turn := range pending {
		if turn.Live {
			return ErrActiveTurn
		}
	}
	for _, turn := range pending {
		if turn.hasSnapshots() {
			return fmt.Errorf("%w: owner %s", ErrRecoveryRequired, turn.ID)
		}
	}
	return nil
}
