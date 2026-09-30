package fileops

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// refs covers both holders and waiters. The semaphore allows a canceled waiter
// to leave without waiting for the current filesystem mutation to finish.
type mutationPathLock struct {
	token chan struct{}
	refs  int
}

var mutationPaths = struct {
	sync.Mutex
	locks map[string]*mutationPathLock
}{locks: make(map[string]*mutationPathLock)}

// LockMutationPaths is the non-cancelable compatibility form.
func LockMutationPaths(paths ...string) func() {
	release, _ := LockMutationPathsContext(context.Background(), paths...)
	return release
}

// LockMutationPathsContext serializes canonical paths in lexical order. Parent
// and worker tools share this queue; unrelated paths remain independent. On
// cancellation it releases any acquired paths and every waiter reference.
func LockMutationPathsContext(ctx context.Context, paths ...string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if path == "" {
			continue
		}
		key := canonicalMutationPath(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	locks := make([]*mutationPathLock, len(keys))
	mutationPaths.Lock()
	for i, key := range keys {
		lock := mutationPaths.locks[key]
		if lock == nil {
			lock = &mutationPathLock{token: make(chan struct{}, 1)}
			mutationPaths.locks[key] = lock
		}
		lock.refs++
		locks[i] = lock
	}
	mutationPaths.Unlock()
	release := func(acquired int) {
		for i := acquired - 1; i >= 0; i-- {
			<-locks[i].token
		}
		mutationPaths.Lock()
		for i, key := range keys {
			locks[i].refs--
			if locks[i].refs == 0 {
				delete(mutationPaths.locks, key)
			}
		}
		mutationPaths.Unlock()
	}
	for i, lock := range locks {
		select {
		case <-ctx.Done():
			release(i)
			return nil, ctx.Err()
		case lock.token <- struct{}{}:
		}
		if err := ctx.Err(); err != nil {
			release(i + 1)
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		release(len(locks))
		return nil, err
	}
	return func() { release(len(locks)) }, nil
}

func canonicalMutationPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	abs = filepath.Clean(abs)

	// EvalSymlinks requires the final path to exist. For creates, resolve the
	// nearest existing ancestor and append the missing suffix again.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		ancestor := abs
		var suffix []string
		for {
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				break
			}
			suffix = append(suffix, filepath.Base(ancestor))
			ancestor = parent
			if resolved, resolveErr := filepath.EvalSymlinks(ancestor); resolveErr == nil {
				abs = resolved
				for i := len(suffix) - 1; i >= 0; i-- {
					abs = filepath.Join(abs, suffix[i])
				}
				break
			}
		}
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

func mutationQueueSize() int {
	mutationPaths.Lock()
	defer mutationPaths.Unlock()
	return len(mutationPaths.locks)
}
