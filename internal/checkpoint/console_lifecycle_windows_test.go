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

// Uses the real native cmd process and real Git BEFORE/AFTER snapshots. The
// observed command is a shell condition, so it must keep the conservative full
// checkpoint admission path rather than being added to a read-only allowlist.
func consoleInstantNativeArgs(t *testing.T) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(map[string]any{"command": []string{"cmd", "/c", `if exist . (echo Downloads) else (echo brak)`}})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func consoleAwaitNative(t *testing.T, done <-chan checkpointLifetimeResult) checkpointLifetimeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("native instant command/checkpoint completion did not release its owner")
		return checkpointLifetimeResult{}
	}
}

func consoleRunNative(t *testing.T, spec tools.Tool, args json.RawMessage) checkpointLifetimeResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan checkpointLifetimeResult, 1)
	go func() { result, err := spec.Fn(ctx, args); done <- checkpointLifetimeResult{result, err} }()
	result := consoleAwaitNative(t, done)
	if err := errors.Join(result.err, result.result.Err); err != nil {
		t.Fatalf("native command failed: %v", err)
	}
	var native ctxexec.Result
	if err := json.Unmarshal([]byte(result.result.Text), &native); err != nil {
		t.Fatal(err)
	}
	if native.ExitCode != 0 || native.OutputIncomplete || (strings.TrimSpace(native.Stdout) != "Downloads" && strings.TrimSpace(native.Stdout) != "brak") {
		t.Fatalf("unexpected exact command result: %+v", native)
	}
	return result
}

func TestConsoleInstantNativeCheckpointLifecycleWindows(t *testing.T) {
	for _, wrapper := range []string{"turn", "controller"} {
		t.Run(wrapper, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "ordinary.txt"), []byte("before\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			manager := openSnapshotTestManager(t, home)
			turn := manager.NewTurn("console-native", "instant native console condition")
			controller := NewController(manager, "console-native")
			if wrapper == "controller" {
				controller.Start("instant native console condition")
				turn = controller.currentTurn()
			}
			spec := tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec()
			if wrapper == "controller" {
				spec = controller.Wrap(spec)
			} else {
				spec = turn.Wrap(spec)
			}
			consoleRunNative(t, spec, consoleInstantNativeArgs(t))
			if !turn.touched || !turn.wholeWorkspace || turn.before == "" || turn.active == nil || turn.active.lease == nil {
				t.Fatal("native command bypassed real BEFORE admission")
			}
			ready, immediate := turn.Seal()
			if !immediate {
				t.Fatal("native command returned but checkpoint still owns a tool member")
			}
			select {
			case <-ready:
			default:
				t.Fatal("native command exit did not close completion signal")
			}
			finishCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
			defer stop()
			var record *Record
			var err error
			if wrapper == "controller" {
				record, err = controller.CompleteDeferred(finishCtx, func(*Record, error) { t.Error("finished native command unexpectedly deferred completion") })
			} else {
				record, err = turn.CompleteDeferred(finishCtx, func(*Record, error) { t.Error("finished native command unexpectedly deferred completion") })
			}
			if err != nil || record != nil {
				t.Fatalf("no-op native command completion: record=%v err=%v", record, err)
			}
			if _, err := os.Stat(turn.active.lease.path); !os.IsNotExist(err) {
				t.Fatalf("finished native command retained its owner lease: %v", err)
			}
		})
	}
}

// Signal the actual contended store-acquisition call, not an earlier generic
// context check. The mutex/gate remains held until cancellation has returned.
type consoleAfterAcquireContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *consoleAfterAcquireContext) Err() error {
	var callers [24]uintptr
	frames := runtime.CallersFrames(callers[:runtime.Callers(2, callers[:])])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "(*StoreGate).acquire") {
			c.once.Do(func() { close(c.entered) })
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestConsoleInstantNativeBlockedAfterReturnsCancellationWindows(t *testing.T) {
	home := t.TempDir()
	manager := openSnapshotTestManager(t, home)
	turn := manager.NewTurn("console-native-after", "instant command followed by contended AFTER")
	spec := turn.Wrap(tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec())
	consoleRunNative(t, spec, consoleInstantNativeArgs(t))
	before := turn.before
	gate, err := manager.gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &consoleAfterAcquireContext{Context: ctx, entered: make(chan struct{})}
	done := make(chan checkpointLifetimeResult, 1)
	go func() { _, err := turn.Complete(probe); done <- checkpointLifetimeResult{err: err} }()
	checkpointLifetimeSignal(t, probe.entered)
	cancel()
	result := consoleAwaitNative(t, done)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("blocked AFTER lost cancellation: %v", result.err)
	}
	if turn.before != before || !turn.touched || turn.active.lease == nil {
		t.Fatal("cancelled AFTER discarded original recovery ownership")
	}
	ready := turn.barrier.CompletionReady()
	select {
	case <-ready:
	default:
		t.Fatal("blocked AFTER invented a still-running native tool")
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	recoveryCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	defer stop()
	if record, err := turn.Complete(recoveryCtx); err != nil || record != nil {
		t.Fatalf("AFTER cleanup retry: record=%v err=%v", record, err)
	}
	if _, err := os.Stat(turn.active.lease.path); !os.IsNotExist(err) {
		t.Fatalf("cleanup retry retained owner lease: %v", err)
	}
}

func TestConsoleInstantNativeAfterStoreFailureKeepsRecoverableOwnerWindows(t *testing.T) {
	home := t.TempDir()
	manager := openSnapshotTestManager(t, home)
	turn := manager.NewTurn("console-native-store-failure", "instant command followed by malformed local metadata")
	spec := turn.Wrap(tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec())
	consoleRunNative(t, spec, consoleInstantNativeArgs(t))
	// An actual disk-backed read failure after the native command has returned.
	// The active pin/lease remains recoverable; it is not a running process.
	if err := os.WriteFile(manager.meta, []byte("[broken"), 0600); err != nil {
		t.Fatal(err)
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	defer stop()
	if _, err := turn.Complete(finishCtx); err == nil {
		t.Fatal("AFTER silently accepted corrupt metadata")
	}
	ready := turn.barrier.CompletionReady()
	select {
	case <-ready:
	default:
		t.Fatal("store failure retained an executed tool member")
	}
	if turn.active.lease == nil {
		t.Fatal("store failure discarded owner lease")
	}
	if _, err := os.Stat(turn.active.lease.path); err != nil {
		t.Fatal("store failure lost recoverable lease receipt")
	}
	if err := os.Remove(manager.meta); err != nil {
		t.Fatal(err)
	}
	if record, err := turn.Complete(finishCtx); err != nil || record != nil {
		t.Fatalf("store repair completion: record=%v err=%v", record, err)
	}
	if _, err := os.Stat(turn.active.lease.path); !os.IsNotExist(err) {
		t.Fatalf("store repair retained owner lease: %v", err)
	}
}
