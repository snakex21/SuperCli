package search

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestSearchContextPreservesLateMatch(t *testing.T) {
	root := t.TempDir()
	source := "before-neighbor\n" + strings.Repeat("x", 9000) + " RetryPolicy=6842 " + strings.Repeat("ą", 100) + "\nafter-neighbor\n"
	writeSearchFixture(t, root, "settings.txt", source)
	args, _ := json.Marshal(map[string]any{"query": "RetryPolicy", "context": 1})
	result, err := NewSearchCode(root).run(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("%+v %v", result, err)
	}
	if !strings.Contains(result.Text, "RetryPolicy=6842") || !strings.Contains(result.Text, "before-neighbor") || !strings.Contains(result.Text, "after-neighbor") {
		t.Fatalf("lost match or context: %s", result.Text)
	}
	if len(result.Text) > 2600 || !utf8.ValidString(result.Text) {
		t.Fatalf("unbounded/invalid result: %d bytes", len(result.Text))
	}
	if !strings.Contains(result.RetainedText, strings.Repeat("x", 9000)) || !strings.Contains(result.RetainedText, "RetryPolicy=6842") {
		t.Fatal("original matching line is not retained")
	}
	model := core.NewOutputStore().ModelContent("search_code", result)
	if !strings.Contains(model, "RetryPolicy=6842") || !strings.Contains(model, "before-neighbor") {
		t.Fatal("model lost evidence")
	}
}

func TestSearchContextLongMatchesBeyondPreviewCap(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < 70; i++ {
		body.WriteString(strings.Repeat("x", 2200) + " needle=value\n")
	}
	path := writeSearchFixture(t, root, "many.txt", body.String())
	tool := NewSearchCode(root)
	preview := &searchContext{radius: 1, query: "needle"}
	locations, err := tool.fallback(context.Background(), path, "needle", 100, preview)
	if err != nil || locations.Err != nil {
		t.Fatal(err, locations.Err)
	}
	result := tool.renderSearchContext(context.Background(), preview, locations)
	if result.Err != nil || strings.Count(result.Text, "needle=value") != 70 {
		t.Fatalf("lost matches beyond preview cap: %v", result.Err)
	}
	if result.RetainedText != locations.Text {
		t.Fatal("saved matches differ")
	}
}

func TestSearchContextCapturedLongLineLimit(t *testing.T) {
	preview := &searchContext{radius: 1, query: "needle"}
	for i := 1; i <= 600; i++ {
		captureSearchHit([]*searchContext{preview}, "a.txt", i, strings.Repeat("x", 2100)+"needle")
	}
	if len(preview.longLines) != maxSearchContextLines {
		t.Fatalf("retained %d long lines", len(preview.longLines))
	}
}

func TestSearchContextLongLineControls(t *testing.T) {
	for _, query := range []string{"(?i)retrypolicy", "Retry["} {
		t.Run(query, func(t *testing.T) {
			root := t.TempDir()
			term := "RETRYPOLICY=6842"
			if query == "Retry[" {
				term = "RETRY[=6842"
			}
			path := writeSearchFixture(t, root, "a.txt", "before\r\n"+strings.Repeat("ź", 2000)+term+"\r\nafter\r\n")
			preview := &searchContext{radius: 1, query: query}
			tool := NewSearchCode(root)
			locations, err := tool.fallback(context.Background(), path, query, 50, preview)
			if err != nil || locations.Err != nil {
				t.Fatal(err, locations.Err)
			}
			result := tool.renderSearchContext(context.Background(), preview, locations)
			if result.Err != nil || !strings.Contains(result.Text, term) || !utf8.ValidString(result.Text) || strings.Contains(result.Text, "\r") {
				t.Fatalf("bad excerpt: %+v", result)
			}
		})
	}
	root := t.TempDir()
	writeSearchFixture(t, root, "short.txt", "before\nneedle=value\nafter\n")
	args, _ := json.Marshal(map[string]any{"query": "needle", "context": 1})
	result, _ := NewSearchCode(root).run(context.Background(), args)
	want := "== short.txt:1-3 ==\n     1 | before\n>    2 | needle=value\n     3 | after"
	if result.Err != nil || result.Text != want || result.RetainedText != "" || result.ModelPreview != "" {
		t.Fatalf("short output changed: %+v", result)
	}
}

