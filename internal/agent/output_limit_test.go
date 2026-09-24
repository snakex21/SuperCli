package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Replay the oversized limits observed in sessions through both model routes.
// The next model request must already contain data, without a correction call.
func TestOversizedOutputReadReachesNextModelRequest(t *testing.T) {
	for _, mode := range []string{"native", "sentinel"} {
		t.Run(mode, func(t *testing.T) {
			reg := tools.NewRegistry()
			preview := reg.CompactModelOutput("ctx_execute", strings.Repeat("stored evidence\n", 2000))
			batch := []llm.Delta{
				{ToolCall: &llm.ToolCall{ID: "first", Name: "read_output", Arguments: `{"handle":"out_000001","limit":12000}`}},
				{ToolCall: &llm.ToolCall{ID: "second", Name: "read_output", Arguments: `{"handle":"out_000001","offset":3072,"limit":14000}`}},
			}
			if mode == "sentinel" {
				batch = []llm.Delta{{Content: "«read_output\nhandle: out_000001\nlimit: 12000»\n«read_output\nhandle: out_000001\noffset: 3072\nlimit: 14000»", FinishReason: "stop"}}
			}
			p := &outputReplayProvider{stubProvider: &stubProvider{name: "output-limit", scripts: [][]llm.Delta{batch, {{Content: "Read complete.", FinishReason: "stop"}}}}, handle: handleInOutput(preview)}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: mode == "sentinel", MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, mustRun(t, l, "Inspect the retained evidence."))
			for _, event := range events {
				if e, ok := event.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if p.calls != 2 {
				t.Fatalf("model requests=%d, want 2", p.calls)
			}
			results := 0
			for _, m := range p.reqs[1] {
				if m.Role != llm.RoleTool {
					continue
				}
				results++
				want := "bytes 0:8192"
				if results == 2 {
					want = "bytes 3072:11264"
				}
				if !strings.Contains(m.Content, want) || !strings.Contains(m.Content, "stored evidence") || strings.HasPrefix(m.Content, "error:") {
					t.Fatalf("model received failure instead of capped data: %s", m.Content)
				}
				if len(m.Content) > 8192+200 {
					t.Fatalf("result escaped cap: %d", len(m.Content))
				}
			}
			if results != 2 {
				t.Fatalf("tool results=%d, want 2", results)
			}
		})
	}
}
