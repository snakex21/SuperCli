package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/agent"
)

func TestRenderTreeChangesListsNewModifiedDeleted(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.go", "package a\n")
	write("edit.go", "package a\nfunc A() {}\n")
	write("gone.go", "package a\n")
	write(".git/HEAD", "ref\n")
	before := agent.SnapshotTree(root)
	if _, ok := before[".git/HEAD"]; ok {
		t.Fatal("dot directories must be skipped")
	}

	write("edit.go", "package a\nfunc A() int { return 1 }\n")
	write("sub/new.go", "package sub\n")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	got := agent.RenderTreeChanges(root, before)
	for _, want := range []string{"### edit.go (modified", "return 1", "### sub/new.go (new", "### gone.go (deleted)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("changes missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "keep.go") {
		t.Fatalf("unchanged file listed:\n%s", got)
	}
}

func TestReviewTakeoverPromptNamesReason(t *testing.T) {
	p := reviewTakeoverPrompt("fix bug", "handoff", "", "### a.go (modified, full content)\n")
	if !strings.Contains(p, "ran out of time") || !strings.Contains(p, "fix bug") || !strings.Contains(p, "a.go") {
		t.Fatalf("unexpected prompt:\n%s", p)
	}
}
