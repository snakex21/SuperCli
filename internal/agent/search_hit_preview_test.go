package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestSearchHitPreviewReachesBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			dir := t.TempDir()
			for name, body := range map[string]string{
				"a.txt":      strings.Repeat("a", 12000) + " BudgetLimit=1111\n",
				"middle.env": "BudgetLimit=7321\n",
				"z.txt":      "BudgetLimit=9999 " + strings.Repeat("z", 12000) + "\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewSearchCode(dir).Spec())
			reg.MarkAlwaysOn("search_code")
			args, _ := json.Marshal(map[string]any{"query": "BudgetLimit", "context": 0})
			first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "search", Name: "search_code", Arguments: string(args)}}}
			if thin {
				first = []llm.Delta{{Content: "«search_code\nquery: BudgetLimit\ncontext: 0»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "search-hit-preview", scripts: [][]llm.Delta{first, {{Content: "middle.env: 7321", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find BudgetLimit in middle.env.")) {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if provider.calls != 2 {
				t.Fatalf("calls=%d", provider.calls)
			}
			found := false
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "search_code" {
					found = true
					if !strings.Contains(m.Content, "middle.env:1:BudgetLimit=7321") || !strings.Contains(m.Content, "BudgetLimit=1111") || !strings.Contains(m.Content, "handle=out_") || len(m.Content) > 4400 {
						t.Fatalf("lost evidence (%d bytes): %s", len(m.Content), m.Content)
					}
				}
			}
			if !found {
				t.Fatal("missing search result")
			}
		})
	}
}
