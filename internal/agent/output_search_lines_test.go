package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestCompleteSearchLineReachesBothAgentProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			reg := tools.NewRegistry()
			target := "RetryPolicy " + strings.Repeat("label ", 38) + "effective_limit=6842\n"
			text := strings.Repeat("routine log detail\n", 1000) + target + strings.Repeat("routine log detail\n", 1000)
			handle := handleInOutput(reg.CompactModelOutput("ctx_execute", text))
			if handle == "" {
				t.Fatal("no output handle")
			}
			raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "RetryPolicy"})
			call := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "search", Name: "read_output", Arguments: string(raw)}}}
			if thin {
				call = []llm.Delta{{Content: "«read_output\nhandle: " + handle + "\nquery: RetryPolicy\n»", FinishReason: "stop"}}
			}
			p := &stubProvider{name: "line-excerpt", scripts: [][]llm.Delta{call, {{Content: "6842", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Find the effective limit for RetryPolicy.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if len(p.reqs) != 2 {
				t.Fatalf("requests=%d", len(p.reqs))
			}
			found := false
			for _, m := range p.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "read_output" && strings.Contains(m.Content, target) {
					found = true
				}
			}
			if !found {
				t.Fatal("complete matching line did not reach model")
			}
		})
	}
}
