package search

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundedWalkStopsAndSharesExclusions(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src/a.go", "src/b.go", ".tmp/hidden.go", "node_modules/hidden.go", ".git/hidden.go"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	complete, err := WalkFileEntriesBounded(context.Background(), root, 100, func(path string, d fs.DirEntry) error {
		rel, _ := filepath.Rel(root, path)
		seen[filepath.ToSlash(rel)] = true
		if info, err := d.Info(); err != nil || info.Size() != 1 {
			t.Fatalf("metadata: %v %v", info, err)
		}
		return nil
	})
	if err != nil || !complete || len(seen) != 2 || !seen["src/a.go"] || !seen["src/b.go"] {
		t.Fatalf("complete=%v seen=%v err=%v", complete, seen, err)
	}
	calls := 0
	complete, err = WalkFileEntriesBounded(context.Background(), root, 1, func(string, fs.DirEntry) error { calls++; return nil })
	if err != nil || complete || calls != 0 {
		t.Fatalf("entry budget must count directories: complete=%v calls=%d err=%v", complete, calls, err)
	}
}

func TestBoundedWalkCancellationAndWideDirectory(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 300; i++ {
		if err := os.WriteFile(filepath.Join(root, string(rune(0x400+i))+".txt"), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	complete, err := WalkFileEntriesBounded(context.Background(), root, 7, func(string, fs.DirEntry) error { calls++; return nil })
	if err != nil || complete || calls != 7 {
		t.Fatalf("complete=%v calls=%d err=%v", complete, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls = 0
	complete, err = WalkFileEntriesBounded(ctx, root, 1000, func(string, fs.DirEntry) error { calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || complete || calls != 1 {
		t.Fatalf("cancellation: complete=%v calls=%d err=%v", complete, calls, err)
	}
}
