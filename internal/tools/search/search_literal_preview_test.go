package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestSearchLiteralPreviewShowsCapturedMatch(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	for _, backend := range []string{"fallback", "rg"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "rg" {
				if rg == "" {
					t.Skip("set SUPERCLI_TEST_RG")
				}
				t.Setenv("PATH", filepath.Dir(rg))
			} else {
				t.Setenv("PATH", t.TempDir())
			}
			for _, fixture := range []struct{ name, query, prefix, match string }{
				{"bracket", "Retry[", strings.Repeat("x", 9000), "RETRY[=6842"},
				{"parenthesis", "Lookup(", strings.Repeat("ą", 5000), "LOOKUP(value)=921"},
				{"polish", "żądanie[", strings.Repeat("界", 3500), "ŻĄDANIE[=821"},
				// strings.ToLower (the scanner's fallback policy) differs from Unicode
				// simple folding. A wider or narrower preview match must not hide the hit.
				{"no_extra_casefold", "s[", "ſ[wrong " + strings.Repeat("x", 9000), "S[=6842"},
				{"lowercase_width", "i[", strings.Repeat("İ", 5000), "İ[=6842"},
			} {
				for _, radius := range []int{-1, 0, 1} {
					t.Run(fmt.Sprintf("%s/context=%d", fixture.name, radius), func(t *testing.T) {
						dir := t.TempDir()
						source := "before\n" + fixture.prefix + fixture.match + strings.Repeat("z", 2000) + "\nafter\n"
						path := writeSearchFixture(t, dir, "source.txt", source)
						args := map[string]any{"query": fixture.query}
						if radius >= 0 {
							args["context"] = radius
						}
						raw, _ := json.Marshal(args)
						result, err := NewSearchCode(dir).run(context.Background(), raw)
						if err != nil || result.Err != nil {
							t.Fatalf("search: %v / %v", err, result.Err)
						}
						store := core.NewOutputStore()
						view := store.ModelContent("search_code", result)
						if !strings.Contains(view, fixture.match) {
							t.Errorf("captured match hidden in preview: %s", view)
						}
						if len(view) > core.ModelOutputPreviewBytes+300 || !utf8.ValidString(view) {
							t.Fatalf("unbounded or invalid preview: %d bytes", len(view))
						}
						if !strings.Contains(result.Text, fixture.match) {
							t.Error("search lost the captured match")
						}
						if !strings.Contains(view, "source.txt") {
							t.Error("missing path")
						}
						// Retrieve the original captured line after removing the source. This
						// must use the retained result rather than repeat the file search.
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						_, tail, ok := strings.Cut(view, "handle=")
						if !ok {
							t.Fatal("missing retained result")
						}
						handle := strings.FieldsFunc(tail, func(r rune) bool { return r == ';' || r == ']' || r == ' ' })[0]
						raw, _ = json.Marshal(map[string]any{"handle": handle, "query": fixture.match})
						saved, err := store.ReadOutputTool().Fn(context.Background(), raw)
						if err != nil || saved.Err != nil || !strings.Contains(saved.Text, fixture.match) {
							t.Fatalf("retained match unavailable: %v / %v; %s", err, saved.Err, saved.Text)
						}
					})
				}
			}
		})
	}
}

func BenchmarkSearchLiteralPreview(b *testing.B) {
	for _, query := range []string{"RetryPolicy", "Retry["} {
		b.Run(query, func(b *testing.B) {
			term := query
			if query == "Retry[" {
				term = strings.ToUpper(query)
			}
			text := strings.Repeat("x", 9000) + term + "=6842" + strings.Repeat("z", 3000)
			tool := NewSearchCode(".")
			preview := &searchContext{records: []searchRecord{{path: "source.txt", line: 1, text: text}}}
			b.ReportAllocs()
			for b.Loop() {
				_ = tool.previewSearchHits(Result{Text: text}, preview, query)
			}
		})
	}
}

func TestSearchLiteralMatchOriginalByteOffsets(t *testing.T) {
	for _, fixture := range []struct{ query, text, match string }{
		{"i[", "Ⱥİxx I[yes", "I["},
		{"i[", "İ[yes", "İ["},
		{"k[", "K[yes", "K["},
		{"s[", "ſ[no S[yes", "S["},
		{"s[", "ſ[no", ""},
		{"i[", "\xffI[yes", "I["},
		{"i[", "nothing", ""},
	} {
		match := newSearchExcerptMatcher(fixture.query).find(fixture.text)
		if fixture.match == "" {
			if match != nil {
				t.Fatalf("unexpected match: %q -> %v", fixture.text, match)
			}
			continue
		}
		want := strings.Index(fixture.text, fixture.match)
		if len(match) != 2 || match[0] != want || match[1] != want+len(fixture.match) {
			t.Fatalf("%q: offsets=%v want %d:%d", fixture.text, match, want, want+len(fixture.match))
		}
	}
}
