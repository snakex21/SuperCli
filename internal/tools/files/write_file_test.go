package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runWriteFile(t *testing.T, tool *WriteFile, args string) Result {
	t.Helper()
	res, err := tool.execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("write_file go-error: %v", err)
	}
	return res
}

func TestWriteFile_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir)
	res := runWriteFile(t, tool, `{"path":"test.txt","content":"hello"}`)
	if res.Err != nil {
		t.Fatalf("unexpected tool error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "Created") {
		t.Errorf("text = %q, want 'Created'", res.Text)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestWriteFile_Overwrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteFile(dir)
	res := runWriteFile(t, tool, `{"path":"x.txt","content":"new"}`)
	if !strings.Contains(res.Text, "Overwrote") {
		t.Errorf("text = %q, want 'Overwrote'", res.Text)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x.txt"))
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
}

func TestWriteFile_NestedPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir)
	res := runWriteFile(t, tool, `{"path":"a/b/c.txt","content":"deep"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b", "c.txt")); err != nil {
		t.Errorf("nested file not created: %v", err)
	}
}

// TestWriteFile_SandboxEscape is the security test: the model must
// not be able to write outside the project home, even with an
// absolute path or .. traversal.
func TestWriteFile_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFile(dir)

	// Relative traversal.
	res := runWriteFile(t, tool, `{"path":"../escape.txt","content":"x"}`)
	if res.Err == nil {
		t.Error("traversal escape should be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.txt")); err == nil {
		t.Error("file was written outside home via traversal")
		os.Remove(filepath.Join(dir, "..", "escape.txt"))
	}

	// Absolute path outside home.
	outside := filepath.Join(t.TempDir(), "abs-escape.txt")
	escBytes, _ := json.Marshal(writeFileArgs{Path: outside, Content: "x"})
	res2, _ := tool.execute(context.Background(), escBytes)
	if res2.Err == nil {
		t.Error("absolute path outside home should be rejected")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("file was written outside home via absolute path")
	}
}

func TestWriteFile_BadJSON(t *testing.T) {
	tool := NewWriteFile(t.TempDir())
	res, _ := tool.execute(context.Background(), []byte("not json"))
	if res.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestWriteFile_EmptyPath(t *testing.T) {
	tool := NewWriteFile(t.TempDir())
	res := runWriteFile(t, tool, `{"path":"","content":"x"}`)
	if res.Err == nil {
		t.Error("expected error for empty path")
	}
}

func TestWriteFile_Spec(t *testing.T) {
	spec := NewWriteFile(".").Spec()
	if spec.Name != "write_file" {
		t.Errorf("Name = %q, want write_file", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
	if !strings.Contains(spec.Schema, "content") {
		t.Errorf("schema missing content field: %s", spec.Schema)
	}
}
