package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Measure the actual dispatch path using production schemas and deterministic
// callbacks. Network, inference and disk latency are deliberately excluded.
// Run this unchanged with the frozen old loop_tools.go overlay and the candidate.
func BenchmarkPreparedToolDispatch(b *testing.B) {
	search := tools.NewWebSearch("", "")
	lookup, advanced := search.LookupSpec(), search.Spec()
	export := tools.NewWebDownload(".").Spec()
	plain := tools.Tool{Name: "inspect_resource", ReadOnly: true, Schema: preparedInvocationSchema}
	for _, tc := range []struct {
		name, args string
		tool       tools.Tool
		hit        bool
	}{
		{"plain", `{"query":"example","count":1}`, plain, false},
		{"lookup-miss", `{"query":"example"}`, lookup, false},
		{"lookup-hit", `{"query":"example"}`, lookup, true},
		{"search-miss", `{"query":"example","max_results":"5","freshness":"week","include_domains":["example.org"],"exclude_domains":["example.net"]}`, advanced, false},
		{"search-refresh", `{"query":"example","max_results":"5","refresh":"true"}`, advanced, false},
		{"search-hit", `{"query":"example","max_results":"5","freshness":"week","include_domains":["example.org"],"exclude_domains":["example.net"]}`, advanced, true},
		{"effect-miss", `{"url":"https://example.org/asset.bin","path":"exports/asset.bin","max_bytes":"1024"}`, export, false},
		{"effect-replay", `{"url":"https://example.org/asset.bin","path":"exports/asset.bin","max_bytes":"1024"}`, export, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			executions := 0
			tool := tc.tool
			tool.Description = "deterministic dispatch fixture"
			tool.NextTools = nil
			tool.Verify = func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }
			tool.Fn = func(context.Context, json.RawMessage) (tools.Result, error) {
				executions++
				result := tools.Result{Text: "Verified result"}
				if tc.tool.ReplaySuccess != nil {
					result.Operation = &core.VerifiedOperation{Summary: "Verified output"}
				}
				return result, nil
			}
			if tool.ReplaySuccess != nil {
				tool.ReplaySuccess = func(_ context.Context, _ json.RawMessage, previous tools.Result) (tools.Result, bool) {
					previous.Inert = true
					return previous, true
				}
			}
			reg := tools.NewRegistry()
			reg.MustRegister(tool)
			reg.MarkAlwaysOn(tool.Name)
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, System: "fixture"})
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			out := make(chan Event, 16)
			call := llm.ToolCall{ID: "fixture", Name: tool.Name, Arguments: tc.args}
			invoke := func() {
				if got := loop.invoke(ctx, call, out); got.failed {
					b.Fatalf("fixture failed: %+v", got)
				}
				for len(out) > 0 {
					<-out
				}
			}
			if tc.hit {
				invoke()
			}
			executions = 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Bound repeated mutation bookkeeping in this synthetic benchmark.
				// Both variants use exactly the same reset and completion protocol.
				loop.identicalWrites = identicalSuccessGate{}
				if !tc.hit {
					loop.resultReuse.reset()
					loop.completedOps.reset()
				}
				invoke()
			}
			b.StopTimer()
			wantExecutions := b.N
			if tc.hit {
				wantExecutions = 0
			}
			if executions != wantExecutions {
				b.Fatalf("executions=%d want=%d", executions, wantExecutions)
			}
			b.ReportMetric(float64(executions)/float64(b.N), "Fn/op")
		})
	}
}
