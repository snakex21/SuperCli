package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchContextContainsAnswerInSameResult(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "src/config.go", "package demo\nfunc retryLimit() int {\n\treturn 3\n}\n")
	res, err := NewSearchCode(dir).run(context.Background(), json.RawMessage("{\"query\":\"func retryLimit\",\"context\":2,\"max\":1}"))
	if err != nil || res.Err != nil || !strings.Contains(res.Text, ">    2 | func retryLimit") || !strings.Contains(res.Text, "return 3") {
		t.Fatalf("search must include the answer without another read: %+v %v", res, err)
	}
	if !strings.Contains(res.Text, "== src/config.go:1-4 ==") {
		t.Fatal(res.Text)
	}
}

func TestSearchContextMergesOverlapsAndPreservesExplicitLocations(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a.txt", "one\nneedle two\nthree\nneedle four\nfive\n")
	tool := NewSearchCode(dir)
	plain, _ := tool.run(context.Background(), json.RawMessage("{\"query\":\"needle\",\"context\":0}"))
	if plain.Err != nil || plain.Text != "a.txt:2:needle two\na.txt:4:needle four" {
		t.Fatalf("explicit location output changed: %+v", plain)
	}
	rich, _ := tool.run(context.Background(), json.RawMessage("{\"query\":\"needle\",\"context\":1}"))
	if rich.Err != nil || strings.Count(rich.Text, "== a.txt:") != 1 || strings.Count(rich.Text, "| three") != 1 ||
		!strings.Contains(rich.Text, ">    2 |") || !strings.Contains(rich.Text, ">    4 |") {
		t.Fatalf("overlapping neighborhoods were duplicated: %+v", rich)
	}
}

func TestSearchContextLimitsAndFailures(t *testing.T) {
	dir := t.TempDir()
	tool := NewSearchCode(dir)
	for _, radius := range []int{-1, 21} {
		res, _ := tool.run(context.Background(), json.RawMessage(fmt.Sprintf("{\"query\":\"needle\",\"context\":%d}", radius)))
		if res.Err == nil {
			t.Fatalf("accepted radius %d", radius)
		}
	}
	noMatch, _ := tool.run(context.Background(), json.RawMessage("{\"query\":\"needle\",\"context\":2}"))
	if noMatch.Err != nil || noMatch.Text != "no matches" {
		t.Fatalf("%+v", noMatch)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := tool.run(ctx, json.RawMessage("{\"query\":\"needle\",\"context\":2}"))
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("cancel was hidden: %+v", res)
	}
	locations := Result{Text: "gone.go:2:needle"}
	missing := tool.renderSearchContext(context.Background(), &searchContext{radius: 1, hits: []searchHit{{path: filepath.Join(dir, "gone.go"), line: 2}}}, locations)
	if missing.Err == nil || missing.Text != locations.Text || !errors.Is(missing.Err, os.ErrNotExist) {
		t.Fatalf("lost search evidence after file disappearance: %+v", missing)
	}
}

func TestSearchContextBoundsLargeOutputAndLongLines(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("before\n", 20) + "needle\n" + strings.Repeat("after\n", 20)
	for i := 0; i < 20; i++ {
		writeSearchFixture(t, dir, fmt.Sprintf("f%02d.go", i), body)
	}
	res, _ := NewSearchCode(dir).run(context.Background(), json.RawMessage("{\"query\":\"needle\",\"context\":20,\"max\":20}"))
	if res.Err != nil || !strings.Contains(res.Text, "context capped at 500 lines") {
		t.Fatalf("%+v", res)
	}
	if strings.Count(res.Text, " | ") > maxSearchContextLines {
		t.Fatal("context exceeded line budget")
	}
	large := writeSearchFixture(t, dir, "long.go", "needle\n"+strings.Repeat("ą", 6000)+"\n")
	raw, _ := json.Marshal(map[string]any{"query": "needle", "path": large, "context": 1})
	res, _ = NewSearchCode(dir).run(context.Background(), raw)
	if res.Err != nil || len(res.Text) > 3000 || !strings.Contains(res.Text, "bytes on this line truncated") || !utf8.ValidString(res.Text) {
		t.Fatalf("unbounded or invalid long line: bytes=%d err=%v", len(res.Text), res.Err)
	}
}

func TestSearchContextRGMatchesFallback(t *testing.T) {
	dir := t.TempDir()
	path := writeSearchFixture(t, dir, "a.zig", "first\nneedle\nlast\n")
	tool := NewSearchCode(dir)
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "context")
	rgPreview := &searchContext{radius: 1}
	res, err := tool.ripgrep(context.Background(), os.Args[0], path, "needle", 1, rgPreview)
	if err != nil || res.Err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	rich := tool.renderSearchContext(context.Background(), rgPreview, res)
	fallbackPreview := &searchContext{radius: 1}
	base, _ := tool.fallback(context.Background(), path, "needle", 1, fallbackPreview)
	fallback := tool.renderSearchContext(context.Background(), fallbackPreview, base)
	if rich.Err != nil || rich.Text != fallback.Text || !strings.Contains(rich.Text, "last") {
		t.Fatalf("rg=%+v fallback=%+v", rich, fallback)
	}
}

func TestSearchContextRGFailureRetainsContext(t *testing.T) {
	dir := t.TempDir()
	path := writeSearchFixture(t, dir, "a.zig", "first\nneedle\nlast\n")
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "long")
	tool := NewSearchCode(dir)
	preview := &searchContext{radius: 1, hits: []searchHit{{path: "stale partial rg path", line: 1}}}
	res, err := tool.ripgrep(context.Background(), os.Args[0], path, "needle", 1, preview)
	if err != nil || res.Err != nil {
		t.Fatalf("%+v %v", res, err)
	}
	rich := tool.renderSearchContext(context.Background(), preview, res)
	if rich.Err != nil || !strings.Contains(rich.Text, "last") || len(preview.hits) != 1 {
		t.Fatalf("fallback lost context or kept stale rg hits: %+v", rich)
	}
}

func TestContextSearchPathWithColons(t *testing.T) {
	path := "C:\\work\\src\\example.go"
	got, number, content, ok := parseContextSearchHit(path + "\x002:needle:42:value")
	if !ok || got != path || number != 2 || content != "needle:42:value" {
		t.Fatalf("%q %d %q %v", got, number, content, ok)
	}
	unix := "/work/a:42:part.go"
	got, _, _, ok = parseContextSearchHit(unix + "\x007:needle")
	if !ok || got != unix {
		t.Fatalf("colon in filename changed path: %q", got)
	}
}

func BenchmarkSearchContextLocalCost(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 40; i++ {
		writeSearchFixture(b, dir, fmt.Sprintf("f%02d.go", i), strings.Repeat("ordinary line\n", 256)+"func retryLimit() int { return 3 }\n")
	}
	tool := NewSearchCode(dir)
	for _, radius := range []int{0, 8} {
		b.Run(fmt.Sprintf("context=%d", radius), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var preview *searchContext
				if radius > 0 {
					preview = &searchContext{radius: radius}
				}
				res, err := tool.fallback(context.Background(), dir, "func retryLimit", 3, preview)
				if preview != nil && res.Err == nil {
					res = tool.renderSearchContext(context.Background(), preview, res)
				}
				if err != nil || res.Err != nil {
					b.Fatalf("%+v %v", res, err)
				}
			}
		})
	}
}
