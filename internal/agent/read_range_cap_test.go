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

func TestOversizedReadReturnsEvidenceInBothProtocols(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprint(thin), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "limits.txt"), []byte(strings.Repeat("padding\n", 24)+"RetryLimit=6842\n"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tools.NewReadLines(root).Spec())
			reg.MarkAlwaysOn("read_lines")
			calls := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "read", Name: "read_lines", Arguments: "{\"file\":\"limits.txt\",\"from\":21,\"to\":521}"}}}
			if thin {
				calls = []llm.Delta{{Content: "«read_lines\nfile: limits.txt\nfrom: 21\nto: 521\n»", FinishReason: "stop"}}
			}
			p := &stubProvider{name: "capped-read", scripts: [][]llm.Delta{calls, {{Content: "6842", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, BaseDir: root, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			events, err := loop.Run(context.Background(), "Read the requested range.")
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
				if m.Role == llm.RoleTool && m.Name == "read_lines" {
					evidence = m.Content
				}
			}
			if !strings.Contains(evidence, "RetryLimit=6842") || !strings.Contains(evidence, "end of file at line 25") || strings.Contains(evidence, "error:") {
				t.Fatalf("missing evidence: %q", evidence)
			}
		})
	}
}
