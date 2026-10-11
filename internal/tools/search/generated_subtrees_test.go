package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedSubtreesExcludedButExplicitRootsRemainSearchable(t *testing.T) {
	root := t.TempDir()
	names := []string{".tmp", "supercli-data", "supercli-data-preview", "dist", "bin", "node_modules", "%SystemDrive%"}
	for _, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "needle.txt"), []byte("needle\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var files []string
	if err := WalkFiles(root, func(path string) error { files = append(files, filepath.Base(path)); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("generated subtrees were scanned: %v", files)
	}
	for _, name := range names {
		files = nil
		if err := WalkFiles(filepath.Join(root, name), func(path string) error { files = append(files, filepath.Base(path)); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || files[0] != "needle.txt" {
			t.Fatalf("explicit %q root was hidden: %v", name, files)
		}
	}
}
