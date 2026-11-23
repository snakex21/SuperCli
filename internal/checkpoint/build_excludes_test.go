package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTree(t *testing.T, home string, files ...string) {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(home, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openExcludeManager(t *testing.T, home string) *Manager {
	t.Helper()
	m, err := Open(home, t.TempDir())
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 1.0.38: build caches and outputs are never scanned by a checkpoint, so a
// project full of Zig/node/Python caches cannot make snapshots slow.
func TestCheckpointSkipsBuildCachesAndOutputs(t *testing.T) {
	// Not parallel: the scan hook and WholeSnapshotBudget are package
	// globals, and a parallel test clearing or replacing the hook made the
	// slow scan finish inside the budget (flaky on Linux CI).
	home := t.TempDir()
	writeTree(t, home,
		"src/main.zig", "build-migration/run-1/notes.md",
		"zig-engine/.zig-cache/o/abc/file.o", "zig-out/bin/game.exe",
		"build-migration/run-1/zig-global/z/cache.bin", "web/node_modules/pkg/index.js",
		"tools/__pycache__/x.pyc", ".venv/lib/site.py", "target/debug/app", "dist/app.js",
		"runs/r7/zcache/o/x.o", "custom-out/a.bin", "keep/build/script.txt",
	)
	m := openExcludeManager(t, home)
	m.SetExcludeRules([]string{"custom-out", "!build"})
	// A command declaring its Zig cache directory excludes it as well.
	m.noteCommandCaches([]byte(`{"command":["powershell","-c","$env:ZIG_GLOBAL_CACHE_DIR='runs/r7/zcache'; zig build test"]}`), "")

	var mu sync.Mutex
	var visited []string
	setSnapshotScanHook(func(p string) { mu.Lock(); visited = append(visited, p); mu.Unlock() })
	defer setSnapshotScanHook(nil)
	if _, err := m.captureSnapshot(context.Background(), nil, "", nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	got := strings.Join(visited, "\n")
	for _, want := range []string{"src/main.zig", "build-migration/run-1/notes.md", "keep/build/script.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("source file %s not snapshotted:\n%s", want, got)
		}
	}
	for _, skipped := range []string{".zig-cache", "zig-out", "zig-global", "node_modules", "__pycache__", ".venv", "target/", "dist/", "zcache", "custom-out"} {
		if strings.Contains(got, skipped) {
			t.Errorf("%s was scanned:\n%s", skipped, got)
		}
	}
	// Directory roots (scoped captures) skip the same directories.
	visited = nil
	mu.Unlock()
	files, err := m.preflightSnapshot(context.Background(), []string{"build-migration", "web"}, nil)
	mu.Lock()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f.path, "zig-global") || strings.Contains(f.path, "node_modules") {
			t.Fatalf("scoped capture included %s", f.path)
		}
	}
}

// A whole-workspace snapshot that takes too long no longer holds the shared
// checkpoint lock: the command runs with a note, and another agent's file
// write gets the lock within a short bound.
func TestSlowWorkspaceSnapshotDoesNotBlockOtherAgentsWrite(t *testing.T) {
	// Not parallel: the scan hook and WholeSnapshotBudget are package
	// globals, and a parallel test clearing or replacing the hook made the
	// slow scan finish inside the budget (flaky on Linux CI).
	home := t.TempDir()
	var files []string
	for i := 0; i < 400; i++ {
		files = append(files, fmt.Sprintf("data/part-%03d.txt", i))
	}
	writeTree(t, home, append(files, "src/a.txt")...)
	m := openExcludeManager(t, home)
	turn := m.NewTurn("session-1", "slow")

	savedBudget := WholeSnapshotBudget
	WholeSnapshotBudget = 300 * time.Millisecond
	started := make(chan struct{})
	var once sync.Once
	setSnapshotScanHook(func(string) {
		once.Do(func() { close(started) })
		// ~4 s for the whole tree; stays active while the other agent writes
		// (its scoped capture visits one path).
		time.Sleep(10 * time.Millisecond)
	})
	defer func() { WholeSnapshotBudget = savedBudget; setSnapshotScanHook(nil) }()

	type result struct {
		notice string
		err    error
		took   time.Duration
	}
	cmd := make(chan result, 1)
	go func() {
		begin := time.Now()
		notice, err := turn.ensureBeforeForToolNotice(context.Background(), "ctx_execute", []byte(`{"command":["zig","build"]}`))
		cmd <- result{notice, err, time.Since(begin)}
	}()
	<-started
	begin := time.Now()
	_, err := turn.ensureBeforeForToolNotice(context.Background(), "create_file", []byte(`{"path":"src/new.txt","content":"x"}`))
	writeWait := time.Since(begin)
	if err != nil {
		t.Fatal(err)
	}
	if writeWait > 3*time.Second {
		t.Fatalf("another agent's write waited %s behind a slow snapshot", writeWait)
	}
	r := <-cmd
	if r.err != nil {
		t.Fatalf("command blocked by its own checkpoint: %v", r.err)
	}
	if !strings.Contains(r.notice, "UWAGA") && !strings.Contains(r.notice, "ograniczony") {
		t.Fatalf("no visible note for the skipped snapshot: %q", r.notice)
	}
	t.Logf("write waited %s; command checkpoint took %s; note: %s", writeWait, r.took, r.notice)
}
