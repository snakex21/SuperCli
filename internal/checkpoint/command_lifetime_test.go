package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

type checkpointAdmissionContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *checkpointAdmissionContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Err()
}

type checkpointLifetimeResult struct {
	result tools.Result
	err    error
}

func checkpointLifetimeSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic invocation did not enter admission")
	}
}

func checkpointLifetimeAwait(t *testing.T, done <-chan checkpointLifetimeResult) checkpointLifetimeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled/bounded invocation still waits for the held checkpoint lock")
	}
	return checkpointLifetimeResult{}
}

func TestCheckpointCommandAdmissionCancelAndDeadlineBeforeRunner(t *testing.T) {
	for _, wrapper := range []string{"turn", "controller"} {
		for _, held := range []string{"store", "turn"} {
			for _, cause := range []string{"cancel", "deadline"} {
				t.Run(wrapper+"/"+held+"/"+cause, func(t *testing.T) {
					m := openSnapshotTestManager(t, t.TempDir())
					turn := m.NewTurn("synthetic-admission", "bounded command")
					controller := NewController(m, "synthetic-admission")
					if wrapper == "controller" {
						controller.Start("bounded command")
						turn = controller.currentTurn()
					}
					var release func()
					if held == "store" {
						unlock, err := m.lockStore(context.Background())
						if err != nil {
							t.Fatal(err)
						}
						var once sync.Once
						release = func() {
							once.Do(func() {
								if err := unlock(); err != nil {
									t.Error(err)
								}
							})
						}
					} else {
						turn.mu.Lock()
						var once sync.Once
						release = func() { once.Do(turn.mu.Unlock) }
					}
					defer release()
					parent, cancel := context.WithCancel(context.Background())
					defer cancel()
					probe := &checkpointAdmissionContext{Context: parent, entered: make(chan struct{})}
					var runnerCalls atomic.Int32
					runner := ctxexec.New(m.home)
					runner.LookPath = func(string) (string, error) {
						runnerCalls.Add(1)
						return "", errors.New("synthetic command must not reach native lookup")
					}
					spec := tools.NewCtxExecuteTool(runner, m.home).Spec()
					if wrapper == "controller" {
						spec = controller.Wrap(spec)
					} else {
						spec = turn.Wrap(spec)
					}
					timeout := 0
					wantCause := error(context.Canceled)
					if cause == "deadline" {
						timeout = 10
						wantCause = context.DeadlineExceeded
					}
					args := json.RawMessage(fmt.Sprintf(`{"command":["synthetic-no-network"],"timeout_ms":%d}`, timeout))
					done := make(chan checkpointLifetimeResult, 1)
					finished := make(chan struct{})
					go func() {
						defer close(finished)
						result, err := spec.Fn(probe, args)
						done <- checkpointLifetimeResult{result, err}
					}()
					t.Cleanup(func() {
						cancel()
						release()
						checkpointLifetimeSignal(t, finished)
						ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						if _, err := turn.Complete(ctx); err != nil {
							t.Error(err)
						}
					})
					if cause == "cancel" {
						checkpointLifetimeSignal(t, probe.entered)
						cancel()
					}
					result := checkpointLifetimeAwait(t, done)
					if !errors.Is(errors.Join(result.err, result.result.Err), wantCause) || runnerCalls.Load() != 0 {
						t.Fatalf("admission cause=%v result=%v err=%v native lookups=%d", wantCause, result.result.Err, result.err, runnerCalls.Load())
					}
					if _, immediate := turn.Seal(); !immediate {
						t.Fatal("failed admission leaked an accepted mutation member")
					}
				})
			}
		}
	}
}

func TestDeferredCompletionAndKeyDoNotWaitForTurnMutex(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	turn := m.NewTurn("synthetic-pending", "pending before capture")
	borrow, err := turn.barrier.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	turn.mu.Lock()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(turn.mu.Unlock) }
	defer release()
	defer borrow.Close()
	done := make(chan checkpointLifetimeResult, 1)
	foreground := make(chan checkpointLifetimeResult, 1)
	go func() {
		record, err := turn.CompleteDeferred(context.Background(), func(record *Record, err error) {
			if record != nil {
				err = errors.Join(err, errors.New("untouched synthetic worker created a record"))
			}
			done <- checkpointLifetimeResult{err: err}
		})
		if record != nil {
			err = errors.Join(err, errors.New("pending turn returned a record"))
		}
		foreground <- checkpointLifetimeResult{err: err}
	}()
	result := checkpointLifetimeAwait(t, foreground)
	if result.err != nil || len(turn.CompletionKey()) != 32 {
		t.Fatalf("pending completion/key waited or failed: %v", result.err)
	}
	key := turn.CompletionKey()
	select {
	case <-done:
		t.Fatal("completion ran before borrower closed")
	default:
	}
	release()
	borrow.Close()
	result = checkpointLifetimeAwait(t, done)
	if result.err != nil || turn.CompletionKey() != key {
		t.Fatalf("event-driven finalizer changed completion identity: %v", result.err)
	}
}

