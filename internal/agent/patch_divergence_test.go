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

func TestPatchDivergenceDiagnosticReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			prefix := strings.Repeat("same words ", 60)
			current := prefix + "[reference](reference.md) for details."
			path := filepath.Join(root, "README.md")
			if err := os.WriteFile(path, []byte(current+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			reg.MarkAlwaysOn("patch_file")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("diagnostic"), Registry: reg, BaseDir: root, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"path": "README.md", "old": prefix + "reference.md for details.", "new": "replacement"}
			raw, _ := json.Marshal(args)
			call := llm.ToolCall{ID: "rejected", Name: "patch_file", Arguments: string(raw)}
			if thin {
				raw, _ = json.Marshal(map[string]any{"tool": "patch_file", "args": args})
				call.Name = "invoke_tool"
				call.Arguments = string(raw)
			}
			events := make(chan Event, 16)
			outcome := loop.invoke(context.Background(), call, events)
			if !outcome.failed || len(outcome.followUps) != 1 {
				t.Fatalf("wrong status: %+v", outcome)
			}
			if !strings.Contains(outcome.followUps[0].Content, "[reference](reference.md)") {
				t.Fatalf("model cannot see mismatch: %s", outcome.followUps[0].Content)
			}
			close(events)
			seen := false
			for event := range events {
				if result, ok := event.(ToolResultEvent); ok {
					seen = true
					if result.Err == nil || !strings.Contains(result.Err.Error(), "[reference](reference.md)") {
						t.Fatalf("UI cannot see mismatch: %+v", result)
					}
				}
			}
			if !seen {
				t.Fatal("missing result event")
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != current+"\n" {
				t.Fatal("rejected edit changed file")
			}
		})
	}
}
