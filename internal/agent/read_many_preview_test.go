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

func TestReadManyBudgetedBatchReachesBothModelRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		name := "native"
		if thin {
			name = "sentinel"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			var reads []string
			for i := 0; i < 12; i++ {
				file := fmt.Sprintf("source%d.txt", i)
				reads = append(reads, file+":1-100")
				if i == 5 {
					continue
				}
				body := strings.Repeat(fmt.Sprintf("source%d %s\n", i, strings.Repeat("x", 90)), 100)
				if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reg := tools.NewRegistry()
			spec := tools.NewReadMany(dir).Spec()
			execute, calls := spec.Fn, 0
			spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
				calls++
				return execute(ctx, args)
			}
			reg.MustRegister(spec)
			reg.MarkAlwaysOn("read_many")
			request := strings.Join(reads, " | ")
			args, _ := json.Marshal(map[string]string{"reads": request})
			call := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "read", Name: "read_many", Arguments: string(args)}}}
			if thin {
				call = []llm.Delta{{Content: "«read_many\nreads: " + request + "»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "batch-read-preview", scripts: [][]llm.Delta{call, {{Content: "Files reviewed, one file is missing.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Read the twelve source files.")) {
				if problem, ok := event.(ErrorEvent); ok {
					t.Fatal(problem.Err)
				}
			}
			if calls != 1 || provider.calls != 2 {
				t.Fatalf("tool calls=%d model calls=%d", calls, provider.calls)
			}
			var preview string
			for _, message := range provider.reqs[1] {
				if message.Role == llm.RoleTool && message.Name == "read_many" {
					preview = message.Content
				}
			}
			// Whole ranges up to the budget, the missing file's error, and a
			// ready continuation for the rest; never a trimmed middle.
			if len(preview) > 32*1024+512 || !strings.Contains(preview, "error: not_found") ||
				!strings.Contains(preview, "[incomplete: ") || !strings.Contains(preview, "Next: read_many reads=") ||
				strings.Contains(preview, "omitted") {
				t.Fatalf("bad provider payload (%d): %s", len(preview), preview)
			}
			if !strings.Contains(preview, "source11.txt:1-100") {
				t.Fatalf("model lost the last file: %s", preview)
			}
		})
	}
}
