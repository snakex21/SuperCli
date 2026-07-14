package fileops

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// mutationPathLock serializes mutations of one canonical filesystem path.
// refs includes holders and waiters, so entries can be removed without racing
// a goroutine that has found the entry but has not acquired its mutex yet.
type mutationPathLock struct {
	mu   sync.Mutex
	refs int
}

var mutationPaths = struct {
	sync.Mutex
	locks map[string]*mutationPathLock
}{locks: make(map[string]*mutationPathLock)}

// LockMutationPaths serializes mutations touching the supplied paths. Paths
// are canonicalized, deduplicated and locked in lexical order, which makes
// multi-path operations such as move and copy deadlock-free.
//
// The queue is process-wide on purpose: parent and delegated agents use
// separate tool instances but share the same filesystem. Unrelated paths do
// not block one another, and the map entry disappears after the last waiter.
func LockMutationPaths(paths ...string) func() {
	keys := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
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
			lock = &mutationPathLock{}
			mutationPaths.locks[key] = lock
		}
		lock.refs++
		locks[i] = lock
	}
	mutationPaths.Unlock()

	for _, lock := range locks {
		lock.mu.Lock()
	}

	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
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
