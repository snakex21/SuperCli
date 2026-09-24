package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSearchContextKeepsLateMatchInBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			body := "before\n" + strings.Repeat("x", 9000) + " RetryPolicy=6842\nafter\n"
			if err := os.WriteFile(filepath.Join(root, "settings.txt"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			registry := tools.NewRegistry()
			spec := tools.NewSearchCode(root).Spec()
			registry.MustRegister(spec)
			registry.MarkAlwaysOn("search_code")
			arg, _ := json.Marshal(map[string]any{"query": "RetryPolicy", "context": 1})
			result, err := spec.Fn(context.Background(), arg)
			if err != nil || result.Err != nil || len(result.Text) > 12288 || result.RetainedText == "" || result.ModelPreview != "" {
				t.Fatalf("bad fixture: bytes=%d %v %+v", len(result.Text), err, result.Err)
			}
			call := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "batch", Name: "search_code", Arguments: string(arg)}}}
			if thin {
				call = []llm.Delta{{Content: "«search_code\nquery: RetryPolicy\ncontext: 1\n»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "batch-middle", scripts: [][]llm.Delta{call, {{Content: "Values found.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Read the files and report their values.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if len(provider.reqs) != 2 {
				t.Fatalf("requests=%d", len(provider.reqs))
			}
			var text string
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "search_code" {
					text = m.Content
				}
			}
			if !strings.HasPrefix(text, result.Text) {
				t.Fatalf("requested batch was compacted: model=%d full=%d", len(text), len(result.Text))
			}
			if !strings.Contains(text, "RetryPolicy=6842") {
				t.Fatal("lost middle evidence")
			}
		})
	}
}
