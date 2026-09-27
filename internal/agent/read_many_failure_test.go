package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestAllFailedReadManyReachesBothProtocolsAsFailure(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadMany(t.TempDir()).Spec())
			reg.MarkAlwaysOn("read_many")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("batch-error"), Registry: reg, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"reads": "missing_a.go:1-20 | missing_b.go:1-20"}
			raw, _ := json.Marshal(args)
			call := llm.ToolCall{ID: "missing", Name: "read_many", Arguments: string(raw)}
			if thin {
				raw, _ = json.Marshal(map[string]any{"tool": "read_many", "args": args})
				call.Name = "invoke_tool"
				call.Arguments = string(raw)
			}
			events := make(chan Event, 16)
			outcome := loop.invoke(context.Background(), call, events)
			if !outcome.failed || outcome.observation.valid {
				t.Fatal("failed batch treated as successful progress")
			}
			if len(outcome.followUps) != 1 || !strings.HasPrefix(outcome.followUps[0].Content, "error:") || !strings.Contains(outcome.followUps[0].Content, "0 ok, 2 failed") {
				t.Fatalf("model lost failure: %+v", outcome.followUps)
			}
			close(events)
			seen := false
			for event := range events {
				if result, ok := event.(ToolResultEvent); ok {
					seen = true
					if result.Err == nil || !strings.Contains(result.Output, "missing_a.go") {
						t.Fatal("UI received success or lost diagnostics")
					}
				}
			}
			if !seen {
				t.Fatal("missing tool result event")
			}
		})
	}
}
