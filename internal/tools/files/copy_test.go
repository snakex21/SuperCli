package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func runCopy(t *testing.T, tool *Copy, args string) Result {
	t.Helper()
	res, err := tool.execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("copy go-error: %v", err)
	}
	return res
}

func TestCopyTool_File(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	tool := NewCopy(dir)
	res := runCopy(t, tool, `{"src":"a.txt","dest":"b.txt"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.Text != "Copied a.txt -> b.txt" {
		t.Fatalf("direct destination result changed: %q", res.Text)
	}
	// Both exist; source preserved (copy, not move).
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Error("source removed after copy")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(got) != "hello" {
		t.Errorf("copy content = %q, want hello", got)
	}
}

func TestCopyTool_FolderRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "sub", "f.txt"), []byte("x"), 0o644)
	tool := NewCopy(dir)
	res := runCopy(t, tool, `{"src":"src","dest":"dst"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "sub", "f.txt")); err != nil {
		t.Errorf("nested file not copied: %v", err)
	}
}

func TestCopyTool_IntoFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "backup"), 0o755)
	tool := NewCopy(dir)
	res := runCopy(t, tool, `{"src":"a.txt","dest":"backup"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.Text != "Copied a.txt -> backup/a.txt" {
		t.Fatalf("result lost actual copied path: %q", res.Text)
	}
	if _, err := os.Stat(filepath.Join(dir, "backup", "a.txt")); err != nil {
		t.Errorf("file not copied into folder: %v", err)
	}
}

func TestCopyTool_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	tool := NewCopy(dir)
	res := runCopy(t, tool, `{"src":"a.txt","dest":"b.txt"}`)
	if res.Err == nil {
		t.Error("copy onto existing file should be refused")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(got) != "b" {
		t.Errorf("destination overwritten: %q", got)
	}
}

func TestCopyTool_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	tool := NewCopy(dir)
	res := runCopy(t, tool, `{"src":"a.txt","dest":"../leak.txt"}`)
	if res.Err == nil {
		t.Error("escaping dest should be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "leak.txt")); err == nil {
		t.Error("file escaped home")
		os.Remove(filepath.Join(dir, "..", "leak.txt"))
	}
}

func TestCopyTool_MissingArgs(t *testing.T) {
	tool := NewCopy(t.TempDir())
	if res := runCopy(t, tool, `{"src":"a.txt"}`); res.Err == nil {
		t.Error("missing dest should error")
	}
	if res, _ := tool.execute(context.Background(), []byte("bad")); res.Err == nil {
		t.Error("bad JSON should error")
	}
}

func TestCopyTool_Spec(t *testing.T) {
	spec := NewCopy(".").Spec()
	if spec.Name != "copy" {
		t.Errorf("Name = %q, want copy", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func TestCopyTool_FolderIntoFolderReportsFinalPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "sub", "a.txt"), []byte("copied"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := runCopy(t, NewCopy(dir), `{"src":"src","dest":"backup/"}`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.Text != "Copied src -> backup/src" {
		t.Fatalf("result lost actual copied directory: %q", res.Text)
	}
	got, err := os.ReadFile(filepath.Join(dir, "backup", "src", "sub", "a.txt"))
	if err != nil || string(got) != "copied" {
		t.Fatalf("copy destination content = %q, error = %v", got, err)
	}
}
