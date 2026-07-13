package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestReadLinesExternalPathRequiresAllowAll(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "shared", "note.txt")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	tool := NewReadLines(home).Spec()
	args := []byte(`{"file":"` + filepath.ToSlash(outside) + `","from":1,"to":1}`)
	denied, _ := tool.Fn(context.Background(), args)
	if denied.Err == nil {
		t.Fatal("external read allowed while sandboxed")
	}
	sandbox.SetUnsandboxed(true)
	allowed, _ := tool.Fn(context.Background(), args)
	if allowed.Err != nil || !strings.Contains(allowed.Text, "external") {
		t.Fatalf("allowed=%+v", allowed)
	}
}
