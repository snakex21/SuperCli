package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestPatchWhitespaceEvidenceReachesBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			source := "package cache\n\nfunc check() {\n if !ok || now > e.ExpiresAt { return \"\", false }\n}\n"
			path := filepath.Join(root, "store.go")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewPatchFile(root).Spec())
			reg.MarkAlwaysOn("patch_file")
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("evidence"), Registry: reg, BaseDir: root, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]string{"path": "store.go", "old": "\tif !ok || now > e.ExpiresAt { return \"\", false }", "new": "\tif !ok || now >= e.ExpiresAt { return \"\", false }"}
			raw, _ := json.Marshal(args)
			call := llm.ToolCall{ID: "failed-indent", Name: "patch_file", Arguments: string(raw)}
			if thin {
				raw, _ = json.Marshal(map[string]any{"tool": "patch_file", "args": args})
				call.Name = "invoke_tool"
				call.Arguments = string(raw)
			}
			events := make(chan Event, 16)
			result := loop.invoke(context.Background(), call, events)
			exact := strconv.Quote(" if !ok || now > e.ExpiresAt { return \"\", false }")
			if !result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, exact) {
				t.Fatalf("model did not receive the exact indentation: %+v", result)
			}
			close(events)
			seen := false
			for event := range events {
				if output, ok := event.(ToolResultEvent); ok {
					seen = true
					if output.Err == nil || !strings.Contains(output.Err.Error(), exact) {
						t.Fatalf("UI lost diagnostic: %+v", output)
					}
				}
			}
			if !seen {
				t.Fatal("no tool result event")
			}
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != source {
				t.Fatal("failed patch changed the file")
			}
		})
	}
}
