package checkpoint

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools"
)

func TestPendingLeaseBlocksRestoreAndClearBeforeFirstSnapshot(t *testing.T) {
	ctx := context.Background()
	a, b := openParallelStoreManagers(t)
	old := recordParallelStoreFileChange(t, a, "older", "older-session", "older.txt", 1, []byte("old-before"), []byte("old-after"))
	turn, barrier := bindingFixtureTurn(t, a)
	borrow, err := BorrowInvocation(WithTurn(ctx, turn, barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	pending, err := b.PendingTurns(ctx)
	if err != nil || len(pending) != 1 || !pending[0].Live || !pending[0].OwnerKnown || pending[0].hasSnapshots() {
		t.Fatalf("before-first-Fn lease inventory: %+v, %v", pending, err)
	}
	if _, err := b.Undo(ctx, old.ID); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("pending worker allowed older Undo: %v", err)
	}
	if err := b.Clear(); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("pending worker allowed Clear: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "older.txt"), []byte("old-after"))
	// Holding the native owner across a model must not hold the shared I/O gate.
	transaction, err := b.gate.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("lifetime lease retained StoreGate token: %v", err)
	}
	if err := transaction.Close(); err != nil {
		t.Fatal(err)
	}
	borrow.Close()
	if _, err := b.Undo(ctx, old.ID); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("borrow/Fn gap released lease before completion: %v", err)
	}
	if record, err := turn.Complete(ctx); err != nil || record != nil {
		t.Fatalf("no-snapshot completion: %+v, %v", record, err)
	}
	if pending, err := b.PendingTurns(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("completed owner retained lease: %+v, %v", pending, err)
	}
	if _, err := b.Undo(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, filepath.Join(a.home, "older.txt"), []byte("old-before"))
}

