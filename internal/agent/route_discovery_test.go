package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLightTurnDiscoversAndUsesToolWithoutRepeatingRequest(t *testing.T) {
	for _, prompt := range []string{"cześć", "wyjaśnij jak działa fixture"} {
		t.Run(prompt, func(t *testing.T) {
			r := tools.NewRegistry()
			executions := 0
			r.MustRegister(tools.Tool{Name: "fixture_records", Description: "Read fixture records.", ReadOnly: true,
				Schema: "{\"type\":\"object\"}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					executions++
					return tools.Result{Text: "record count: 1"}, nil
				}})
			r.MustRegister(tools.NewToolSearcher(r, nil).Spec())
			r.MarkAlwaysOn("tool_search")
			p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "find", Name: "tool_search", Arguments: "{\"query\":\"fixture_records\"}"}, FinishReason: "tool_calls"}},
				{{ToolCall: &llm.ToolCall{ID: "use", Name: "fixture_records", Arguments: "{}"}, FinishReason: "tool_calls"}},
				{{Content: "One record.", FinishReason: "stop"}},
			}}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: r, System: "project-rules-marker", EnableNavigator: true,
				NavigatorAuto: true, NavigatorKeywordsOnly: true, ThinTools: true, MaxSteps: 4})
			if err != nil {
				t.Fatal(err)
			}
			l.nextCoordinatorAddon = "deferred-repo-context-marker"
			ch, err := l.Run(context.Background(), prompt)
			if err != nil {
				t.Fatal(err)
			}
			for ev := range ch {
				if e, ok := ev.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
				if e, ok := ev.(ToolResultEvent); ok && e.Err != nil {
					t.Fatal(e.Err)
				}
			}
			if len(p.reqs) != 3 || executions != 1 {
				t.Fatalf("requests=%d executions=%d", len(p.reqs), executions)
			}
			if p.toolReqs[0] != 1 || p.toolReqs[1] <= p.toolReqs[0] {
				t.Fatalf("discovery did not expose tools: %v", p.toolReqs)
			}
			for i, req := range p.reqs {
				var text strings.Builder
				users := 0
				for _, m := range req {
					text.WriteString(m.TextOnly().Content)
					if m.Role == llm.RoleUser {
						users++
					}
				}
				if users != 1 {
					t.Fatalf("request %d repeated user prompt", i)
				}
				for _, marker := range []string{"project-rules-marker", "deferred-repo-context-marker"} {
					if strings.Contains(text.String(), marker) != (i > 0) {
						t.Fatalf("request %d wrong context: %s", i, marker)
					}
				}
			}
		})
	}
}

func TestFailedDiscoveryKeepsLightRoute(t *testing.T) {
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{Name: "fixture", Description: "fixture", Schema: "{\"type\":\"object\"}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	r.ActivateDiscovered("fixture")
	l := &Loop{route: RouteChatOnly, registry: r}
	l.continueWithDiscoveredTools([]llm.ToolCall{{Name: "tool_search"}}, []callOutcome{{failed: true}})
	if l.route != RouteChatOnly {
		t.Fatal("failed discovery expanded context")
	}
	l.continueWithDiscoveredTools([]llm.ToolCall{{Name: "recall"}}, []callOutcome{{}})
	if l.route != RouteChatOnly {
		t.Fatal("recall expanded context")
	}
}
