//go:build windows

package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

const consoleProbePhaseCap = 5 * time.Second

func consoleProbeCause(err error) string {
	switch {
	case err == nil:
		return "complete"
	case errors.Is(err, ErrSnapshotLimit):
		return "checkpoint_snapshot_limit"
	case errors.Is(err, ErrStoreBusy):
		return "checkpoint_store_busy"
	case errors.Is(err, context.DeadlineExceeded):
		return "diagnostic_deadline"
	case errors.Is(err, context.Canceled):
		return "diagnostic_cancelled"
	default:
		return "checkpoint_or_native_error"
	}
}

// The cap is diagnostic, not a production command runtime limit. Await one
// completion event; a missed cancel reports goroutine stacks without polling.
func consoleProbePhase(t *testing.T, name string, run func(context.Context) error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), consoleProbePhaseCap)
	defer cancel()
	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	select {
	case err := <-done:
		err = errors.Join(err, ctx.Err())
		t.Logf("console_probe phase=%s duration_ms=%d cap_ms=%d outcome=%s cancelled=%t", name, time.Since(started).Milliseconds(), consoleProbePhaseCap.Milliseconds(), consoleProbeCause(err), ctx.Err() != nil)
		if err != nil {
			t.Logf("console_probe phase=%s error=%.1500s", name, err.Error())
		}
		return err
	case <-time.After(consoleProbePhaseCap + 3*time.Second):
		cancel()
		stack := make([]byte, 64<<10)
		stack = stack[:runtime.Stack(stack, true)]
		t.Fatalf("console_probe phase=%s cancellation did not join its owner; goroutines:\n%s", name, stack)
		return context.DeadlineExceeded
	}
}

