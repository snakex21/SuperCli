package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func runMove(t *testing.T, tool *Move, args string) Result {
	t.Helper()
	res, err := tool.execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("move go-error: %v", err)
	}
	return res
}

func TestMoveTool_Rename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMove(dir)
	res := runMove(t, tool, `{"src":"a.txt","dest":"b.txt"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Error("original still exists after rename")
	}
}

func TestMoveTool_IntoFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(dir, "archive"), 0o755)
	tool := NewMove(dir)
	res := runMove(t, tool, `{"src":"a.txt","dest":"archive"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "a.txt")); err != nil {
		t.Errorf("file not moved into folder: %v", err)
	}
}

func TestMoveTool_Folder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "old", "sub"), 0o755)
	tool := NewMove(dir)
	res := runMove(t, tool, `{"src":"old","dest":"new"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "sub")); err != nil {
		t.Errorf("folder not moved with contents: %v", err)
	}
}

func TestMoveTool_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	tool := NewMove(dir)
	res := runMove(t, tool, `{"src":"a.txt","dest":"b.txt"}`)
	if res.Err == nil {
		t.Error("move onto existing file should be refused")
	}
	// b.txt must be untouched.
	got, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(got) != "b" {
		t.Errorf("destination overwritten: %q", got)
	}
}

func TestMoveTool_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	tool := NewMove(dir)

	// Escaping destination.
	res := runMove(t, tool, `{"src":"a.txt","dest":"../escaped.txt"}`)
	if res.Err == nil {
		t.Error("escaping dest should be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escaped.txt")); err == nil {
		t.Error("file escaped home")
		os.Remove(filepath.Join(dir, "..", "escaped.txt"))
	}

	// Escaping source.
	res2 := runMove(t, tool, `{"src":"../../etc/hosts","dest":"x.txt"}`)
	if res2.Err == nil {
		t.Error("escaping src should be rejected")
	}
}

func TestMoveTool_MissingArgs(t *testing.T) {
	tool := NewMove(t.TempDir())
	if res := runMove(t, tool, `{"src":"a.txt"}`); res.Err == nil {
		t.Error("missing dest should error")
	}
	if res, _ := tool.execute(context.Background(), []byte("bad")); res.Err == nil {
		t.Error("bad JSON should error")
	}
}

func TestMoveTool_Spec(t *testing.T) {
	spec := NewMove(".").Spec()
	if spec.Name != "move" {
		t.Errorf("Name = %q, want move", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}
