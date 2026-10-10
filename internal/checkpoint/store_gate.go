package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	ErrStoreBusy        = errors.New("checkpoint store is busy")
	ErrStoreUnsupported = errors.New("checkpoint store locking is unsupported on this operating system")
)

const checkpointStoreLockName = ".checkpoint-store.lock"

// A fixed set avoids a process-lifetime registry of every data directory ever
// opened. Hash collisions only serialize short I/O for independent stores.
var checkpointStoreLocalTokens = func() [256]chan struct{} {
	var tokens [256]chan struct{}
	for i := range tokens {
		tokens[i] = make(chan struct{}, 1)
	}
	return tokens
}()

// StoreGate serializes short checkpoint I/O transactions across independent
// managers and processes. It must not be held while running a model or the
// original tool callback.
// The persistent lock file is beside checkpoints, so clearing that directory
// cannot replace the locked inode. Neither this gate nor callers may unlink it.
// Coordination requires every store writer to use this gate.
type StoreGate struct {
	path  string
	local chan struct{}
}

// NewStoreGate resolves an existing portable data directory without creating
// directories or lock files. Aliases of that directory use the same native lock.
func NewStoreGate(dataDir string) (*StoreGate, error) {
	if dataDir == "" {
		return nil, errors.New("checkpoint data directory is empty")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("checkpoint data directory: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("checkpoint data directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("checkpoint data directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("checkpoint data directory is not a directory")
	}
	path := filepath.Join(abs, checkpointStoreLockName)
	return &StoreGate{path: path, local: checkpointStoreLocalToken(path)}, nil
}

func checkpointStoreLocalToken(path string) chan struct{} {
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	hash := fnv.New32a()
	hash.Write([]byte(path))
	return checkpointStoreLocalTokens[hash.Sum32()%uint32(len(checkpointStoreLocalTokens))]
}

// StoreIO owns a successfully acquired native lock. Do not copy it. Close is
// safe to call concurrently and returns the same release error on every call.
// Canceling the acquisition context does not release an acquired lease: the
// caller must close it after its I/O and recovery work finishes.
type StoreIO struct {
	once   sync.Once
	unlock func() error
	err    error
}

// TryAcquire makes one nonblocking local attempt, then at most one nonblocking
// native lock attempt. ErrStoreBusy means the caller must refuse this
// transaction; this method never retries,
// polls, starts a goroutine or waits for another writer. Context is checked
// before opening the file and after acquiring the lock; it cannot interrupt a
// filesystem open. A canceled post-acquisition request releases its lease.
func (g *StoreGate) TryAcquire(ctx context.Context) (*StoreIO, error) {
	return g.acquire(ctx, false)
}

// Acquire waits only for a local token shared by gates for this portable store,
// honoring context cancellation without polling. Once admitted it makes exactly
// one native nonblocking attempt, returning ErrStoreBusy for another process.
// It never waits or retries on that external busy result. Local hash collisions
// may briefly serialize independent stores. This gate is not reentrant.
func (g *StoreGate) Acquire(ctx context.Context) (*StoreIO, error) {
	return g.acquire(ctx, true)
}

func (g *StoreGate) acquire(ctx context.Context, waitLocal bool) (*StoreIO, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil || g.path == "" || g.local == nil {
		return nil, errors.New("checkpoint store gate is not initialized")
	}
	if waitLocal {
		select {
		case g.local <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		select {
		case g.local <- struct{}{}:
		default:
			return nil, ErrStoreBusy
		}
	}
	// Cancellation may have raced with a ready token; release before any open.
	if err := ctx.Err(); err != nil {
		<-g.local
		return nil, err
	}
	if err := checkCheckpointStoreLockPath(g.path); err != nil {
		<-g.local
		return nil, err
	}
	unlock, err := checkpointStoreLock(g.path)
	if err != nil {
		<-g.local
		return nil, err
	}
	lease := &StoreIO{unlock: func() error {
		defer func() { <-g.local }()
		return unlock()
	}}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	return lease, nil
}

// Close releases the native lock without deleting or truncating its file.
// A nil or zero StoreIO has nothing to release.
func (l *StoreIO) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.unlock != nil {
			l.err = l.unlock()
		}
	})
	return l.err
}

// WithIO uses nonblocking TryAcquire for one callback and always releases the
// lease, including when the callback fails or panics. A release error is joined
// with the callback error.
// The callback must honor ctx and must not reacquire this non-reentrant gate.
func (g *StoreGate) WithIO(ctx context.Context, fn func(context.Context) error) (err error) {
	if fn == nil {
		return errors.New("checkpoint store I/O callback is nil")
	}
	lease, err := g.TryAcquire(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return fn(ctx)
}

func checkCheckpointStoreLockPath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checkpoint store lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("checkpoint store lock must be a regular file, not a link or special file")
	}
	return nil
}
