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

func TestReadContextOmittedLineThroughBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%t", thin), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "service"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "service", "publish.go"), []byte("package service\n// recorded read evidence\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			read := tools.NewReadContext(root).Spec()
			fn := read.Fn
			reads := 0
			read.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				reads++
				return fn(ctx, raw)
			}
			reg.MustRegister(read)
			if thin {
				ensureWorkerDiscovery(reg)
			}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("context-default"), Registry: reg, BaseDir: root, ThinTools: thin})
			if err != nil {
				t.Fatal(err)
			}
			call := llm.ToolCall{ID: "recorded-read", Name: "read_context", Arguments: `{"file":"service/publish.go","radius":20}`}
			if thin {
				call.Name = "invoke_tool"
				call.Arguments = `{"tool":"read_context","args":{"file":"service/publish.go","radius":20}}`
			}
			result := loop.invoke(context.Background(), call, make(chan Event, 8))
			if result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, "recorded read evidence") || !strings.Contains(result.followUps[0].Content, "end of file at line 2") {
				t.Fatalf("one-call read failed: %+v", result)
			}
			if reads != 1 {
				t.Fatalf("read executions=%d, want one", reads)
			}
		})
	}
}
