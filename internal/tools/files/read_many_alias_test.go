package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManyWorkspaceAliasKeepsRelativeGlobReferences(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(home, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "src", "a.go"), []byte("needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res, err := NewReadMany(alias).execute(context.Background(), []byte(`{"reads":"src/*.go:1-1"}`))
	for _, want := range []string{"== [1] src/a.go:1-1 ==", "needle", "[read_many: 1 ok, 0 failed]"} {
		if err != nil || res.Err != nil || !strings.Contains(res.Text, want) {
			t.Fatalf("want %q: %+v %v", want, res, err)
		}
	}
	if strings.Contains(res.Text, root) || strings.Contains(res.Text, "../") {
		t.Fatalf("leaked alias-dependent reference: %s", res.Text)
	}
}
