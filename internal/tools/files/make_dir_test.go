package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMakeDir(t *testing.T, tool *MakeDir, args string) Result {
	t.Helper()
	res, err := tool.execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("make_dir go-error: %v", err)
	}
	return res
}

func TestMakeDirTool_Creates(t *testing.T) {
	dir := t.TempDir()
	tool := NewMakeDir(dir)
	res := runMakeDir(t, tool, `{"path":"reports/2026"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "Created folder") {
		t.Errorf("text = %q, want 'Created folder'", res.Text)
	}
	info, err := os.Stat(filepath.Join(dir, "reports", "2026"))
	if err != nil || !info.IsDir() {
		t.Errorf("nested folder not created: %v", err)
	}
}

func TestMakeDirTool_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	tool := NewMakeDir(dir)
	runMakeDir(t, tool, `{"path":"sub"}`)
	res := runMakeDir(t, tool, `{"path":"sub"}`)
	if res.Err != nil {
		t.Fatalf("idempotent create should not error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "already exists") {
		t.Errorf("text = %q, want 'already exists'", res.Text)
	}
}

func TestMakeDirTool_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewMakeDir(dir)
	res := runMakeDir(t, tool, `{"path":"../escape-dir"}`)
	if res.Err == nil {
		t.Error("traversal escape should be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape-dir")); err == nil {
		t.Error("folder created outside home via traversal")
		os.Remove(filepath.Join(dir, "..", "escape-dir"))
	}
}

func TestMakeDirTool_ExistingFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMakeDir(dir)
	res := runMakeDir(t, tool, `{"path":"x"}`)
	if res.Err == nil {
		t.Error("want error when path is an existing file")
	}
}

func TestMakeDirTool_BadJSONAndEmpty(t *testing.T) {
	tool := NewMakeDir(t.TempDir())
	if res, _ := tool.execute(context.Background(), []byte("nope")); res.Err == nil {
		t.Error("expected error for bad JSON")
	}
	if res := runMakeDir(t, tool, `{"path":""}`); res.Err == nil {
		t.Error("expected error for empty path")
	}
}

func TestMakeDirTool_Spec(t *testing.T) {
	spec := NewMakeDir(".").Spec()
	if spec.Name != "make_dir" {
		t.Errorf("Name = %q, want make_dir", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}
