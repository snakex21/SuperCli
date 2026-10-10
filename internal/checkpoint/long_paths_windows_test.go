//go:build windows

package checkpoint

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointLongPathsWindows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	// The private repository fits the native Git init limit, while generated
	// record refs and their locks exceed it. This reproduces the actual failure
	// independently of the length of the runner's temporary directory.
	const repoLength = 195
	repoSuffix := filepath.Join("checkpoints", strings.Repeat("0", 16), "objects.git")
	padding := repoLength - len(root) - len(repoSuffix) - 2
	if padding < 1 || padding > 255 {
		t.Fatalf("temporary root cannot construct the long-ref fixture: %d bytes", len(root))
	}
	data := filepath.Join(root, strings.Repeat("d", padding))
	source := filepath.Join(home, "source.txt")
	writeCheckpointFixture(t, source, "before")
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(m.repo) != repoLength || len(filepath.Join(m.repo, filepath.FromSlash(recordRefRoot(strings.Repeat("0", 32))), "before.lock")) <= 260 {
		t.Fatalf("fixture does not exercise long private refs: %d", len(m.repo))
	}
	turn := m.NewTurn("long-path", "edit source")
	if err := turn.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	// A portable checkpoint must work independently of a user's Git setting.
	// Keep the override local to each command, without persisting it in this repo.
	if _, err := m.git(ctx, "config", "--local", "core.longpaths", "false"); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, source, "after")
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("complete long-path checkpoint: %+v, %v", record, err)
	}
	if _, err := m.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, source, []byte("before"))
	if _, err := m.Redo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	assertFileBytes(t, source, []byte("after"))
	refs, err := retentionGitCommand(ctx, m.repo, "for-each-ref", "--format=%(refname)").Output()
	if err != nil {
		t.Fatalf("retention long-path inventory: %v", err)
	}
	for _, side := range []string{"before", "after"} {
		if !strings.Contains(string(refs), recordRefRoot(record.ID)+"/"+side) {
			t.Fatalf("retention inventory lost %s record ref", side)
		}
	}
	configured, err := m.git(ctx, "config", "--local", "--get", "core.longpaths")
	if err != nil || strings.TrimSpace(configured) != "false" {
		t.Fatalf("command changed persistent Git configuration: %q, %v", configured, err)
	}
}
