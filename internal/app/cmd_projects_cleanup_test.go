package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

func projectCommandCleanupFixture(t *testing.T, data string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	home := t.TempDir()
	if _, err := projectsAdd(data, home); err != nil {
		t.Fatal(err)
	}
	m, err := checkpoint.Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("command-cleanup", "write fixture")
	wrapped := turn.Wrap(tools.NewWriteFile(home).Spec())
	args, _ := json.Marshal(map[string]string{"path": "keep.txt", "content": "current project file"})
	res, err := wrapped.Fn(context.Background(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("write fixture: %+v %v", res, err)
	}
	if record, err := turn.Complete(context.Background()); err != nil || record == nil {
		t.Fatalf("checkpoint: %+v %v", record, err)
	}
	return home
}

func TestProjectsCheckpointCommandsScopedClearAndPolicyOverrides(t *testing.T) {
	ctx, data := context.Background(), t.TempDir()
	home, other := projectCommandCleanupFixture(t, data), projectCommandCleanupFixture(t, data)
	otherBefore, err := checkpoint.PreviewWorkspaceCleanup(ctx, other, data)
	if err != nil || otherBefore.Bytes == 0 {
		t.Fatalf("other preview: %+v %v", otherBefore, err)
	}
	if text, err := projectsCommand(ctx, "checkpoints \""+home+"\"", data); err != nil || !strings.Contains(text, "--clear") {
		t.Fatalf("preview: %q %v", text, err)
	}
	if text, err := projectsCommand(ctx, "checkpoints \""+home+"\" --clear", data); err != nil || !strings.Contains(text, "Cleared") {
		t.Fatalf("clear: %q %v", text, err)
	}
	if _, _, ok := resolveProject(memory.LoadProjectsMap(data), home); !ok {
		t.Fatal("manual clear unregistered project")
	}
	if contents, err := os.ReadFile(filepath.Join(home, "keep.txt")); err != nil || string(contents) != "current project file" {
		t.Fatalf("project file changed: %q %v", contents, err)
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(ctx, home, data); err != nil || after.Bytes != 0 {
		t.Fatalf("cleared preview: %+v %v", after, err)
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(ctx, other, data); err != nil || after != otherBefore {
		t.Fatalf("unrelated history changed: %+v %v", after, err)
	}
	if _, err := projectsCommand(ctx, "cleanup-policy always", data); err != nil {
		t.Fatal(err)
	}
	if enabled, err := memory.LoadProjectCheckpointCleanup(data); err != nil || !enabled {
		t.Fatalf("shared policy: %v %v", enabled, err)
	}
	if _, err := projectsCommand(ctx, "remove \""+other+"\" --keep-checkpoints", data); err != nil {
		t.Fatal(err)
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(ctx, other, data); err != nil || after != otherBefore {
		t.Fatalf("one-off keep removed history: %+v %v", after, err)
	}
	if _, err := projectsAdd(data, other); err != nil {
		t.Fatal(err)
	}
	if _, err := projectsCommand(ctx, "remove \""+other+"\"", data); err != nil {
		t.Fatal(err)
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(ctx, other, data); err != nil || after.Bytes != 0 {
		t.Fatalf("saved policy did not clear: %+v %v", after, err)
	}
	if _, _, ok := resolveProject(memory.LoadProjectsMap(data), other); ok {
		t.Fatal("removed project still registered")
	}
	if _, err := os.Stat(filepath.Join(data, "projects", memory.ProjectKey(other), "memory.db")); err != nil {
		t.Fatalf("memory removed: %v", err)
	}
}

func TestProjectsCheckpointCanceledRemovalKeepsRegistration(t *testing.T) {
	data := t.TempDir()
	home := projectCommandCleanupFixture(t, data)
	before, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, data)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := projectsCommand(ctx, "remove \""+home+"\" --checkpoints", data); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal: %v", err)
	}
	if _, _, ok := resolveProject(memory.LoadProjectsMap(data), home); !ok {
		t.Fatal("canceled removal unregistered project")
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, data); err != nil || after != before {
		t.Fatalf("canceled removal changed history: %+v %v", after, err)
	}
}
