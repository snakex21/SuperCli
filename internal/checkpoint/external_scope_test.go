package checkpoint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tools "supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

func TestFullAccessExternalEditsHaveSeparatePortableCheckpoint(t *testing.T) {
	old := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true)
	defer sandbox.SetUnsandboxed(old)
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	outside := filepath.Join(root, "other")
	for _, dir := range []string{workspace, outside} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := filepath.Join(root, "app-data")
	m, err := Open(workspace, data)
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("session", "external edit")
	t.Cleanup(func() {
		if _, err := turn.Complete(context.Background()); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(outside, "zażółć.go")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	wrapped := turn.Wrap(tools.Tool{Name: "patch_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		calls++
		return tools.Result{Text: "Patched"}, os.WriteFile(path, []byte("after"), 0600)
	}})
	raw, _ := json.Marshal(map[string]string{"path": path})
	r, err := wrapped.Fn(context.Background(), raw)
	if err != nil || r.Err != nil || calls != 1 || !strings.Contains(r.Text, "separate scoped checkpoint") {
		t.Fatalf("external edit refused: %+v %v calls=%d", r, err, calls)
	}
	if _, err := turn.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Latest("session") != nil {
		t.Fatal("external changes attributed to active workspace undo")
	}
	external, err := Open(outside, data)
	if err != nil {
		t.Fatal(err)
	}
	record := external.Latest("session")
	if record == nil || len(record.Files) != 1 || record.Files[0] != "zażółć.go" || !strings.HasPrefix(external.repo, data) {
		t.Fatalf("external checkpoint missing: %+v", record)
	}
	if _, err := external.Undo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "before" {
		t.Fatal("external before bytes not restorable")
	}
	if _, err := external.Redo(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	if string(content) != "after" {
		t.Fatal("external after bytes not restorable")
	}
	sandbox.SetUnsandboxed(false)
	blockedTurn := m.NewTurn("blocked", "no permission")
	t.Cleanup(func() {
		if _, err := blockedTurn.Complete(context.Background()); err != nil {
			t.Error(err)
		}
	})
	blocked := blockedTurn.Wrap(wrapped)
	r, _ = blocked.Fn(context.Background(), raw)
	if r.Err == nil || calls != 1 {
		t.Fatal("sandbox-off boundary bypassed")
	}
}

func TestExternalCaptureFailureStopsActualTool(t *testing.T) {
	old := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true)
	defer sandbox.SetUnsandboxed(old)
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	outside := filepath.Join(root, "other")
	for _, dir := range []string{workspace, outside} {
		os.MkdirAll(dir, 0700)
	}
	m, err := Open(workspace, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("session", "too large")
	t.Cleanup(func() {
		if _, err := turn.Complete(context.Background()); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(outside, "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSnapshotFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	called := false
	wrapped := turn.Wrap(tools.Tool{Name: "write_file", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		called = true
		return tools.Result{}, nil
	}})
	raw, _ := json.Marshal(map[string]string{"path": path})
	r, _ := wrapped.Fn(context.Background(), raw)
	if r.Err == nil || called {
		t.Fatal("mutation ran without a successful external capture")
	}
}
