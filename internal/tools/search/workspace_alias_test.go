package search

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchWorkspaceAliasKeepsRelativeReferences(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	file := writeSearchFixture(t, home, "src/example.go", "needle\n")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("PATH", t.TempDir()) // exercise the fallback, independent of installed rg
	tool := NewSearchCode(alias)
	for _, tc := range []struct{ name, args, want string }{
		{"locations", `{"query":"needle","context":0}`, "src/example.go:1:needle"},
		{"narrow root", `{"query":"needle","path":"src","context":0}`, "src/example.go:1:needle"},
		{"context", `{"query":"needle","context":1}`, "== src/example.go:1-1 =="},
		{"discovery", `{"include":"**/*.go"}`, "src/example.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.run(context.Background(), json.RawMessage(tc.args))
			if err != nil || result.Err != nil || !strings.Contains(result.Text, tc.want) || strings.Contains(result.Text, root) {
				t.Fatalf("expected workspace-relative reference %q: %+v %v", tc.want, result, err)
			}
		})
	}
	if tool.WorkDir != alias {
		t.Fatalf("shared tool mutated: %q", tool.WorkDir)
	}
	got, err := os.ReadFile(filepath.Join(alias, "src/example.go"))
	want, wantErr := os.ReadFile(file)
	if err != nil || wantErr != nil || string(got) != string(want) {
		t.Fatal("relative reference does not round-trip")
	}
}
