package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runFileOps(t *testing.T, tool *FileOpsTool, args string) Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("file_ops error: %v", err)
	}
	return res
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileOps_MoveAndRename(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello")
	tool := NewFileOps(dir)

	runFileOps(t, tool, `{"action":"rename","path":"a.txt","dest":"b.txt"}`)
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Fatal("source still present after rename")
	}
}

func TestFileOps_MoveIntoExistingFolder(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "hello")
	os.Mkdir(filepath.Join(dir, "archive"), 0o755)
	tool := NewFileOps(dir)
	runFileOps(t, tool, `{"action":"move","path":"a.txt","dest":"archive"}`)
	if _, err := os.Stat(filepath.Join(dir, "archive", "a.txt")); err != nil {
		t.Fatalf("move-into-folder failed: %v", err)
	}
}

func TestFileOps_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "A")
	mustWrite(t, filepath.Join(dir, "b.txt"), "B")
	tool := NewFileOps(dir)
	for _, action := range []string{"move", "copy", "rename"} {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"`+action+`","path":"a.txt","dest":"b.txt"}`))
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("%s: want overwrite refusal, got %v", action, err)
		}
	}
	// Both files untouched.
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "B" {
		t.Fatal("destination was modified")
	}
}

func TestFileOps_CopyFileAndFolder(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "src", "x.txt"), "x")
	mustWrite(t, filepath.Join(dir, "src", "sub", "y.txt"), "y")
	tool := NewFileOps(dir)
	runFileOps(t, tool, `{"action":"copy","path":"src","dest":"dst"}`)
	for _, p := range []string{"dst/x.txt", "dst/sub/y.txt", "src/x.txt"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func TestFileOps_CreateFolder(t *testing.T) {
	dir := t.TempDir()
	tool := NewFileOps(dir)
	runFileOps(t, tool, `{"action":"create_folder","path":"reports/2026"}`)
	info, err := os.Stat(filepath.Join(dir, "reports", "2026"))
	if err != nil || !info.IsDir() {
		t.Fatalf("create_folder failed: %v", err)
	}
	// Idempotent: existing folder is not an error.
	res := runFileOps(t, tool, `{"action":"create_folder","path":"reports/2026"}`)
	if !strings.Contains(res.Text, "already exists") {
		t.Fatalf("unexpected: %q", res.Text)
	}
}

func TestFileOps_List(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "aa")
	mustWrite(t, filepath.Join(dir, "sub", "b.txt"), "bb")
	tool := NewFileOps(dir)

	res := runFileOps(t, tool, `{"action":"list","path":"."}`)
	if !strings.Contains(res.Text, "a.txt") || strings.Contains(res.Text, "b.txt") {
		t.Fatalf("non-recursive list wrong: %q", res.Text)
	}
	res = runFileOps(t, tool, `{"action":"list","path":".","recursive":true}`)
	if !strings.Contains(res.Text, "b.txt") {
		t.Fatalf("recursive list wrong: %q", res.Text)
	}
}

func TestFileOps_TrashIsRecoverable(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "old.txt"), "precious")
	tool := NewFileOps(dir)
	tool.Now = func() time.Time { return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) }

	res := runFileOps(t, tool, `{"action":"trash","path":"old.txt"}`)
	if !strings.Contains(res.Text, "trash folder") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	trashed := filepath.Join(dir, ".supercli", "trash", "20260610-120000_old.txt")
	b, err := os.ReadFile(trashed)
	if err != nil || string(b) != "precious" {
		t.Fatalf("trashed file missing/corrupt: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); err == nil {
		t.Fatal("original still present after trash")
	}
}

func TestFileOps_TrashNameCollision(t *testing.T) {
	dir := t.TempDir()
	tool := NewFileOps(dir)
	tool.Now = func() time.Time { return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) }
	mustWrite(t, filepath.Join(dir, "x.txt"), "1")
	runFileOps(t, tool, `{"action":"trash","path":"x.txt"}`)
	mustWrite(t, filepath.Join(dir, "x.txt"), "2")
	runFileOps(t, tool, `{"action":"trash","path":"x.txt"}`)
	entries, _ := os.ReadDir(filepath.Join(dir, ".supercli", "trash"))
	if len(entries) != 2 {
		t.Fatalf("want 2 trashed files, got %d", len(entries))
	}
}

func TestFileOps_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewFileOps(dir)
	for _, args := range []string{
		`{"action":"list","path":".."}`,
		`{"action":"trash","path":"../../something"}`,
		`{"action":"move","path":"a","dest":"../../b"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("want sandbox error for %s", args)
		}
	}
}

func TestFileOps_NoDeleteAction(t *testing.T) {
	tool := NewFileOps(t.TempDir())
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"delete","path":"a"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("delete must not exist: %v", err)
	}
	if strings.Contains(tool.Spec().Schema, `"delete"`) {
		t.Fatal("schema must not advertise a delete action")
	}
}

func TestFileOps_Spec(t *testing.T) {
	spec := NewFileOps(".").Spec()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Name != "file_ops" {
		t.Fatalf("name: %q", spec.Name)
	}
}
