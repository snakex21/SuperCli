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

func runTrash(t *testing.T, tool *Trash, args string) Result {
	t.Helper()
	res, err := tool.execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("trash go-error: %v", err)
	}
	return res
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 14, 20, 30, 0, 0, time.UTC)
}

func TestTrashTool_MovesToTrash(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x"), 0o644)
	tool := NewTrash(dir)
	tool.Now = fixedNow
	res := runTrash(t, tool, `{"path":"old.txt"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	// Original gone from its place.
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); err == nil {
		t.Error("original still present after trash")
	}
	// Lives in trash folder, recoverable.
	trashEntry := filepath.Join(dir, ".supercli", "trash", "20260614-203000_old.txt")
	if _, err := os.Stat(trashEntry); err != nil {
		t.Errorf("trashed file not found at %s: %v", trashEntry, err)
	}
}

func TestTrashTool_Folder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "junk", "sub"), 0o755)
	tool := NewTrash(dir)
	tool.Now = fixedNow
	res := runTrash(t, tool, `{"path":"junk"}`)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk")); err == nil {
		t.Error("folder still present after trash")
	}
	if _, err := os.Stat(filepath.Join(dir, ".supercli", "trash", "20260614-203000_junk", "sub")); err != nil {
		t.Errorf("trashed folder contents missing: %v", err)
	}
}

func TestTrashTool_NameCollision(t *testing.T) {
	dir := t.TempDir()
	tool := NewTrash(dir)
	tool.Now = fixedNow
	// Trash two files with the same name in the same "second".
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("1"), 0o644)
	runTrash(t, tool, `{"path":"x.txt"}`)
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("2"), 0o644)
	res := runTrash(t, tool, `{"path":"x.txt"}`)
	if res.Err != nil {
		t.Fatalf("second trash failed: %v", res.Err)
	}
	// Both must survive in trash under distinct names.
	first := filepath.Join(dir, ".supercli", "trash", "20260614-203000_x.txt")
	second := filepath.Join(dir, ".supercli", "trash", "20260614-203000-1_x.txt")
	if _, err := os.Stat(first); err != nil {
		t.Errorf("first trashed file missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("collision-renamed file missing: %v", err)
	}
}

func TestTrashTool_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewTrash(dir)
	res := runTrash(t, tool, `{"path":"../../etc/hosts"}`)
	if res.Err == nil {
		t.Error("escaping path should be rejected")
	}
}

func TestTrashTool_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewTrash(dir)
	res := runTrash(t, tool, `{"path":"nope.txt"}`)
	if res.Err == nil {
		t.Error("trashing a missing file should error")
	}
}

func TestTrashTool_BadArgs(t *testing.T) {
	tool := NewTrash(t.TempDir())
	if res, _ := tool.execute(context.Background(), []byte("bad")); res.Err == nil {
		t.Error("bad JSON should error")
	}
	if res := runTrash(t, tool, `{"path":""}`); res.Err == nil {
		t.Error("empty path should error")
	}
}

func TestTrashTool_Spec(t *testing.T) {
	spec := NewTrash(".").Spec()
	if spec.Name != "trash" {
		t.Errorf("Name = %q, want trash", spec.Name)
	}
	if !strings.Contains(spec.Description, "recoverable") {
		t.Errorf("description should stress recoverable: %q", spec.Description)
	}
}
