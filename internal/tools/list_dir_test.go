package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runListDir(t *testing.T, tool *ListDirTool, args string) Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("list_dir error: %v", err)
	}
	return res
}

func TestListDir_FileAndSubdir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewListDir(dir)

	// Explicit path.
	res := runListDir(t, tool, `{"path":"."}`)
	if !strings.Contains(res.Text, "a.txt (5 bytes)") {
		t.Fatalf("expected file with size, got: %q", res.Text)
	}
	if !strings.Contains(res.Text, "sub/") {
		t.Fatalf("expected subfolder marked with '/', got: %q", res.Text)
	}

	// Empty path defaults to the working directory (BaseDir).
	res = runListDir(t, tool, `{}`)
	if !strings.Contains(res.Text, "a.txt (5 bytes)") || !strings.Contains(res.Text, "sub/") {
		t.Fatalf("empty-path listing did not list BaseDir, got: %q", res.Text)
	}
}

func TestListDir_Empty(t *testing.T) {
	dir := t.TempDir()
	res := runListDir(t, NewListDir(dir), `{}`)
	if !strings.Contains(res.Text, "is empty") {
		t.Fatalf("expected empty notice, got: %q", res.Text)
	}
}
