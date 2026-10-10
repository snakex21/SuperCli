//go:build windows || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/system/childproc"
)

func newTestStoreGate(t *testing.T, data string) *StoreGate {
	t.Helper()
	gate, err := NewStoreGate(data)
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func mustAcquireStoreGate(t *testing.T, gate *StoreGate) *StoreIO {
	t.Helper()
	lease, err := gate.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})
	return lease
}

func requireStoreBusy(t *testing.T, gate *StoreGate) {
	t.Helper()
	lease, err := gate.TryAcquire(context.Background())
	if lease != nil {
		lease.Close()
		t.Fatal("busy gate returned a lease")
	}
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("expected ErrStoreBusy, got %v", err)
	}
}

func TestStoreGateExistingPortableDirectory(t *testing.T) {
	data := t.TempDir()
	gate := newTestStoreGate(t, data)
	canonical, err := filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	if gate.path != filepath.Join(canonical, checkpointStoreLockName) {
		t.Fatalf("lock is not beside checkpoints: %s", gate.path)
	}
	if _, err := os.Stat(gate.path); !os.IsNotExist(err) {
		t.Fatalf("constructor created a lock file: %v", err)
	}
	missing := filepath.Join(data, "missing")
	if _, err := NewStoreGate(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("constructor created a data directory: %v", err)
	}
	regular := filepath.Join(data, "regular")
	if err := os.WriteFile(regular, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStoreGate(regular); err == nil {
		t.Fatal("accepted a file as data directory")
	}
	if _, err := NewStoreGate(""); err == nil {
		t.Fatal("accepted an empty data directory")
	}
}

func TestStoreGateIndependentInstancesAndRelease(t *testing.T) {
	data := t.TempDir()
	a := newTestStoreGate(t, data)
	b := newTestStoreGate(t, filepath.Join(data, "."))
	sentinel := []byte("persistent lock file content")
	if err := os.WriteFile(a.path, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(a.path)
	if err != nil {
		t.Fatal(err)
	}
	lease := mustAcquireStoreGate(t, a)
	requireStoreBusy(t, a) // The same gate is deliberately not reentrant.
	requireStoreBusy(t, b)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	other := mustAcquireStoreGate(t, b)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(a.path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("gate inode replaced: %v", err)
	}
	content, err := os.ReadFile(a.path)
	if err != nil || !bytes.Equal(content, sentinel) {
		t.Fatalf("gate file was truncated or rewritten: %v", err)
	}
}

func TestStoreGateSurvivesCheckpointDirectoryReplacement(t *testing.T) {
	data := t.TempDir()
	gate := newTestStoreGate(t, data)
	checkpoints := filepath.Join(data, "checkpoints")
	if err := os.Mkdir(checkpoints, 0700); err != nil {
		t.Fatal(err)
	}
	lease := mustAcquireStoreGate(t, gate)
	if err := os.Remove(checkpoints); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(checkpoints, 0700); err != nil {
		t.Fatal(err)
	}
	requireStoreBusy(t, newTestStoreGate(t, data))
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, gate)
}

func TestStoreGateCanonicalDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(data, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	a := newTestStoreGate(t, data)
	b := newTestStoreGate(t, alias)
	if a.path != b.path {
		t.Fatalf("aliases resolved to different locks: %s / %s", a.path, b.path)
	}
	mustAcquireStoreGate(t, a)
	requireStoreBusy(t, b)
}

func TestStoreGateCanceledRequestDoesNotCreateLock(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lease, err := gate.TryAcquire(ctx)
	if lease != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: lease=%v, error=%v", lease, err)
	}
	if _, err := os.Stat(gate.path); !os.IsNotExist(err) {
		t.Fatalf("canceled acquisition wrote a lock file: %v", err)
	}
}

type cancelAfterStoreAttemptContext struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (c *cancelAfterStoreAttemptContext) Err() error {
	if c.calls.Add(1) == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestStoreGateCanceledAfterAttemptReleasesLease(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterStoreAttemptContext{Context: base, cancel: cancel}
	lease, err := gate.TryAcquire(ctx)
	if lease != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("post-acquisition cancellation: lease=%v, error=%v", lease, err)
	}
	if ctx.calls.Load() != 3 {
		t.Fatalf("expected context checks before local/native attempts and after acquisition, got %d", ctx.calls.Load())
	}
	mustAcquireStoreGate(t, gate)
}

type localStoreWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *localStoreWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type storeGateAcquireResult struct {
	lease *StoreIO
	err   error
}

