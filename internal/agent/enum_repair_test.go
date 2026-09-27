package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestEnumRepairHintReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			reg := tools.NewRegistry()
			// The real schema reproduces the saved failure without touching memory.
			reg.MustRegister(tools.NewRemember(nil).Spec())
			reg.MarkAlwaysOn("remember")
			calls := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "bad", Name: "remember", Arguments: `{"text":"Synthetic project decision.","scope":"project","type":"project"}`}}}
			if thin {
				calls = []llm.Delta{{Content: "«remember\ntext: Synthetic project decision.\nscope: project\ntype: project\n»", FinishReason: "stop"}}
			}
			p := &stubProvider{name: "enum-repair", scripts: [][]llm.Delta{calls, {{Content: "The type needs correcting.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			events, err := loop.Run(context.Background(), "Remember the project decision.")
			if err != nil {
				t.Fatal(err)
			}
			for event := range events {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if len(p.reqs) != 2 {
				t.Fatalf("unexpected automatic requests: %d", len(p.reqs))
			}
			var evidence string
			for _, message := range p.reqs[1] {
				if message.Role == llm.RoleTool && message.Name == "remember" {
					evidence = message.Content
				}
			}
			if !strings.Contains(evidence, `allowed values: ["fact","decision","task-log","preference"]`) {
				t.Fatalf("repair choices did not reach the model: %s", evidence)
			}
			t.Logf("repair result bytes=%d", len(evidence))
		})
	}
}
