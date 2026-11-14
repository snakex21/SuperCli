package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLayoutFixture(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLayoutListsSmallProjectGroupedByFolder(t *testing.T) {
	root := t.TempDir()
	writeLayoutFixture(t, root, "go.mod", "main.go", "pkg/a/a.go", "pkg/a/a_test.go", "node_modules/x/index.js")
	plain := Build(root, Options{LookPath: noGit, Now: time.Unix(1, 0)})
	got := Build(root, Options{LookPath: noGit, Now: time.Unix(1, 0), Layout: true})
	if !strings.HasPrefix(got, plain) {
		t.Fatalf("layout must only append to the repo block:\n%s", got)
	}
	for _, want := range []string{"files (4):", "./: go.mod main.go", "pkg/a/: a.go a_test.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "node_modules") {
		t.Fatalf("layout must skip dependency folders:\n%s", got)
	}
	if strings.Contains(Build(root, Options{LookPath: noGit, Now: time.Unix(1, 0)}), "files (") {
		t.Fatal("layout is opt-in")
	}
}

func TestLayoutSummarisesLargerProjectWithinBudget(t *testing.T) {
	root := t.TempDir()
	var files []string
	for d := 0; d < 20; d++ {
		for f := 0; f < 5+d; f++ {
			files = append(files, fmt.Sprintf("dir%02d/file%02d.go", d, f))
		}
	}
	writeLayoutFixture(t, root, append(files, "README.md")...)
	got := appendLayout(t.Context(), "", root)
	if !strings.Contains(got, "layout (") || !strings.Contains(got, "./: README.md") || !strings.Contains(got, "dir19/ (24 files)") || !strings.Contains(got, "more folders") {
		t.Fatalf("unexpected summary:\n%s", got)
	}
	section := got[strings.Index(got, "layout ("):]
	if EstimateTokens(section) > LayoutBudget {
		t.Fatalf("layout over budget: %d", EstimateTokens(section))
	}
}
