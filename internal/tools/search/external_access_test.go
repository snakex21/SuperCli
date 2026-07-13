package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestSearchCodeExternalRootRequiresAllowAll(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "shared")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "x.go"), []byte("package x // needle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	tool := NewSearchCode(home).Spec()
	args := []byte(`{"query":"needle","path":"` + filepath.ToSlash(outside) + `"}`)
	denied, _ := tool.Fn(context.Background(), args)
	if denied.Err == nil {
		t.Fatal("external search allowed while sandboxed")
	}
	sandbox.SetUnsandboxed(true)
	allowed, _ := tool.Fn(context.Background(), args)
	if allowed.Err != nil || allowed.Text == "no matches" {
		t.Fatalf("allowed=%+v", allowed)
	}
}
