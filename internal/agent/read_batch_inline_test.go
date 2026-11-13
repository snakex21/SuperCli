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

func TestModerateReadBatchKeepsMiddleEvidenceInBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			var ranges []string
			for i := 0; i < 3; i++ {
				var body strings.Builder
				for n := 1; n <= 70; n++ {
					if n == 35 {
						fmt.Fprintf(&body, "ImportantValue=unique-%d\n", i)
					} else {
						fmt.Fprintf(&body, "padding_%02d=unused_context_abcdefghijkl\n", n)
					}
				}
				name := fmt.Sprintf("file%d.env", i)
				if err := os.WriteFile(filepath.Join(root, name), []byte(body.String()), 0600); err != nil {
					t.Fatal(err)
				}
				ranges = append(ranges, name+":1-70")
			}
			registry := tools.NewRegistry()
			spec := tools.NewReadMany(root).Spec()
			registry.MustRegister(spec)
			registry.MarkAlwaysOn("read_many")
			arg, _ := json.Marshal(map[string]string{"reads": strings.Join(ranges, " | ")})
			result, err := spec.Fn(context.Background(), arg)
			if err != nil || result.Err != nil || len(result.Text) <= 8192 || len(result.Text) > 12288 {
				t.Fatalf("bad fixture: bytes=%d %v %+v", len(result.Text), err, result.Err)
			}
			call := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "batch", Name: "read_many", Arguments: string(arg)}}}
			if thin {
				call = []llm.Delta{{Content: "«read_many\nreads: " + strings.Join(ranges, " | ") + "\n»", FinishReason: "stop"}}
			}
			provider := &stubProvider{name: "batch-middle", scripts: [][]llm.Delta{call, {{Content: "Values found.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range drainEvents(t, mustRun(t, loop, "Read the files and report their values.")) {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if len(provider.reqs) != 2 {
				t.Fatalf("requests=%d", len(provider.reqs))
			}
			var text string
			for _, m := range provider.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "read_many" {
					text = m.Content
				}
			}
			if text != result.Text {
				t.Fatalf("requested batch was compacted: model=%d full=%d", len(text), len(result.Text))
			}
			for i := 0; i < 3; i++ {
				if !strings.Contains(text, fmt.Sprintf("ImportantValue=unique-%d", i)) {
					t.Fatalf("lost middle of file %d", i)
				}
			}
		})
	}
}