func TestPendingLeaseProtectsEmptyStoreAndReadonlyHasNoOwner(t *testing.T) {
	ctx := context.Background()
	m, turn, barrier := bindingFixture(t)
	readonly, err := BorrowInvocation(WithTurn(ctx, turn, barrier), false)
	if err != nil {
		t.Fatal(err)
	}
	readonly.Close()
	if turn.active != nil {
		t.Fatal("readonly invocation allocated an owner")
	}
	if _, err := os.Stat(m.turnLeaseDir()); !os.IsNotExist(err) {
		t.Fatalf("readonly invocation created lease files: %v", err)
	}
	canceled, cancel := context.WithCancel(WithTurn(ctx, turn, barrier))
	cancel()
	if _, err := BorrowInvocation(canceled, true); !errors.Is(err, context.Canceled) || turn.active != nil {
		t.Fatalf("canceled admission created an owner: %v", err)
	}
	borrow, err := BorrowInvocation(WithTurn(ctx, turn, barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	if _, err := os.Stat(filepath.Join(m.repo, "HEAD")); !os.IsNotExist(err) {
		t.Fatalf("pending admission initialized Git: %v", err)
	}
	if err := m.Clear(); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("empty-store Clear bypassed native owner: %v", err)
	}
	borrow.Close()
	if _, err := turn.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Clear(); err != nil {
		t.Fatal(err)
	}
	// GUI/TUI reuse this Manager after Clear; admission precedes Git capture.
	next, nextBarrier := bindingFixtureTurn(t, m)
	nextBorrow, err := BorrowInvocation(WithTurn(ctx, next, nextBarrier), true)
	if err != nil {
		t.Fatalf("cached Manager could not admit a turn after Clear: %v", err)
	}
	nextBorrow.Close()
	if _, err := next.Complete(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDirectWrappedMutationUsesLifetimeLeaseAndFailureRetainsIt(t *testing.T) {
	ctx := context.Background()
	m, turn, _ := bindingFixture(t)
	runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(m.home).Spec()), `{"path":"direct.txt","content":"direct after"}`)
	pending, err := m.PendingTurns(ctx)
	if err != nil || len(pending) != 1 || !pending[0].Live || pending[0].Before == "" || pending[0].After != "" {
		t.Fatalf("direct wrapped Fn owner: %+v, %v", pending, err)
	}
	if err := m.Clear(); !errors.Is(err, ErrActiveTurn) {
		t.Fatalf("direct turn released owner before after capture: %v", err)
	}
	if err := os.WriteFile(m.meta, []byte(`[{"incomplete":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Complete(ctx); err == nil {
		t.Fatal("damaged metadata did not fail completion")
	}
	unlock, err := checkpointStoreLock(turn.active.lease.path)
	if unlock != nil {
		_ = unlock()
	}
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("failed completion discarded native owner: %v", err)
	}
	if err := os.WriteFile(m.meta, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("recovery retry: %+v, %v", record, err)
	}
	if pending, err := m.PendingTurns(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("retry retained owner/refs: %+v, %v", pending, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.home, "direct.txt")); !os.IsNotExist(err) {
		t.Fatal("Undo did not remove the synthetic file")
	}
}

func TestTurnLeaseRejectsDirectoryLink(t *testing.T) {
	m, turn, barrier := bindingFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, m.turnLeaseDir()); err != nil {
		t.Skipf("directory links unavailable: %v", err)
	}
	if _, err := BorrowInvocation(WithTurn(context.Background(), turn, barrier), true); err == nil {
		t.Fatal("lease admission followed a directory link")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("lease admission wrote outside its store: %d entries, %v", len(entries), err)
	}
	if _, immediate := turn.Seal(); !immediate {
		t.Fatal("failed lease admission leaked a borrow")
	}
}

func TestUnknownOwnerRefsRequireRecoveryWithoutChangingSnapshots(t *testing.T) {
	ctx := context.Background()
	a, _, _ := bindingFixture(t)
	// Use an independent workspace for the isolation assertion below.
	otherWorkspace, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := recordParallelStoreFileChange(t, a, "legacy", "legacy-session", "legacy.txt", 1, []byte("legacy before"), []byte("legacy after"))
	pins, err := newActivePins(a.gitCommand)
	if err != nil {
		t.Fatal(err)
	}
	pins.id = strings.Repeat("0", 32) // Sort orphan first to exercise live priority.
	unlock, err := a.lockStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = pins.publish(ctx, "before", record.Before)
	if err == nil {
		err = pins.publish(ctx, "after", record.After)
	}
	if err == nil {
		_, err = a.git(ctx, "update-ref", pins.root()+"/unknown-side", record.Before)
	}
	err = errors.Join(err, unlock())
	if err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, filepath.Join(a.home, "legacy.txt"), "manual legacy edits")
	pending, err := a.PendingTurns(ctx)
	if err != nil || len(pending) != 1 || pending[0].Live || pending[0].OwnerKnown || pending[0].Before != record.Before || pending[0].After != record.After || pending[0].OtherRefs[pins.root()+"/unknown-side"] != record.Before {
		t.Fatalf("unknown owner/ref was lost or inferred from current files: %+v, %v", pending, err)
	}
	if _, err := a.Undo(ctx, record.ID); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("legacy owner bypassed recovery: %v", err)
	}
	if err := a.Clear(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("legacy roots were cleared: %v", err)
	}
	liveTurn, liveBarrier := bindingFixtureTurn(t, a)
	liveBorrow, err := BorrowInvocation(WithTurn(ctx, liveTurn, liveBarrier), true)
	if err != nil {
		t.Fatal(err)
	}
	defer liveBorrow.Close()
	if err := a.Clear(); !errors.Is(err, ErrActiveTurn) || errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("orphan recovery masked a currently live owner: %v", err)
	}
	liveBorrow.Close()
	if _, err := liveTurn.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := otherWorkspace.Clear(); err != nil {
		t.Fatalf("workspace-local recovery blocked another workspace: %v", err)
	}
	assertFileBytes(t, filepath.Join(a.home, "legacy.txt"), []byte("manual legacy edits"))
}

const turnLeaseChildEnv = "SUPERCLI_SYNTHETIC_TURN_LEASE_CHILD"

// Only the parent test supplies these absolute, newly created synthetic dirs.
// os.Exit deliberately skips Complete/Go defers to exercise OS handle release.
func TestTurnLeaseChildProcess(t *testing.T) {
	mode := os.Getenv(turnLeaseChildEnv)
	if mode == "" {
		t.Skip("native lifetime lease child helper")
	}
	home, data := os.Getenv("SUPERCLI_SYNTHETIC_LEASE_HOME"), os.Getenv("SUPERCLI_SYNTHETIC_LEASE_DATA")
	if !filepath.IsAbs(home) || !filepath.IsAbs(data) || (mode != "empty" && mode != "before" && mode != "both") {
		t.Fatal("invalid synthetic child fixture")
	}
	ctx := context.Background()
	m, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("synthetic-child", "interrupted synthetic worker")
	borrow, err := BorrowInvocation(WithTurn(ctx, turn, &turn.barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	before, after := "-", "-"
	if mode != "empty" {
		runSnapshotTool(t, turn.Wrap(tools.NewWriteFile(home).Spec()), `{"path":"source.txt","content":"owned after"}`)
		before = turn.before
		if mode == "both" {
			after, err = m.captureSnapshotFilteredPinned(ctx, []string{"source.txt"}, "", nil, nil, false, turn.active, "after")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	fmt.Printf("ready %s %s %s\n", turn.active.id, before, after)
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	_ = borrow // Intentionally live until process exit, not a stale PID receipt.
	os.Exit(0)
}

func TestProcessExitReleasesOwnerButPreservesUnknownSnapshots(t *testing.T) {
	for _, mode := range []string{"empty", "before", "both"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			home, data := t.TempDir(), t.TempDir()
			m, err := Open(home, data)
			if errors.Is(err, ErrUnavailable) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			old := recordParallelStoreFileChange(t, m, "older", "older-session", "older.txt", 1, []byte("old-before"), []byte("old-after"))
			writeCheckpointFixture(t, filepath.Join(home, "source.txt"), "owned before")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTurnLeaseChildProcess$")
			childproc.HideWindow(cmd)
			cmd.Env = append(os.Environ(), turnLeaseChildEnv+"="+mode, "SUPERCLI_SYNTHETIC_LEASE_HOME="+home, "SUPERCLI_SYNTHETIC_LEASE_DATA="+data)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			var childErrors strings.Builder
			cmd.Stderr = &childErrors
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				_ = stdin.Close()
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			line, err := bufio.NewReader(stdout).ReadString('\n')
			parts := strings.Fields(line)
			if err != nil || len(parts) != 4 || parts[0] != "ready" || !validTurnLeaseID(parts[1]) {
				t.Fatalf("child readiness handshake: %q, %v", line, err)
			}
			pending, err := m.PendingTurns(ctx)
			if err != nil || len(pending) != 1 || !pending[0].Live || !pending[0].OwnerKnown {
				t.Fatalf("cross-process live owner: %+v, %v", pending, err)
			}
			if _, err := m.Undo(ctx, old.ID); !errors.Is(err, ErrActiveTurn) {
				t.Fatalf("restore ignored cross-process owner: %v", err)
			}
			if _, err := stdin.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			waitErr := cmd.Wait() // One completion wait, no process/status polling.
			waited = true
			if waitErr != nil {
				t.Fatalf("child completion: %v: %s", waitErr, childErrors.String())
			}
			writeCheckpointFixture(t, filepath.Join(home, "source.txt"), "manual after exit")
			pending, err = m.PendingTurns(ctx)
			if err != nil || len(pending) != 1 || pending[0].Live || !pending[0].OwnerKnown || pending[0].ID != parts[1] {
				t.Fatalf("dead owner native proof: %+v, %v", pending, err)
			}
			before, after := parts[2], parts[3]
			if before == "-" {
				before = ""
			}
			if after == "-" {
				after = ""
			}
			if pending[0].Before != before || pending[0].After != after {
				t.Fatalf("recovery replaced stored before/after: %+v", pending[0])
			}
			if mode == "empty" {
				if _, err := m.Undo(ctx, old.ID); err != nil {
					t.Fatalf("dead empty lease permanently blocked restore: %v", err)
				}
				if err := m.Clear(); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := m.Undo(ctx, old.ID); !errors.Is(err, ErrRecoveryRequired) {
					t.Fatalf("orphan snapshots did not require explicit recovery: %v", err)
				}
				if err := m.Clear(); !errors.Is(err, ErrRecoveryRequired) {
					t.Fatalf("Clear discarded orphan snapshots: %v", err)
				}
			}
			assertFileBytes(t, filepath.Join(home, "source.txt"), []byte("manual after exit"))
		})
	}
}
