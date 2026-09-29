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

func TestReadManyRangeListRepairReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha\nbeta\ngamma\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			spec := tools.NewReadMany(root).Spec()
			handler := spec.Fn
			calls := 0
			spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				calls++
				return handler(ctx, args)
			}
			reg.MustRegister(spec)
			reg.MarkAlwaysOn("read_many")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("range-list"), Registry: reg, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"reads": map[string]any{"item": []string{"a.go:3-3", "a.go:1-1"}}}
			raw, _ := json.Marshal(args)
			call := llm.ToolCall{ID: "ranges", Name: "read_many", Arguments: string(raw)}
			if thin {
				raw, _ = json.Marshal(map[string]any{"tool": "read_many", "args": args})
				call.Name = "invoke_tool"
				call.Arguments = string(raw)
			}
			events := make(chan Event, 16)
			outcome := loop.invoke(context.Background(), call, events)
			if outcome.failed || calls != 1 || len(outcome.followUps) != 1 {
				t.Fatalf("repair did not reach model in one dispatch: failed=%v calls=%d results=%+v", outcome.failed, calls, outcome.followUps)
			}
			output := outcome.followUps[0].Content
			gamma, alpha := strings.Index(output, "gamma"), strings.Index(output, "alpha")
			if gamma < 0 || alpha <= gamma || strings.Contains(output, "beta") {
				t.Fatalf("requested ranges/order lost: %s", output)
			}
			close(events)
			results := 0
			for event := range events {
				if result, ok := event.(ToolResultEvent); ok {
					results++
					if result.Err != nil || !strings.Contains(result.Output, "gamma") {
						t.Fatalf("UI lost successful result: %+v", result)
					}
				}
			}
			if results != 1 {
				t.Fatalf("UI result count=%d, want 1", results)
			}
		})
	}
}
