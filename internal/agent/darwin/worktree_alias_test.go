package darwin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeManagerHasGitRecognizesDirectoryAlias(t *testing.T) {
	requireGit(t)
	home := makeRepo(t)
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manager := NewWorktreeManager(alias)
	if !manager.HasGit() {
		t.Fatal("repository root alias was not recognized")
	}
	if manager.Base() != alias {
		t.Fatalf("public base changed: %q", manager.Base())
	}
	child := filepath.Join(alias, "nested")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if NewWorktreeManager(child).HasGit() {
		t.Fatal("a descendant is not the repository root")
	}
}
