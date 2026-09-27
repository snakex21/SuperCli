package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestSearchContextCapRetainsCapturedLocationsAndContext(t *testing.T) {
	for _, limit := range []int{20, 25} {
		for _, longLine := range []bool{false, true} {
			t.Run(fmt.Sprintf("max=%d/long=%t", limit, longLine), func(t *testing.T) {
				root := t.TempDir()
				for i := 0; i < 24; i++ {
					match := fmt.Sprintf("needle_%02d=zażółć", i)
					if longLine && i == 0 {
						match = strings.Repeat("ą", 3000) + match
					}
					writeSearchFixture(t, root, fmt.Sprintf("f%02d.txt", i), strings.Repeat("before\n", 20)+match+"\n"+strings.Repeat("after\n", 20))
				}
				tool := NewSearchCode(root)
				preview := &searchContext{radius: 20, query: "needle_"}
				locations, err := tool.fallback(context.Background(), root, "needle_", limit, preview)
				if err != nil || locations.Err != nil {
					t.Fatalf("search: %v %v", err, locations.Err)
				}
				result := tool.renderSearchContext(context.Background(), preview, locations)
				if result.Err != nil || !strings.Contains(result.Text, "context capped at 500 lines") || strings.Count(result.Text, " | ") > 500 {
					t.Fatalf("context bound lost: %v", result.Err)
				}
				if !strings.Contains(result.RetainedText, locations.Text) || !strings.Contains(result.RetainedText, result.Text) {
					t.Fatal("stored output must retain both captured locations and the displayed context")
				}
				captured := min(limit, 24)
				for i := 0; i < captured; i++ {
					if !strings.Contains(result.RetainedText, fmt.Sprintf("f%02d.txt:21:", i)) {
						t.Fatalf("captured file %d omitted", i)
					}
				}
				if limit < 24 && (!strings.Contains(result.RetainedText, searchLimitNotice(limit)) || strings.Contains(result.RetainedText, "needle_20")) {
					t.Fatal("captured-match limit lost or exceeded")
				}
				if longLine && !strings.Contains(result.RetainedText, strings.Repeat("ą", 3000)+"needle_00") {
					t.Fatal("original long matching line lost")
				}
				if !utf8.ValidString(result.Text) || !utf8.ValidString(result.RetainedText) {
					t.Fatal("UTF-8 corrupted")
				}
				store := core.NewOutputStore()
				view := store.ModelContent("search_code", result)
				handle := core.StoredOutputHandle(view)
				if handle == "" {
					t.Fatal("no retrieval handle")
				}
				raw, _ := json.Marshal(map[string]any{"handle": handle, "query": fmt.Sprintf("needle_%02d", captured-1)})
				saved, err := store.ReadOutputTool().Fn(context.Background(), raw)
				if err != nil || saved.Err != nil || !strings.Contains(saved.Text, fmt.Sprintf("f%02d.txt:21:needle_%02d=zażółć", captured-1, captured-1)) {
					t.Fatalf("last location not retrievable: %v %+v", err, saved)
				}
				t.Logf("captured=%d context=%d retained=%d model=%d bytes", captured, len(result.Text), len(result.RetainedText), len(view))
			})
		}
	}
}

func TestUncappedSearchContextDoesNotAddStoredPayload(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "one.txt", "before\nneedle\nafter\n")
	result, err := NewSearchCode(root).run(context.Background(), json.RawMessage(`{"query":"needle","context":1}`))
	if err != nil || result.Err != nil || result.RetainedText != "" || result.ModelPreview != "" || !strings.Contains(result.Text, "before") || !strings.Contains(result.Text, "after") {
		t.Fatalf("ordinary context changed: %v %+v", err, result)
	}
}
