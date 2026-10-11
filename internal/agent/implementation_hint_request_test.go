package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestImplementationGuidanceDoesNotAccumulateOrLeakIntoReadOnlyTurn(t *testing.T) {
	for _, thin := range []bool{false, true} {
		provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
			{{Content: "first answer", FinishReason: "stop"}},
			{{Content: "second answer", FinishReason: "stop"}},
			{{Content: "explanation", FinishReason: "stop"}},
		}}
		loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), System: "fixed instructions", ThinTools: thin, StableToolset: true, CatalogHoist: thin})
		if err != nil {
			t.Fatal(err)
		}
		prompts := []string{"Fix the first defect.", "Fix the next defect.", "Explain the code; do not edit files."}
		for turn, prompt := range prompts {
			drainEvents(t, mustRun(t, loop, prompt))
			if len(provider.reqs) != turn+1 {
				t.Fatal("guidance added model calls")
			}
			request := provider.reqs[turn]
			count := 0
			for _, message := range request {
				count += strings.Count(message.Content, implementationVerificationInstruction)
			}
			want := 1
			if turn == 2 {
				want = 0
			}
			if count != want || (want == 1 && !strings.Contains(request[len(request)-1].Content, implementationVerificationInstruction)) {
				t.Fatalf("thin=%v turn=%d guidance copies=%d, want=%d", thin, turn, count, want)
			}
			for _, message := range loop.AllMessages() {
				if strings.Contains(message.TextOnly().Content, implementationVerificationInstruction) {
					t.Fatal("automatic instruction persisted in canonical history")
				}
			}
			if request[0].Content != provider.reqs[0][0].Content {
				t.Fatal("current-turn guidance rewrote the leading system prefix")
			}
		}
	}
}
