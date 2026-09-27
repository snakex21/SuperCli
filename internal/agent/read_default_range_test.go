package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestReadLinesFileOnlyThroughBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("known project detail\n"), 0600); err != nil {
			t.Fatal(err)
		}
		reg := tools.NewRegistry()
		reg.MustRegister(tools.NewReadLines(root).Spec())
		if thin {
			ensureWorkerDiscovery(reg)
		}
		loop, err := NewLoop(LoopConfig{Provider: echoProvider("read-default"), Registry: reg, ThinTools: thin})
		if err != nil {
			t.Fatal(err)
		}
		call := llm.ToolCall{ID: "live-read", Name: "read_lines", Arguments: `{"file":"README.md"}`}
		if thin {
			call.Name = "invoke_tool"
			call.Arguments = `{"tool":"read_lines","args":{"file":"README.md"}}`
		}
		result := loop.invoke(context.Background(), call, make(chan Event, 8))
		if result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, "known project detail") || !strings.Contains(result.followUps[0].Content, "end of file at line 1") {
			t.Fatalf("thin=%t: %+v", thin, result)
		}
	}
}
