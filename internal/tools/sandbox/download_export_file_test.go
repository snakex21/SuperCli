package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadExactFileGrantDoesNotOpenParentSiblingsOrChildren(t *testing.T) {
	prior := IsUnsandboxed()
	SetUnsandboxed(false)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root := t.TempDir()
	workspace, file := filepath.Join(root, "project"), filepath.Join(root, "export", "nested", "one.gif")
	ctx := WithDownloadExportTargets(context.Background(), nil, []string{file})
	full, err := ResolveDownloadDestination(ctx, workspace, file)
	if err != nil || filepath.Base(full) != "one.gif" {
		t.Fatalf("exact new file rejected: %q %v", full, err)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if again, err := ResolveDownloadDestination(ctx, workspace, file); err != nil || again != full {
		t.Fatalf("parent creation changed exact-file grant: %q %v", again, err)
	}
	for _, denied := range []string{
		filepath.Dir(file), filepath.Join(filepath.Dir(file), "two.gif"),
		filepath.Join(file, "child.gif"), filepath.Join(filepath.Dir(file), "..", "escape.gif"),
		filepath.Join(root, "other", "one.gif"),
	} {
		if got, err := ResolveDownloadDestination(ctx, workspace, denied); !errors.Is(err, ErrEscape) || got != "" {
			t.Errorf("unrequested target allowed: %q => %q %v", denied, got, err)
		}
	}
	if _, err := ResolveDownloadDestination(WithDownloadExportDirs(ctx), workspace, file); !errors.Is(err, ErrEscape) {
		t.Fatal("fresh scope retained exact file", err)
	}
	child, cancel := context.WithCancel(ctx)
	if _, err := ResolveDownloadDestination(child, workspace, file); err != nil {
		t.Fatal("child lost exact parent grant", err)
	}
	cancel()
	if _, err := ResolveDownloadDestination(child, workspace, file); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled exact grant resolved", err)
	}
	if _, err := ResolveSafe(workspace, file); !errors.Is(err, ErrEscape) {
		t.Fatal("exact download grant opened other filesystem tools", err)
	}
}

func TestDownloadExactFileGrantPinsJunctionAncestorAndCanonicalTarget(t *testing.T) {
	prior := IsUnsandboxed()
	SetUnsandboxed(false)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root := t.TempDir()
	workspace, original, other, alias := filepath.Join(root, "project"), filepath.Join(root, "original"), filepath.Join(root, "other"), filepath.Join(root, "alias")
	for _, dir := range []string{original, other} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(original, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	file := filepath.Join(alias, "one.gif")
	ctx := WithDownloadExportTargets(context.Background(), nil, []string{file})
	full, err := ResolveDownloadDestination(ctx, workspace, file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveDownloadDestination(ctx, workspace, full); err != nil || got != full {
		t.Fatalf("canonical exact target refused: %q %v", got, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, full} {
		if _, err := ResolveDownloadDestination(ctx, workspace, path); !errors.Is(err, ErrEscape) {
			t.Fatalf("changed file ancestor accepted %q: %v", path, err)
		}
	}
}