func TestStoreGateAcquireWaitsLocallyAndCanCancel(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		name := "release"
		if cancelWaiting {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			a := newTestStoreGate(t, data)
			b := newTestStoreGate(t, filepath.Join(data, "."))
			if a.local != b.local {
				t.Fatal("independent gates did not share the local token")
			}
			first := mustAcquireStoreGate(t, a)
			base, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ctx := &localStoreWaitContext{Context: base, waiting: make(chan struct{})}
			result := make(chan storeGateAcquireResult, 1)
			go func() {
				lease, err := b.Acquire(ctx)
				result <- storeGateAcquireResult{lease: lease, err: err}
			}()
			// Done is evaluated at the local select. This handshake observes
			// admission to the wait without sleeps or progress polling.
			select {
			case <-ctx.waiting:
			case got := <-result:
				if got.lease != nil {
					got.lease.Close()
				}
				t.Fatalf("Acquire returned before local wait: %v", got.err)
			case <-base.Done():
				t.Fatal("Acquire never reached the local wait")
			}
			if cancelWaiting {
				cancel()
			} else if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			got := <-result // One completion receive, no polling.
			if cancelWaiting {
				if got.lease != nil || !errors.Is(got.err, context.Canceled) {
					t.Fatalf("local cancellation: lease=%v, error=%v", got.lease, got.err)
				}
				requireStoreBusy(t, b) // Canceling a waiter cannot release the owner.
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if got.err != nil || got.lease == nil {
					t.Fatalf("local wait did not acquire after release: %v", got.err)
				}
				requireStoreBusy(t, a)
				if err := got.lease.Close(); err != nil {
					t.Fatal(err)
				}
			}
			mustAcquireStoreGate(t, a)
		})
	}
}

func TestStoreGateLocalTokenReleasedOnIOError(t *testing.T) {
	data := t.TempDir()
	gate := newTestStoreGate(t, data)
	if err := os.Mkdir(gate.path, 0700); err != nil {
		t.Fatal(err)
	}
	if lease, err := gate.Acquire(context.Background()); lease != nil || err == nil || errors.Is(err, ErrStoreBusy) {
		t.Fatalf("invalid lock did not fail: lease=%v, error=%v", lease, err)
	}
	if err := os.Remove(gate.path); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, newTestStoreGate(t, data))
}

func TestStoreGateLocalTokenReleasedOnNativeOpenError(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	gate := newTestStoreGate(t, data)
	if err := os.Remove(data); err != nil {
		t.Fatal(err)
	}
	if lease, err := gate.Acquire(context.Background()); lease != nil || !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrStoreBusy) {
		t.Fatalf("native open error lost: lease=%v, error=%v", lease, err)
	}
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, newTestStoreGate(t, data))
}

