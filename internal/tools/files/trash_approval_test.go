package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	core "supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

func TestTrashOutsideWorkspaceNeedsApprovalInsideDoesNot(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "work")
	outside := filepath.Join(root, "elsewhere.txt")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(home, "inside.txt"), outside} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prev := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true)
	defer sandbox.SetUnsandboxed(prev)
	core.SetDefaultApprover(nil)

	r := core.NewRegistry()
	r.MustRegister(NewTrash(home).Spec())
	res, _ := r.Execute(context.Background(), "trash", json.RawMessage(`{"path":"inside.txt"}`))
	if res.Err != nil {
		t.Fatalf("workspace trash must stay ungated: %v", res.Err)
	}
	raw, _ := json.Marshal(map[string]string{"path": outside})
	res, _ = r.Execute(context.Background(), "trash", raw)
	if res.Err == nil {
		t.Fatal("trash outside the workspace ran without approval")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
}
