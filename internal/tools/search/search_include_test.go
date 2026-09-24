package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSearchIncludeGlobSemantics(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "a.go", true}, {"*.go", "src/a.go", true}, {"*.go", "src/a.zig", false},
		{"src/*.go", "src/a.go", true}, {"src/*.go", "src/deep/a.go", false},
		{"src/**/*.go", "src/a.go", true}, {"src/**/*.go", "src/deep/a.go", true},
		{"**/test?.{go,zig}", "test1.go", true}, {"**/test?.{go,zig}", "src/test2.zig", true},
		{"**/test?.{go,zig}", "src/test22.go", false}, {"{src,lib}/**/*.{go,zig}", "lib/deep/a.zig", true},
		{"{src,lib}/**/*.{go,zig}", "tests/a.zig", false}, {"./src/**", "src/deep/a.go", true},
		{"[ab]*.go", "src/alpha.go", true}, {"[^a]*.go", "src/alpha.go", false},
		{"*.go", "src/ąść.go", true}, {"*.go", "src/A.GO", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			g, err := compileSearchGlob(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got := g.matches(root, filepath.Join(root, filepath.FromSlash(tc.name))); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
	for _, bad := range []string{"!*.go", "../*.go", "/src/*.go", "src\\*.go", "*.go,*.zig", "[", "src/**bad", "{go}", "{go,}", "{a,{b,c}}", strings.Repeat("*", 513)} {
		if _, err := compileSearchGlob(bad); err == nil {
			t.Fatalf("accepted invalid filter %q", bad)
		}
	}
	if _, err := compileSearchGlob(strings.Repeat("{a,b}", 6)); err == nil {
		t.Fatal("unbounded alternatives")
	}
}

func TestSearchIncludeRootFileAndBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	source := writeSearchFixture(t, dir, "src/a.go", "package sample\nconst needle = 7\n")
	// Previously a broad fallback search fails on this oversized line. The
	// filter must reject it before opening/scanning it, not trim matches later.
	writeSearchFixture(t, dir, "a.md", strings.Repeat("x", 1024*1024+1)+"\n")
	for _, tc := range []struct{ root, pattern string }{{dir, "*.go"}, {dir, "src/**/*.go"}, {filepath.Dir(source), "*.go"}, {source, "*.go"}} {
		g, err := compileSearchGlob(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := NewSearchCode(dir).fallback(context.Background(), tc.root, "needle", 20, &searchContext{include: g})
		if err != nil || got.Err != nil || got.Text != "src/a.go:2:const needle = 7" {
			t.Fatalf("%+v %v", got, err)
		}
	}
	args, _ := json.Marshal(map[string]any{"query": "needle", "path": "src/a.go", "include": "*.zig"})
	got, err := NewSearchCode(dir).run(context.Background(), args)
	if err != nil || got.Err != nil || got.Text != "no matches" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = NewSearchCode(dir).run(context.Background(), json.RawMessage(`{"query":"needle","include":"["}`))
	if err != nil || got.Err == nil || !strings.Contains(got.Err.Error(), "include") {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSearchIncludeRGAndFallbackKeepFilterAndLimit(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a.md", "needle excluded\n")
	writeSearchFixture(t, dir, "b.go", "needle first\nneedle second\n")
	g, _ := compileSearchGlob("*.go")
	for _, mode := range []string{"focus", "fail"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SUPERCLI_SEARCH_RG_HELPER", mode)
			preview := &searchContext{include: g, radius: 1}
			got, err := NewSearchCode(dir).ripgrep(context.Background(), os.Args[0], dir, "needle", 1, preview)
			if err != nil || got.Err != nil || strings.Contains(got.Text, "excluded") || !strings.Contains(got.Text, "b.go:1:needle first") || !strings.Contains(got.Text, "search limit reached: 1") {
				t.Fatalf("%+v %v", got, err)
			}
			rendered := NewSearchCode(dir).renderSearchContext(context.Background(), preview, got)
			if rendered.Err != nil || !strings.Contains(rendered.Text, ">    1 | needle first") || !strings.Contains(rendered.Text, "search limit reached: 1") {
				t.Fatalf("%+v", rendered)
			}
		})
	}
}

func BenchmarkSearchInclude(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 128; i++ {
		writeSearchFixture(b, dir, fmt.Sprintf("notes%03d.md", i), strings.Repeat("documentation about an unrelated feature\n", 512))
	}
	writeSearchFixture(b, dir, "src/widget.go", "package widget\nfunc ResolveWidget() string { return \"target\" }\n")
	for _, pattern := range []string{"", "*.go"} {
		b.Run(map[bool]string{true: "all_files", false: "go_files"}[pattern == ""], func(b *testing.B) {
			g, _ := compileSearchGlob(pattern)
			tool := NewSearchCode(dir)
			b.ReportAllocs()
			for b.Loop() {
				got, err := tool.fallback(context.Background(), dir, "ResolveWidget", 50, &searchContext{include: g})
				if err != nil || got.Err != nil || !strings.Contains(got.Text, "target") {
					b.Fatalf("%+v %v", got, err)
				}
			}
		})
	}
}

// Optional integration against an explicitly supplied standalone rg binary.
func TestSearchIncludeRealRipgrepParity(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG to test a real ripgrep executable")
	}
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.zig", "c.md", "src/a.go", "src/test1.go", "src/deep/test2.zig", "lib/a.go", "lib/deep/c.zig", "node_modules/skip.go"} {
		writeSearchFixture(t, dir, name, "needle\n")
	}
	tool := NewSearchCode(dir)
	for _, pattern := range []string{"*.go", "src/*.go", "src/**/*.go", "**/test?.{go,zig}", "{src,lib}/**/*.{go,zig}", "./src/**", "[ab]*.go", "[^a]*.go"} {
		t.Run(pattern, func(t *testing.T) {
			g, err := compileSearchGlob(pattern)
			if err != nil {
				t.Fatal(err)
			}
			fallback, e := tool.fallback(context.Background(), dir, "needle", 50, &searchContext{include: g})
			actual, e2 := tool.ripgrep(context.Background(), rg, dir, "needle", 50, &searchContext{include: g})
			if e != nil || e2 != nil || fallback.Err != nil || actual.Err != nil {
				t.Fatalf("fallback=%+v/%v rg=%+v/%v", fallback, e, actual, e2)
			}
			left, right := strings.Split(fallback.Text, "\n"), strings.Split(actual.Text, "\n")
			slices.Sort(left)
			slices.Sort(right)
			if !slices.Equal(left, right) {
				t.Fatalf("fallback=%q rg=%q", fallback.Text, actual.Text)
			}
		})
	}
}
