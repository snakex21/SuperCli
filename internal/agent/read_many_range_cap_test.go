package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestOversizedBatchReachesModelInBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			var body strings.Builder
			for n := 1; n <= 650; n++ {
				fmt.Fprintf(&body, "v%d\n", n)
			}
			if err := os.WriteFile(filepath.Join(root, "netplay.go"), []byte(body.String()), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadMany(root).Spec())
			reg.MarkAlwaysOn("read_many")
			calls := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "batch", Name: "read_many", Arguments: `{"reads":"netplay.go:1-300 | netplay.go:300-600"}`}}}
			if thin {
				calls = []llm.Delta{{Content: "«read_many\nreads: netplay.go:1-300 | netplay.go:300-600\n»", FinishReason: "stop"}}
			}
			p := &stubProvider{name: "batch-range", scripts: [][]llm.Delta{calls, {{Content: "Read the bounded ranges.", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, BaseDir: root, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			events, err := loop.Run(context.Background(), "Read the requested ranges.")
			if err != nil {
				t.Fatal(err)
			}
			for e := range events {
				if failure, ok := e.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if len(p.reqs) != 2 {
				t.Fatalf("requests=%d", len(p.reqs))
			}
			var evidence string
			for _, m := range p.reqs[1] {
				if m.Role == llm.RoleTool && m.Name == "read_many" {
					evidence = m.Content
				}
			}
			for _, want := range []string{" 599 | v599", "requested lines 600-600 not read", "2 ok, 0 failed"} {
				if !strings.Contains(evidence, want) || strings.Contains(evidence, "error:") {
					t.Fatalf("missing %q in model evidence: %q", want, evidence)
				}
			}
		})
	}
}