func TestCancelledStoreAdmissionCompletesWithoutReacquiringHeldGate(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	turn := m.NewTurn("synthetic-held-gate", "cancel before native lease")
	// Exercise the precise tail even if cancellation wins the admission race:
	// only the lazily allocated identity exists; no native lease/ref was touched.
	if err := turn.ensureActivePinsLocked(); err != nil {
		t.Fatal(err)
	}
	gate, err := m.gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &checkpointAdmissionContext{Context: ctx, entered: make(chan struct{})}
	var calls atomic.Int32
	spec := turn.Wrap(tools.Tool{Name: "ctx_execute", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { calls.Add(1); return tools.Result{}, nil }})
	done := make(chan checkpointLifetimeResult, 1)
	go func() {
		result, err := spec.Fn(probe, json.RawMessage(`{"command":["synthetic-no-network"]}`))
		done <- checkpointLifetimeResult{result, err}
	}()
	checkpointLifetimeSignal(t, probe.entered)
	cancel()
	result := checkpointLifetimeAwait(t, done)
	if !errors.Is(errors.Join(result.err, result.result.Err), context.Canceled) || calls.Load() != 0 {
		t.Fatal("cancelled store admission ran the command")
	}
	completed := make(chan checkpointLifetimeResult, 1)
	go func() {
		record, err := turn.CompleteDeferred(context.Background(), func(*Record, error) { t.Error("untouched turn should complete inline") })
		if record != nil {
			err = errors.Join(err, errors.New("untouched admission produced a record"))
		}
		completed <- checkpointLifetimeResult{err: err}
	}()
	if result := checkpointLifetimeAwait(t, completed); result.err != nil {
		t.Fatal(result.err)
	}
	if turn.touched || turn.active != nil && (turn.active.lease != nil || turn.active.mayHaveRefs) {
		t.Fatal("RAM-only admission acquired durable resources")
	}
}

func TestActiveRefPublicationAttemptPreventsRAMOnlyCleanup(t *testing.T) {
	m := openSnapshotTestManager(t, t.TempDir())
	turn := m.NewTurn("synthetic-ref-attempt", "failed before publication")
	if err := turn.ensureActivePinsLocked(); err != nil {
		t.Fatal(err)
	}
	// The marker is conservative even when cancellation prevents the command
	// from starting: an actual partial publication must take the same gated path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := turn.active.publish(ctx, "before", "1111111111111111111111111111111111111111"); err == nil || !turn.active.mayHaveRefs {
		t.Fatal("publication failure lost durable-ref uncertainty")
	}
	if err := turn.releaseActivePins(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("uncertain refs bypassed gated cleanup: %v", err)
	}
}

func TestCancelledCommandStillCapturesAfterAndCanUndo(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "synthetic.txt")
	before, after := []byte("before\r\n"), []byte("after\r\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	m := openSnapshotTestManager(t, home)
	turn := m.NewTurn("synthetic-cancel-after", "mutating command interrupted")
	entered := make(chan struct{})
	spec := turn.Wrap(tools.Tool{Name: "ctx_execute", Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		if err := os.WriteFile(path, after, 0600); err != nil {
			return tools.Result{}, err
		}
		close(entered)
		<-ctx.Done()
		return tools.Result{Err: ctx.Err()}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan checkpointLifetimeResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result, err := spec.Fn(ctx, json.RawMessage(`{"command":["synthetic-mutator"]}`))
		done <- checkpointLifetimeResult{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		checkpointLifetimeSignal(t, finished)
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := turn.Complete(cleanupCtx); err != nil {
			t.Error(err)
		}
	})
	checkpointLifetimeSignal(t, entered)
	cancel()
	if result := checkpointLifetimeAwait(t, done); !errors.Is(errors.Join(result.err, result.result.Err), context.Canceled) {
		t.Fatal("command lost cancellation")
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	record, err := turn.Complete(finishCtx)
	if err != nil || record == nil {
		t.Fatalf("interrupted mutation lost its checkpoint: %v", err)
	}
	if _, err := m.Undo(finishCtx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, before)
	if _, err := m.Redo(finishCtx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, path, after)
}

func BenchmarkCheckpointContextMutex(b *testing.B) {
	b.Run("sync", func(b *testing.B) {
		var lock sync.Mutex
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			lock.Lock()
			lock.Unlock()
		}
	})
	b.Run("context", func(b *testing.B) {
		var lock contextMutex
		lock.Lock()
		lock.Unlock()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lock.Lock()
			lock.Unlock()
		}
	})
}
