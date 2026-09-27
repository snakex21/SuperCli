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

func TestMissingReadSiblingChoicesReachBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, name := range []string{"read_lines", "read_context", "read_many"} {
			t.Run(fmt.Sprintf("%s/thin=%t", name, thin), func(t *testing.T) {
				root := t.TempDir()
				for _, file := range []string{"protocol.go", "relay.go"} {
					if err := os.WriteFile(filepath.Join(root, file), []byte("do not read this automatically\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewReadLines(root).Spec())
				reg.MustRegister(tools.NewReadContext(root).Spec())
				reg.MustRegister(tools.NewReadMany(root).Spec())
				reg.MarkAlwaysOn(name)
				if thin {
					ensureWorkerDiscovery(reg)
				}
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("missing-read"), Registry: reg, ThinTools: thin})
				if err != nil {
					t.Fatal(err)
				}
				args := map[string]any{"file": "api.go", "from": 1, "to": 300}
				if name == "read_context" {
					args = map[string]any{"file": "api.go", "line": 1, "radius": 1}
				}
				if name == "read_many" {
					args = map[string]any{"reads": "api.go:1-300"}
				}
				raw, _ := json.Marshal(args)
				call := llm.ToolCall{ID: "missing", Name: name, Arguments: string(raw)}
				if thin {
					raw, _ = json.Marshal(map[string]any{"tool": name, "args": args})
					call.Name = "invoke_tool"
					call.Arguments = string(raw)
				}
				events := make(chan Event, 16)
				outcome := loop.invoke(context.Background(), call, events)
				if !outcome.failed || len(outcome.followUps) != 1 {
					t.Fatalf("missing read not a single failure: %+v", outcome)
				}
				text := outcome.followUps[0].Content
				if !strings.Contains(text, `"protocol.go", "relay.go"`) || !strings.Contains(text, "not_found") {
					t.Fatalf("model lost choices: %s", text)
				}
				if strings.Contains(text, "do not read this automatically") {
					t.Fatal("silently read an alternative")
				}
				close(events)
				seen := false
				for event := range events {
					if result, ok := event.(ToolResultEvent); ok {
						seen = true
						if result.Err == nil || !strings.Contains(result.Output+fmt.Sprint(result.Err), "protocol.go") {
							t.Fatalf("UI lost failed-read recovery: %+v", result)
						}
					}
				}
				if !seen {
					t.Fatal("no UI result")
				}
			})
		}
	}
}
