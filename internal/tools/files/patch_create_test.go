package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchFileTool_Basic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nconst X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewPatchFile(dir)
	args, _ := json.Marshal(map[string]any{
		"path": "a.go",
		"changes": []map[string]any{
			{"old": "const X = 1", "new": "const X = 2"},
		},
	})
	res, err := tool.Spec().Fn(context.Background(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Text, "replacements=1") || !strings.Contains(res.Text, "changed=true") {
		t.Fatalf("text=%q", res.Text)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(got), "const X = 2") {
		t.Fatalf("%q", got)
	}
}

func TestPatchFileTool_SchemaForbidsAdditional(t *testing.T) {
	spec := NewPatchFile(".").Spec()
	if !strings.Contains(spec.Schema, `"additionalProperties": false`) {
		t.Fatal("schema should forbid additional properties")
	}
	if !strings.Contains(spec.Schema, `"changes"`) {
		t.Fatal("schema missing changes")
	}
}

func TestCreateFileTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewCreateFile(dir)
	args, _ := json.Marshal(map[string]any{"path": "n/e/w.txt", "content": "hello"})
	res, err := tool.Spec().Fn(context.Background(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "n", "e", "w.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q err=%v", got, err)
	}
	res, _ = tool.Spec().Fn(context.Background(), args)
	if res.Err == nil {
		t.Fatal("expected refuse overwrite")
	}
	got, _ = os.ReadFile(filepath.Join(dir, "n", "e", "w.txt"))
	if string(got) != "hello" {
		t.Fatalf("changed: %q", got)
	}
	if !strings.Contains(tool.Spec().Schema, `"additionalProperties": false`) {
		t.Fatal("schema should forbid additional properties")
	}
}
