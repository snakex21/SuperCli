package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestSearchPreviewKeepsShortMiddleHit(t *testing.T) {
	for _, radius := range []string{"", `,"context":0`} {
		t.Run("context"+radius, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			dir := t.TempDir()
			writeSearchFixture(t, dir, "a.txt", strings.Repeat("a", 6000)+" BudgetLimit=1111 "+strings.Repeat("z", 6000)+"\n")
			writeSearchFixture(t, dir, "middle.env", "BudgetLimit=7321\n")
			writeSearchFixture(t, dir, "z.txt", strings.Repeat("b", 6000)+" BudgetLimit=9999 "+strings.Repeat("y", 6000)+"\n")
			got, err := NewSearchCode(dir).run(context.Background(), json.RawMessage(`{"query":"BudgetLimit"`+radius+"}"))
			if err != nil || got.Err != nil {
				t.Fatalf("%v %v", err, got.Err)
			}
			store := core.NewOutputStore()
			view := store.ModelContent("search_code", got)
			for _, want := range []string{"a.txt:1:", "middle.env:1:BudgetLimit=7321", "z.txt:1:", "BudgetLimit=1111", "BudgetLimit=9999", "handle=out_"} {
				if !strings.Contains(view, want) {
					t.Fatalf("model lost %q in %d bytes: %s", want, len(view), view)
				}
			}
			if len(view) > core.ModelOutputPreviewBytes+220 || !utf8.ValidString(view) {
				t.Fatalf("bad preview: %d bytes", len(view))
			}
			if len(got.Text) < 24000 || !strings.Contains(got.Text, strings.Repeat("a", 6000)) {
				t.Fatal("original result was discarded")
			}
			start := strings.Index(view, "handle=") + len("handle=")
			handle := strings.FieldsFunc(view[start:], func(r rune) bool { return r == ';' || r == ']' || r == ' ' })[0]
			args, _ := json.Marshal(map[string]any{"handle": handle, "query": "BudgetLimit=7321"})
			saved, err := store.ReadOutputTool().Fn(context.Background(), args)
			if err != nil || saved.Err != nil || !strings.Contains(saved.Text, "middle.env:1:BudgetLimit=7321") {
				t.Fatalf("saved evidence unavailable: %+v %v", saved, err)
			}
		})
	}
}

func TestSearchPreviewRealRipgrepParity(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG")
	}
	t.Setenv("PATH", filepath.Dir(rg))
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a.txt", strings.Repeat("ą", 7000)+" needle=123 "+strings.Repeat("ź", 7000)+"\nneedle=456\n")
	tool := NewSearchCode(dir)
	args := json.RawMessage(`{"query":"needle","context":0,"max":2}`)
	native, err := tool.run(context.Background(), args)
	if err != nil || native.Err != nil {
		t.Fatalf("%+v %v", native, err)
	}
	t.Setenv("PATH", t.TempDir())
	fallback, err := tool.run(context.Background(), args)
	if err != nil || fallback.Err != nil || native.ModelPreview == "" || native.ModelPreview != fallback.ModelPreview || native.Text != fallback.Text {
		t.Fatalf("different backends: native preview=%q fallback preview=%q err=%v/%v", native.ModelPreview, fallback.ModelPreview, err, fallback.Err)
	}
	if !utf8.ValidString(native.ModelPreview) || !strings.Contains(native.ModelPreview, "limit reached") || !strings.Contains(native.ModelPreview, "needle=456") {
		t.Fatalf("lost notice or text: %s", native.ModelPreview)
	}
}

func TestSearchPreviewKeepsExistingBoundaries(t *testing.T) {
	dir := t.TempDir()
	tool := NewSearchCode(dir)
	for _, n := range []int{core.ModelOutputInlineBytes, core.ModelOutputInlineBytes + 1} {
		writeSearchFixture(t, dir, "a.txt", "needle "+strings.Repeat("x", n-len("a.txt:1:needle "))+"\n")
		got, err := tool.run(context.Background(), json.RawMessage(`{"query":"needle","context":0}`))
		if err != nil || got.Err != nil || len(got.Text) != n {
			t.Fatalf("bytes=%d error=%v/%v", len(got.Text), err, got.Err)
		}
		if (got.ModelPreview != "") != (n > core.ModelOutputInlineBytes) {
			t.Fatalf("preview threshold: %d", n)
		}
	}
	// Path references must remain complete; if they cannot fit, use the old
	// saved-output preview rather than returning misleading shortened paths.
	preview := &searchContext{}
	for i := 0; i < 50; i++ {
		captureSearchHit([]*searchContext{preview}, filepath.Join(dir, strings.Repeat("long", 40), fmt.Sprintf("%d.txt", i)), 1, "needle")
	}
	got := tool.previewSearchHits(Result{Text: strings.Repeat("x", 9000)}, preview, "needle")
	if got.ModelPreview != "" {
		t.Fatal("oversized path list was shortened")
	}
	bad := tool.previewSearchHits(Result{Text: strings.Repeat("x", 9000), Err: fmt.Errorf("search failed")}, preview, "needle")
	if bad.ModelPreview != "" || bad.Err == nil {
		t.Fatal("search error was hidden")
	}
}

func TestSearchLineExcerptUTF8AndLateMatch(t *testing.T) {
	re := regexp.MustCompile("Szukane=7321")
	for _, surrounding := range []string{"x", "ą", "界", "🙂"} {
		text := strings.Repeat(surrounding, 5000) + "Szukane=7321" + strings.Repeat(surrounding, 5000)
		for _, budget := range []int{64, 127, 512, 2048} {
			got := searchLineExcerpt(text, re, budget)
			if len(got) > budget || !utf8.ValidString(got) || !strings.Contains(got, "Szukane=7321") {
				t.Fatalf("budget=%d bytes=%d invalid=%v match=%v", budget, len(got), !utf8.ValidString(got), strings.Contains(got, "Szukane=7321"))
			}
		}
	}
}

func TestSearchPreviewBoundsCapturedMetadata(t *testing.T) {
	preview := &searchContext{}
	for i := 0; i < 1000; i++ {
		captureSearchHit([]*searchContext{preview}, "a.txt", i+1, "needle")
	}
	if len(preview.records) > maxSearchPreviewHits+1 {
		t.Fatalf("captured %d records", len(preview.records))
	}
	got := NewSearchCode(".").previewSearchHits(Result{Text: strings.Repeat("x", 9000)}, preview, "needle")
	if got.ModelPreview != "" {
		t.Fatal("part of the result was presented as all hits")
	}
}

func TestSearchPreviewRGFailureDiscardsStaleRecords(t *testing.T) {
	dir := t.TempDir()
	file := writeSearchFixture(t, dir, "a.txt", "needle\n")
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "long")
	preview := &searchContext{records: []searchRecord{{path: "stale.txt", line: 1, text: "stale"}}}
	res, err := NewSearchCode(dir).ripgrep(context.Background(), os.Args[0], file, "needle", 1, preview)
	if err != nil || res.Err != nil || len(preview.records) != 1 || preview.records[0].path != file || preview.records[0].text != "needle" {
		t.Fatalf("stale rg data survived fallback: %+v %v %v", preview.records, err, res.Err)
	}
}
