package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func BenchmarkSearchIncludeTraversal(b *testing.B) {
	dir := b.TempDir()
	for folder := 0; folder < 128; folder++ {
		for file := 0; file < 16; file++ {
			writeSearchFixture(b, dir, fmt.Sprintf("docs/group%03d/file%03d.md", folder, file), "unrelated documentation\n")
		}
	}
	writeSearchFixture(b, dir, "src/widget.go", "package widget\nfunc ResolveWidget() {}\n")
	g, err := compileSearchGlob("src/**/*.go")
	if err != nil {
		b.Fatal(err)
	}
	tool := NewSearchCode(dir)
	b.ReportAllocs()
	for b.Loop() {
		result, err := tool.fallback(context.Background(), dir, "ResolveWidget", 50, &searchContext{include: g})
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "src/widget.go:2:func ResolveWidget() {}") {
			b.Fatalf("%+v %v", result, err)
		}
	}
}

func TestSearchGlobCanDescend(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		pattern, dir string
		want         bool
	}{
		{"src/**/*.go", "src", true}, {"src/**/*.go", "src/deep", true}, {"src/**/*.go", "docs", false},
		{"src/*.go", "src", true}, {"src/*.go", "src/deep", false}, {"src/*.go", "src/main.go", false},
		{"{src,lib}/**/*.{go,zig}", "lib/deep", true}, {"{src,lib}/**/*.{go,zig}", "docs", false},
		{"s[rt]c/**/test?.go", "src/nested", true}, {"s[^r]c/*.go", "src", false},
		{"src/**/test/*.go", "src/a/test", true}, {"src/**/test/*.go", "src/a/test/deep", true},
		{"src/**", "src/deep", true}, {"*.go", "any/deep", true}, {"**/src/*.go", "other/deep", true},
	} {
		t.Run(tc.pattern+"/"+tc.dir, func(t *testing.T) {
			g, err := compileSearchGlob(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got := g.canDescend(root, filepath.Join(root, filepath.FromSlash(tc.dir))); got != tc.want {
				t.Fatalf("canDescend=%v want %v", got, tc.want)
			}
		})
	}
}

// Exhaustively check short paths against the existing file matcher. A directory
// predicate may admit extra work, but it must never discard a matching file.
func TestSearchPruningPreservesMatchingAncestors(t *testing.T) {
	root := t.TempDir()
	patterns := []string{"*.go", "src/*.go", "src/**/*.go", "{src,lib}/**/*.{go,zig}", "[sl]*c/**/test?.go", "src/**/lib/**/test?.go", "src/**/**/a.go", "src/*/*/a.go", "./src/**", "src/[ab]*/**/[^x]*.go"}
	var paths []string
	var generate func(string, int)
	generate = func(prefix string, depth int) {
		for _, name := range []string{"a.go", "test1.go", "a.zig", "note.txt"} {
			paths = append(paths, filepath.Join(root, prefix, name))
		}
		if depth == 0 {
			return
		}
		for _, dir := range []string{"src", "lib", "abc", "test1.go"} {
			generate(filepath.Join(prefix, dir), depth-1)
		}
	}
	generate("", 4)
	for _, pattern := range patterns {
		g, err := compileSearchGlob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range paths {
			if !g.matches(root, file) {
				continue
			}
			for dir := filepath.Dir(file); dir != root; dir = filepath.Dir(dir) {
				if !g.canDescend(root, dir) {
					t.Fatalf("%q discards ancestor %q of matching file %q", pattern, dir, file)
				}
			}
		}
	}
}

func TestSearchFilteredWalkSkipsUnrelatedSubtrees(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"src/a.go", "src/deep/b.go", "src/deep/note.md", "docs/nested/a.go", "lib/a.go", "src/node_modules/skip.go", "src/.hidden/c.go"} {
		writeSearchFixture(t, root, name, "needle\n")
	}
	g, _ := compileSearchGlob("src/**/*.go")
	var visited, got, want []string
	descend := func(dir string) bool {
		rel, _ := filepath.Rel(root, dir)
		visited = append(visited, filepath.ToSlash(rel))
		return g.canDescend(root, dir)
	}
	collect := func(dst *[]string) func(string) error {
		return func(file string) error {
			if g.matches(root, file) {
				*dst = append(*dst, file)
			}
			return nil
		}
	}
	if err := walkFilesContext(context.Background(), root, collect(&want)); err != nil {
		t.Fatal(err)
	}
	if err := walkFilesFiltered(context.Background(), root, descend, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered=%v full=%v", got, want)
	}
	if slices.Contains(visited, "docs/nested") || slices.Contains(visited, "src/node_modules") {
		t.Fatalf("entered discarded subtree: %v", visited)
	}
	if !slices.Contains(visited, "src/.hidden") {
		t.Fatalf("lost hidden source directory: %v", visited)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := walkFilesFiltered(ctx, root, descend, collect(&got)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
