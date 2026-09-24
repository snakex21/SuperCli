package search

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchSkipsAgentWorktreesButKeepsConfiguration(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, ".claude/worktrees/agent/src/stale.go", strings.Repeat("needle OLD\n", 25))
	writeSearchFixture(t, dir, ".claude/settings.md", "needle SETTINGS\n")
	writeSearchFixture(t, dir, "src/current.go", "needle CURRENT\n")
	writeSearchFixture(t, dir, "docs/worktrees/guide.md", "needle GUIDE\n")
	tool := NewSearchCode(dir)
	for _, backend := range []string{"fallback", "rg"} {
		t.Run(backend, func(t *testing.T) {
			rg := os.Getenv("SUPERCLI_TEST_RG")
			if backend == "rg" && rg == "" {
				t.Skip("set SUPERCLI_TEST_RG")
			}
			// An include glob also searches hidden configuration with real ripgrep.
			g, _ := compileSearchGlob("**/*")
			var r Result
			var err error
			if backend == "rg" {
				r, err = tool.ripgrep(context.Background(), rg, dir, "needle", 5, &searchContext{include: g})
			} else {
				r, err = tool.fallback(context.Background(), dir, "needle", 5, &searchContext{include: g})
			}
			if err != nil || r.Err != nil || strings.Contains(r.Text, "OLD") || strings.Contains(r.Text, "limit reached") {
				t.Fatalf("%+v %v", r, err)
			}
			for _, expected := range []string{"CURRENT", "SETTINGS", "GUIDE"} {
				if !strings.Contains(r.Text, expected) {
					t.Fatalf("lost %s: %s", expected, r.Text)
				}
			}
			// Explicit roots at and below the excluded directory remain searchable.
			for _, rel := range []string{".claude/worktrees", ".claude/worktrees/agent", ".claude/worktrees/agent/src/stale.go"} {
				root := filepath.Join(dir, filepath.FromSlash(rel))
				if backend == "rg" {
					r, err = tool.ripgrep(context.Background(), rg, root, "needle", 50, &searchContext{include: g})
				} else {
					r, err = tool.fallback(context.Background(), root, "needle", 50, &searchContext{include: g})
				}
				if err != nil || r.Err != nil || !strings.Contains(r.Text, "OLD") {
					t.Fatalf("explicit root %s: %+v %v", rel, r, err)
				}
			}
		})
	}
}

func TestWorktreePolicySharedByFileLookupAndWorkspaceSample(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, ".claude/worktrees/agent/stale.go", "stale\n")
	writeSearchFixture(t, dir, ".claude/settings.md", "settings\n")
	writeSearchFixture(t, dir, "src/main.go", "current\n")
	g, _ := compileSearchGlob("*.go")
	r, err := NewSearchCode(dir).findFiles(context.Background(), dir, g, 10)
	if err != nil || r.Err != nil || r.Text != "src/main.go" {
		t.Fatalf("%+v %v", r, err)
	}
	var names []string
	complete, err := WalkFileEntriesBounded(context.Background(), dir, 20, func(path string, _ fs.DirEntry) error {
		names = append(names, filepath.Base(path))
		return nil
	})
	if err != nil || !complete || strings.Contains(strings.Join(names, ","), "stale") || len(names) != 2 {
		t.Fatalf("sample=%v complete=%v err=%v", names, complete, err)
	}
}

func TestWorktreeFilterRelativeToExplicitRoot(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		root, path string
		skip       bool
	}{
		{".", ".claude/worktrees/a/src.go", true},
		{".", "sub/.ClAuDe/WoRkTrEeS/a/src.go", true},
		{".claude", ".claude/worktrees/a/src.go", true},
		{".claude/worktrees", ".claude/worktrees/a/src.go", false},
		{".claude/worktrees/a", ".claude/worktrees/a/src.go", false},
		{".claude/worktrees/a", ".claude/worktrees/a/sub/.claude/worktrees/b/src.go", true},
		{".", ".claude/skills/SKILL.md", false},
		{".", "docs/worktrees/guide.md", false},
	} {
		if got := searchPathIsSkipped(filepath.Join(dir, tc.root), filepath.Join(dir, tc.path)); got != tc.skip {
			t.Errorf("%+v: skip=%v", tc, got)
		}
	}
}

func BenchmarkSearchWithAgentWorktrees(b *testing.B) {
	dir := b.TempDir()
	for worker := range 32 {
		for file := range 8 {
			writeSearchFixture(b, dir, fmt.Sprintf(".claude/worktrees/agent%02d/src/f%02d.go", worker, file), strings.Repeat("const unrelated = 12345;\n", 256))
		}
	}
	writeSearchFixture(b, dir, "src/current.go", "const currentNeedle = 7;\n")
	tool := NewSearchCode(dir)
	b.ReportAllocs()
	for b.Loop() {
		r, err := tool.fallback(context.Background(), dir, "currentNeedle", 20)
		if err != nil || r.Err != nil || !strings.Contains(r.Text, "src/current.go") {
			b.Fatalf("%+v %v", r, err)
		}
	}
}