func TestSearchContextLongLineAutoAndFreshness(t *testing.T) {
	root := t.TempDir()
	source := strings.Repeat("x", 9000) + " needle=old\n"
	writeSearchFixture(t, root, "a.txt", source)
	tool := NewSearchCode(root)
	auto, _ := tool.run(context.Background(), json.RawMessage("{\"query\":\"needle\"}"))
	args, _ := json.Marshal(map[string]any{"query": "needle", "context": 0})
	locations, _ := tool.run(context.Background(), args)
	if auto.Text != locations.Text || auto.ModelPreview != locations.ModelPreview || auto.RetainedText != locations.RetainedText {
		t.Fatal("automatic long-line view changed")
	}
	writeSearchFixture(t, root, "a.txt", strings.Replace(source, "needle=old", "needle=new", 1))
	args, _ = json.Marshal(map[string]any{"query": "needle", "context": 1})
	fresh, _ := tool.run(context.Background(), args)
	if fresh.Err != nil || !strings.Contains(fresh.Text, "needle=new") || strings.Contains(fresh.Text, "needle=old") {
		t.Fatal("stale result after file edit")
	}
}

func TestSearchContextRGFallbackClearsCapturedLines(t *testing.T) {
	root := t.TempDir()
	path := writeSearchFixture(t, root, "a.txt", "first\nneedle=new\nlast\n")
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "fail")
	preview := &searchContext{radius: 1, query: "needle", longLines: map[searchHit]string{{path: path, line: 2}: strings.Repeat("x", 3000) + "needle=stale"}}
	tool := NewSearchCode(root)
	result, err := tool.ripgrep(context.Background(), os.Args[0], path, "needle", 50, preview)
	if err != nil || result.Err != nil {
		t.Fatal(err, result.Err)
	}
	rich := tool.renderSearchContext(context.Background(), preview, result)
	if len(preview.longLines) != 0 || !strings.Contains(rich.Text, "needle=new") || strings.Contains(rich.Text, "stale") {
		t.Fatal("partial rg data leaked into fallback")
	}
}

func BenchmarkSearchContextMatchingLine(b *testing.B) {
	for _, kind := range []string{"late", "head", "short", "auto"} {
		b.Run(kind, func(b *testing.B) {
			root := b.TempDir()
			source := "before\nneedle=value\nafter\n"
			if kind != "short" {
				source = "before\n" + strings.Repeat("x", 9000) + " needle=value\nafter\n"
			}
			if kind == "head" {
				source = "before\nneedle=value " + strings.Repeat("x", 9000) + "\nafter\n"
			}
			writeSearchFixture(b, root, "a.txt", source)
			tool := NewSearchCode(root)
			args, _ := json.Marshal(map[string]any{"query": "needle", "context": 1})
			if kind == "auto" {
				args, _ = json.Marshal(map[string]any{"query": "needle"})
			}
			b.ReportAllocs()
			for b.Loop() {
				res, err := tool.run(context.Background(), args)
				if err != nil || res.Err != nil {
					b.Fatal(err, res.Err)
				}
			}
		})
	}
}

func TestSearchContextVisibleLongLineUnchanged(t *testing.T) {
	root := t.TempDir()
	path := writeSearchFixture(t, root, "a.txt", "before\nneedle=value "+strings.Repeat("x", 9000)+"\nafter\n")
	tool := NewSearchCode(root)
	preview := &searchContext{radius: 1, query: "needle"}
	locations, err := tool.fallback(context.Background(), path, "needle", 50, preview)
	if err != nil || locations.Err != nil {
		t.Fatal(err, locations.Err)
	}
	repaired := tool.renderSearchContext(context.Background(), preview, locations)
	preview.longLines = nil
	original := tool.renderSearchContext(context.Background(), preview, locations)
	if repaired.Text != original.Text || repaired.ModelPreview != original.ModelPreview || repaired.RetainedText != original.RetainedText {
		t.Fatal("already visible long-line match changed")
	}
}

func TestSearchContextMatchAcrossHeadBoundary(t *testing.T) {
	for _, prefix := range []int{maxSearchContextBytes - len("needle=value"), maxSearchContextBytes - 3, maxSearchContextBytes + 1} {
		root := t.TempDir()
		writeSearchFixture(t, root, "a.txt", strings.Repeat("x", prefix)+"needle=value"+strings.Repeat("z", 100)+"\n")
		args, _ := json.Marshal(map[string]any{"query": "needle=value", "context": 1})
		result, err := NewSearchCode(root).run(context.Background(), args)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "needle=value") {
			t.Fatalf("match lost at prefix=%d: %v %v", prefix, err, result.Err)
		}
		if (result.RetainedText != "") != (prefix+len("needle=value") > maxSearchContextBytes) {
			t.Fatalf("unexpected retention at prefix=%d", prefix)
		}
	}
}
