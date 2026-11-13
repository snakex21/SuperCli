package search

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/tools/browser"
	"supercli/internal/tools/web"
)

// Models ask for "browser tool" or "use the browser"; a tool name wrapped in
// generic words must activate that tool in one call, not list candidates.
func TestToolNameWithGenericWordsActivatesTheTool(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(web.NewWebFetch().Spec())
	reg.MustRegister(browser.New().Spec())
	searcher := NewToolSearcher(reg, nil)
	for _, query := range []string{"browser tool", "use the browser", "narzędzie browser", "Browser"} {
		args, _ := json.Marshal(map[string]any{"query": query})
		result, err := searcher.Spec().Fn(context.Background(), args)
		var got discoveryResponse
		if err != nil || result.Err != nil || json.Unmarshal([]byte(result.Text), &got) != nil ||
			len(got.Matches) != 1 || got.Matches[0].Name != "browser" || !reg.IsVisible("browser") {
			t.Fatalf("%q did not activate browser: %s %v", query, result.Text, err)
		}
	}
	args, _ := json.Marshal(map[string]any{"query": "browser fetch tool"})
	result, _ := searcher.Spec().Fn(context.Background(), args)
	var got discoveryResponse
	_ = json.Unmarshal([]byte(result.Text), &got)
	if len(got.Matches) == 1 && got.Matches[0].Name == "browser" && got.Matches[0].Score == 1 {
		t.Fatalf("two meaningful words must not be treated as an exact name: %s", result.Text)
	}
}
