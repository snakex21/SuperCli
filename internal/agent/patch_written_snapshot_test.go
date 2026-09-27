package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
)

func TestPatchWrittenSnapshotReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "value.go"), []byte("package demo\nconst Value = 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			reg.MarkAlwaysOn("patch_file")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("snapshot"), Registry: reg, BaseDir: root, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"path": "value.go", "old": "Value = 1", "new": "Value = 2"}
			raw, _ := json.Marshal(args)
			call := llm.ToolCall{ID: "write", Name: "patch_file", Arguments: string(raw)}
			if thin {
				raw, _ = json.Marshal(map[string]any{"tool": "patch_file", "args": args})
				call.Name = "invoke_tool"
				call.Arguments = string(raw)
			}
			events := make(chan Event, 16)
			outcome := loop.invoke(context.Background(), call, events)
			if outcome.failed || len(outcome.followUps) != 1 {
				t.Fatalf("failed patch: %+v", outcome)
			}
			content := outcome.followUps[0].Content
			if !strings.Contains(content, "[Written snapshot]") || !strings.Contains(content, "2 | const Value = 2") || strings.Contains(content, "Value = 1") {
				t.Fatalf("model lost written data: %s", content)
			}
			close(events)
			seen := false
			for event := range events {
				if result, ok := event.(ToolResultEvent); ok {
					seen = true
					if result.Err != nil || !strings.Contains(result.Output, "2 | const Value = 2") {
						t.Fatalf("UI lost written data: %+v", result)
					}
				}
			}
			if !seen {
				t.Fatal("missing UI result")
			}
		})
	}
}