// Explicit opt-in only. The workspace is read for real BEFORE/AFTER captures;
// Git objects, refs, locks and metadata remain entirely in portable TMP. The
// only executed project command is the observed benign IF EXIST / echo.
func TestConsoleActualHomeProbeWindows(t *testing.T) {
	supplied := strings.TrimSpace(os.Getenv("SUPERCLI_CONSOLE_PROBE_HOME"))
	if supplied == "" {
		t.Skip("set SUPERCLI_CONSOLE_PROBE_HOME for the read-only actual-home diagnostic")
	}
	home, err := filepath.Abs(supplied)
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatal("opt-in home must be an existing directory")
	}
	tmp := strings.TrimSpace(os.Getenv("TMP"))
	if tmp == "" {
		t.Fatal("opt-in diagnostic requires TMP pointing to a portable workspace directory")
	}
	tmp, err = filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if within(home, tmp) || pathEqual(home, tmp) {
		t.Fatal("portable TMP must be outside the opt-in home")
	}
	data := t.TempDir()
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	if !within(tmp, data) || within(home, data) || pathEqual(home, data) {
		t.Fatal("diagnostic checkpoint data must be under portable TMP and outside the opt-in home")
	}
	manager, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("console-opt-in-diagnostic", "benign instant console condition")
	operationJoined := true
	runPhase := func(name string, run func(context.Context) error) error {
		operationJoined = false
		err := consoleProbePhase(t, name, run)
		operationJoined = true
		return err
	}
	// Failed capture/finalization keeps its durable refs. After the callback has
	// joined, close only this disposable store's native owner handle, preserving
	// the recovery receipt until TempDir cleanup. Never release a live callback.
	t.Cleanup(func() {
		if !operationJoined {
			t.Error("diagnostic owner did not join; refusing cleanup of a live checkpoint operation")
			return
		}
		if !turn.mu.TryLock() {
			t.Error("joined diagnostic retained Turn.mu")
			return
		}
		active := turn.active
		turn.mu.Unlock()
		if !turn.barrier.mu.TryLock() {
			t.Error("joined diagnostic retained its barrier lock")
			return
		}
		members, finishing := turn.barrier.members, turn.barrier.finishing
		turn.barrier.mu.Unlock()
		if members != 0 || finishing {
			t.Errorf("joined diagnostic retained an active operation: members=%d finishing=%t", members, finishing)
		}
		if !manager.mu.TryLock() {
			t.Error("joined diagnostic retained Manager.mu")
			return
		}
		manager.mu.Unlock()
		lease, err := manager.gate.TryAcquire(context.Background())
		if err != nil {
			t.Error("joined diagnostic retained its store gate", err)
		} else if err := lease.Close(); err != nil {
			t.Error(err)
		}
		if active != nil && active.lease != nil {
			if err := active.lease.owner.Close(); err != nil {
				t.Error(err)
				return
			}
			if _, err := os.Stat(active.lease.path); err == nil {
				unlock, err := checkpointStoreLock(active.lease.path)
				if err != nil {
					t.Error("closed diagnostic owner still has an active native lock", err)
				} else if err := unlock(); err != nil {
					t.Error(err)
				}
			} else if !os.IsNotExist(err) {
				t.Error(err)
			}
		}
	})
	// Delimit real checkpoint admission with a harmless callback, so BEFORE and
	// native execution have independent caps. The native phase below still uses
	// the production wrapped ctx_execute implementation and actual cmd process.
	var admissionReached bool
	beforeSpec := turn.Wrap(tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		admissionReached = true
		return tools.Result{}, nil
	}})
	args := consoleInstantNativeArgs(t)
	err = runPhase("before", func(ctx context.Context) error {
		result, err := beforeSpec.Fn(ctx, args)
		return errors.Join(err, result.Err)
	})
	if err == nil && !admissionReached {
		t.Fatal("successful BEFORE did not reach its admission boundary")
	}
	if err == nil {
		runner := ctxexec.New(home)
		var once sync.Once
		var nativeStarted time.Time
		runner.Now = func() time.Time {
			now := time.Now()
			once.Do(func() { nativeStarted = now })
			return now
		}
		nativeSpec := turn.Wrap(tools.NewCtxExecuteTool(runner, home).Spec())
		err = runPhase("native", func(ctx context.Context) error {
			result, err := nativeSpec.Fn(ctx, args)
			if failure := errors.Join(err, result.Err); failure != nil {
				return failure
			}
			var native ctxexec.Result
			if err := json.Unmarshal([]byte(result.Text), &native); err != nil {
				return err
			}
			t.Logf("console_probe native_exit=%d native_duration_ms=%d native_dispatched=%t", native.ExitCode, native.DurationMS, !nativeStarted.IsZero())
			if native.ExitCode != 0 || native.OutputIncomplete || (strings.TrimSpace(native.Stdout) != "Downloads" && strings.TrimSpace(native.Stdout) != "brak") {
				return errors.New("unexpected benign native command result")
			}
			return nil
		})
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Error("benign native command did not complete normally; see native diagnostic outcome")
		}
	} else {
		t.Log("console_probe phase=native outcome=not_dispatched_due_to_checkpoint_diagnostic_cap_or_error")
	}
	ready, immediate := turn.Seal()
	if !immediate {
		t.Fatal("joined diagnostic phases retained a running tool/worker member")
	}
	select {
	case <-ready:
	default:
		t.Fatal("joined phases did not publish completion readiness")
	}
	if err != nil {
		recoverable := turn.active != nil && (turn.active.lease != nil || turn.active.mayHaveRefs)
		t.Logf("console_probe phase=after outcome=not_attempted_after_interrupted_diagnostic recoverable_checkpoint=%t", recoverable)
		// Diagnostic cancellation is not evidence that the user's command failed.
		return
	}
	err = runPhase("after", func(ctx context.Context) error {
		record, err := turn.Complete(ctx)
		if err == nil && record != nil {
			// Concurrent external changes are possible; echo did not cause them.
			t.Logf("console_probe workspace_changes_observed=%d", len(record.Files))
		}
		return err
	})
	if err != nil {
		recoverable := turn.active != nil && (turn.active.lease != nil || turn.active.mayHaveRefs)
		t.Logf("console_probe phase=after recoverable_checkpoint=%t", recoverable)
	} else {
		if turn.active != nil && turn.active.lease != nil {
			if _, err := os.Stat(turn.active.lease.path); !os.IsNotExist(err) {
				t.Fatal("successful completion retained its checkpoint owner receipt")
			}
		}
		if !manager.pendingMu.TryLock() {
			t.Fatal("successful completion retained its pending-owner lock")
		}
		pending := len(manager.pending)
		manager.pendingMu.Unlock()
		if pending != 0 {
			t.Fatal("successful completion retained a pending checkpoint owner")
		}
	}
	ready = turn.barrier.CompletionReady()
	select {
	case <-ready:
	default:
		t.Fatal("AFTER created a dangling operation owner")
	}
}
