package search

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSearchContextSeparatedWindowsAndBudget(t *testing.T) {
	dir := t.TempDir()
	var source strings.Builder
	for i := 1; i <= 1200; i++ {
		fmt.Fprintf(&source, "line %d\n", i)
	}
	path := writeSearchFixture(t, dir, "source.go", source.String())
	tool := NewSearchCode(dir)
	preview := &searchContext{radius: 1, hits: []searchHit{{path, 2}, {path, 100}, {path, 1199}}, limit: 3}
	res := tool.renderSearchContext(context.Background(), preview, Result{Text: "locations"})
	for _, want := range []string{"== source.go:1-3 ==", "== source.go:99-101 ==", "== source.go:1198-1200 ==", ">  100 | line 100", "search limit reached: 3"} {
		if res.Err != nil || !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q: %+v", want, res)
		}
	}
	preview = &searchContext{radius: 20}
	for line := 21; line < 1200; line += 50 {
		preview.hits = append(preview.hits, searchHit{path, line})
	}
	res = tool.renderSearchContext(context.Background(), preview, Result{Text: "locations"})
	if res.Err != nil || strings.Count(res.Text, " | ") != 492 || !strings.Contains(res.Text, "context capped at 500 lines") {
		t.Fatalf("budget: %+v", res)
	}
}

func BenchmarkSearchSeparatedContext(b *testing.B) {
	root := b.TempDir()
	var body strings.Builder
	for i := 1; i <= 100000; i++ {
		if i%5000 == 0 {
			body.WriteString("needle\n")
		} else {
			body.WriteString(strings.Repeat("x", 79) + "\n")
		}
	}
	path := writeSearchFixture(b, root, "large.go", body.String())
	tool := NewSearchCode(root)
	for _, mode := range []string{"render", "whole_search"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				preview := &searchContext{radius: 2}
				var res Result
				if mode == "render" {
					for line := 5000; line <= 100000; line += 5000 {
						preview.hits = append(preview.hits, searchHit{path, line})
					}
					res = Result{Text: "locations"}
				} else {
					res, _ = tool.fallback(context.Background(), root, "needle", 50, preview)
				}
				res = tool.renderSearchContext(context.Background(), preview, res)
				if res.Err != nil || strings.Count(res.Text, "== large.go:") != 20 {
					b.Fatalf("%+v", res)
				}
			}
		})
	}
}