func TestStoreGateBoundedLocalStripes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[chan struct{}]string)
	var first, collision string
	for i := 0; i <= len(checkpointStoreLocalTokens); i++ {
		data := filepath.Join(root, fmt.Sprintf("data-%d", i))
		token := checkpointStoreLocalToken(filepath.Join(data, checkpointStoreLockName))
		if previous, ok := seen[token]; ok {
			first, collision = previous, data
			break
		}
		seen[token] = data
	}
	if first == "" {
		t.Fatal("fixed token table did not produce a collision within 257 paths")
	}
	for _, data := range []string{first, collision} {
		if err := os.Mkdir(data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestStoreGate(t, first)
	b := newTestStoreGate(t, collision)
	lease := mustAcquireStoreGate(t, a)
	requireStoreBusy(t, b)
	if _, err := os.Stat(b.path); !os.IsNotExist(err) {
		t.Fatalf("local busy attempted native file creation: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, b)
}

func TestStoreGateCancellationDoesNotReleaseLiveIO(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lease, err := gate.TryAcquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	cancel()
	requireStoreBusy(t, gate)
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	mustAcquireStoreGate(t, gate)
}

func TestStoreGateWithIOFailureBusyAndPanic(t *testing.T) {
	gate := newTestStoreGate(t, t.TempDir())
	fault := errors.New("synthetic capture failure")
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "fixture")
	if err := gate.WithIO(ctx, func(callbackCtx context.Context) error {
		if callbackCtx != ctx {
			t.Fatal("callback lost its context")
		}
		called := false
		err := gate.WithIO(ctx, func(context.Context) error {
			called = true
			return nil
		})
		if !errors.Is(err, ErrStoreBusy) || called {
			t.Fatalf("busy callback ran: called=%v, error=%v", called, err)
		}
		return fault
	}); !errors.Is(err, fault) {
		t.Fatalf("callback error lost: %v", err)
	}
	func() {
		defer func() {
			if got := recover(); got != fault {
				t.Fatalf("callback panic lost: %v", got)
			}
		}()
		gate.WithIO(context.Background(), func(context.Context) error { panic(fault) })
	}()
	mustAcquireStoreGate(t, gate)
}

func TestStoreIOConcurrentCloseRetainsError(t *testing.T) {
	fault := errors.New("synthetic unlock failure")
	var calls atomic.Int32
	lease := &StoreIO{unlock: func() error {
		calls.Add(1)
		return fault
	}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lease.Close(); !errors.Is(err, fault) {
				t.Errorf("release error lost: %v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("unlock called %d times", calls.Load())
	}
	if err := (*StoreIO)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := new(StoreIO).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreGateInvalidLockDoesNotReportBusy(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		gate := newTestStoreGate(t, t.TempDir())
		if err := os.Mkdir(gate.path, 0700); err != nil {
			t.Fatal(err)
		}
		lease, err := gate.TryAcquire(context.Background())
		if lease != nil || err == nil || errors.Is(err, ErrStoreBusy) {
			t.Fatalf("invalid lock confused with busy: lease=%v, error=%v", lease, err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		data := filepath.Join(root, "data")
		if err := os.Mkdir(data, 0700); err != nil {
			t.Fatal(err)
		}
		gate := newTestStoreGate(t, data)
		target := filepath.Join(root, "outside-lock")
		if err := os.Symlink(target, gate.path); err != nil {
			t.Skipf("file symlinks unavailable: %v", err)
		}
		lease, err := gate.TryAcquire(context.Background())
		if lease != nil || err == nil || errors.Is(err, ErrStoreBusy) {
			t.Fatalf("invalid link confused with busy: lease=%v, error=%v", lease, err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("lock created a file outside data directory: %v", err)
		}
	})
	t.Run("uninitialized", func(t *testing.T) {
		for _, gate := range []*StoreGate{nil, {}} {
			if lease, err := gate.TryAcquire(context.Background()); lease != nil || err == nil || errors.Is(err, ErrStoreBusy) {
				t.Fatalf("uninitialized gate: lease=%v, error=%v", lease, err)
			}
		}
	})
}

const storeGateHelperDir = "SUPERCLI_TEST_CHECKPOINT_STORE_DIR"
const storeGateHelperMode = "SUPERCLI_TEST_CHECKPOINT_STORE_MODE"

type storeGateTestProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr bytes.Buffer
	waited bool
}

func startStoreGateTestProcess(t *testing.T, data, mode string) *storeGateTestProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	p := &storeGateTestProcess{}
	p.cmd = childproc.HideWindow(exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStoreGateProcessHelper$"))
	p.cmd.Env = append(os.Environ(), storeGateHelperDir+"="+data, storeGateHelperMode+"="+mode)
	p.cmd.Stderr = &p.stderr
	var err error
	if p.stdout, err = p.cmd.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	if p.stdin, err = p.cmd.StdinPipe(); err != nil {
		p.stdout.Close()
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		p.stdin.Close()
		p.stdout.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.stdin.Close()
		if !p.waited {
			p.cmd.Process.Kill()
			p.cmd.Wait()
			p.waited = true
		}
		p.stdout.Close()
	})
	var ready [1]byte
	if _, err := io.ReadFull(p.stdout, ready[:]); err != nil || ready[0] != 1 {
		p.cmd.Process.Kill()
		waitErr := p.cmd.Wait()
		p.waited = true
		t.Fatalf("helper handshake failed: read=%v, wait=%v, stderr=%s", err, waitErr, p.stderr.String())
	}
	return p
}

func (p *storeGateTestProcess) finish(t *testing.T) {
	t.Helper()
	if _, err := p.stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	p.stdin.Close()
	err := p.cmd.Wait() // Completion wait, no process, file, timer or log polling.
	p.waited = true
	if err != nil {
		t.Fatalf("helper completion failed: %v, stderr=%s", err, p.stderr.String())
	}
}

func TestStoreGateCrossProcess(t *testing.T) {
	for _, mode := range []string{"close", "exit"} {
		t.Run(mode, func(t *testing.T) {
			data := t.TempDir()
			process := startStoreGateTestProcess(t, data, mode)
			gate := newTestStoreGate(t, data)
			requireStoreBusy(t, gate)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if lease, err := gate.Acquire(ctx); lease != nil || !errors.Is(err, ErrStoreBusy) {
				if lease != nil {
					lease.Close()
				}
				t.Fatalf("Acquire retried or waited on another process: %v", err)
			}
			requireStoreBusy(t, gate) // Failed native attempt must return its local token.
			process.finish(t)
			// The OS must release on process exit, even without StoreIO.Close.
			mustAcquireStoreGate(t, gate)
		})
	}
}

func TestStoreGateProcessHelper(t *testing.T) {
	data := os.Getenv(storeGateHelperDir)
	if data == "" {
		return
	}
	mode := os.Getenv(storeGateHelperMode)
	if mode != "close" && mode != "exit" {
		t.Fatal("unknown checkpoint store helper mode")
	}
	gate := newTestStoreGate(t, data)
	lease, err := gate.TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mode == "close" {
		defer func() {
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	if _, err := os.Stdout.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	var finish [1]byte
	if _, err := io.ReadFull(os.Stdin, finish[:]); err != nil || finish[0] != 1 {
		t.Fatalf("helper completion handshake: %v", err)
	}
	if mode == "exit" {
		os.Exit(0) // Deliberately bypass all Go defers to test native release.
	}
}
