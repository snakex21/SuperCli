package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func searchRGPruneStats(t *testing.T, tool *SearchCode, rg, root string, include *searchGlob, legacy bool) (hits []string, files, bytes int) {
	t.Helper()
	cmd := tool.ripgrepCommand(context.Background(), rg, root, "needle", 50, &searchContext{include: include})
	if legacy {
		// Compare with the old glob policy, using the same root and user include.
		for i := 1; i+1 < len(cmd.Args); i++ {
			if cmd.Args[i] != "--iglob" {
				continue
			}
			glob := cmd.Args[i+1]
			name := strings.TrimSuffix(strings.TrimPrefix(glob, "!**/"), "/**")
			if skippedDirs[name] {
				cmd.Args[i], cmd.Args[i+1] = "-g", "!"+name+"/**"
			}
		}
	}
	cmd.Args = append([]string{cmd.Args[0], "--stats"}, cmd.Args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rg: %v: %s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if file, number, text, ok := parseContextSearchHit(strings.TrimSuffix(line, "\r")); ok {
			if !searchPathIsSkipped(root, file) && include.matches(root, file) {
				hits = append(hits, fmt.Sprintf("%s:%d:%s", tool.displayPath(file), number, text))
			}
		} else {
			var count int
			if _, err := fmt.Sscanf(line, "%d files searched", &count); err == nil && strings.Contains(line, "files searched") {
				files = count
			}
			if _, err := fmt.Sscanf(line, "%d bytes searched", &count); err == nil && strings.Contains(line, "bytes searched") {
				bytes = count
			}
		}
	}
	slices.Sort(hits)
	return hits, files, bytes
}

func TestSearchRGPrunesGeneratedDirectoriesBeforeReading(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG to verify real ripgrep traversal")
	}
	workspace := t.TempDir()
	root := filepath.Join(workspace, "checkout")
	source := "needle SOURCE\n"
	writeSearchFixture(t, root, "src/source.go", source)
	writeSearchFixture(t, root, "Build/generated.go", strings.Repeat("generated unrelated\n", 20000))
	writeSearchFixture(t, root, "src/module/build/generated.go", strings.Repeat("generated unrelated\n", 20000))
	writeSearchFixture(t, root, "src/NODE_MODULES/generated.go", strings.Repeat("generated unrelated\n", 20000))
	glob, _ := compileSearchGlob("*.go")
	tool := NewSearchCode(workspace)
	before, beforeFiles, beforeBytes := searchRGPruneStats(t, tool, rg, root, glob, true)
	after, afterFiles, afterBytes := searchRGPruneStats(t, tool, rg, root, glob, false)
	want := []string{"checkout/src/source.go:1:needle SOURCE"}
	if !slices.Equal(before, want) || !slices.Equal(after, want) {
		t.Fatalf("paths/hits changed: before=%v after=%v want=%v", before, after, want)
	}
	if beforeFiles <= afterFiles || beforeBytes <= afterBytes || afterFiles != 1 || afterBytes != len(source) {
		t.Fatalf("traversal: before=%d files/%d bytes, after=%d files/%d bytes", beforeFiles, beforeBytes, afterFiles, afterBytes)
	}
	t.Logf("same hits; before=%d files/%d bytes, after=%d files/%d bytes", beforeFiles, beforeBytes, afterFiles, afterBytes)
}

func TestSearchRGPruningPreservesExplicitRootsAndGlobSemantics(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG to verify real ripgrep traversal")
	}
	workspace := t.TempDir()
	root := filepath.Join(workspace, "checkout")
	for _, rel := range []string{"src/source.go", "src/upper.GO", "src/extra.txt", ".hidden/keep.go", "ignored.go", "Build/keep.go", "Build/sub/target/skip.go", ".tmp/keep.go"} {
		writeSearchFixture(t, root, rel, "needle "+rel+"\n")
	}
	writeSearchFixture(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeSearchFixture(t, root, ".gitignore", "ignored.go\n")
	tool := NewSearchCode(workspace)
	for _, tc := range []struct {
		name, rel, include string
		want               []string
	}{
		{"default ignores", ".", "", []string{"checkout/src/extra.txt:1:needle src/extra.txt", "checkout/src/source.go:1:needle src/source.go", "checkout/src/upper.GO:1:needle src/upper.GO"}},
		{"case-sensitive include", ".", "src/*.go", []string{"checkout/src/source.go:1:needle src/source.go"}},
		{"positive include", ".", "*.go", []string{"checkout/ignored.go:1:needle ignored.go", "checkout/src/source.go:1:needle src/source.go"}},
		{"positive hidden include", ".", "**/*", []string{"checkout/.hidden/keep.go:1:needle .hidden/keep.go", "checkout/ignored.go:1:needle ignored.go", "checkout/src/extra.txt:1:needle src/extra.txt", "checkout/src/source.go:1:needle src/source.go", "checkout/src/upper.GO:1:needle src/upper.GO"}},
		{"explicit build", "Build", "", []string{"checkout/Build/keep.go:1:needle Build/keep.go"}},
		{"explicit tmp", ".tmp", "*.go", []string{"checkout/.tmp/keep.go:1:needle .tmp/keep.go"}},
		{"explicit file", "Build/keep.go", "", []string{"checkout/Build/keep.go:1:needle Build/keep.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			searchRoot := filepath.Join(root, filepath.FromSlash(tc.rel))
			glob, err := compileSearchGlob(tc.include)
			if err != nil {
				t.Fatal(err)
			}
			hits, _, _ := searchRGPruneStats(t, tool, rg, searchRoot, glob, false)
			if !slices.Equal(hits, tc.want) {
				t.Fatalf("got=%v want=%v", hits, tc.want)
			}
			result, err := tool.ripgrep(context.Background(), rg, searchRoot, "needle", 50, &searchContext{include: glob})
			if err != nil || result.Err != nil {
				t.Fatalf("%+v %v", result, err)
			}
			actual := strings.Split(result.Text, "\n")
			slices.Sort(actual)
			if !slices.Equal(actual, tc.want) {
				t.Fatalf("tool paths/hits=%v want=%v", actual, tc.want)
			}
		})
	}
}
