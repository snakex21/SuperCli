package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

func TestContextOriginNativeThinProducerIntegration(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "thin"}[thin], func(t *testing.T) {
			rec := stats.NewMemory()
			provider := &stubProvider{name: "context-fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "read", Name: "fixture_read", Arguments: `{}`}, FinishReason: "tool_calls"}},
				{{ToolCall: &llm.ToolCall{ID: "task", Name: "task", Arguments: `{}`}, FinishReason: "tool_calls"}},
				{{Content: "done", FinishReason: "stop"}},
			}}
			reg := tools.NewRegistry()
			for _, name := range []string{"fixture_read", "task"} {
				reg.MustRegister(tools.Tool{Name: name, Description: "Fixture only", Schema: `{"type":"object"}`, ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "same report 🙂"}, nil
				}})
				reg.MarkAlwaysOn(name)
			}
			metered := llm.Metered(provider, "fixture", llm.PurposeMain, func(s llm.CallStat) { rec.RecordCall(stats.Call{Context: llm.CloneContextUsage(&s.Context)}) })
			loop, err := NewLoop(LoopConfig{Provider: metered, Registry: reg, System: "fixed instructions", Stats: rec, ThinTools: thin, StableToolset: thin, CatalogHoist: thin})
			if err != nil {
				t.Fatal(err)
			}
			loop.SetNextUserAddon("automatic preflight 東京")
			drainEvents(t, mustRun(t, loop, "[memory_briefing] human text, not a reminder"))
			u := stats.SumContextUsage(rec.Snapshot(), rec.Calls())
			if u == nil || u.MeasuredCalls != 3 || u.LogicalCalls != 3 || u.Reasons[llm.ReasonUserInput] != 1 || u.Reasons[llm.ReasonToolResults] != 2 {
				t.Fatalf("calls=%+v", u)
			}
			for _, source := range []llm.ContextSource{llm.SourceHuman, llm.SourceRuntime, llm.SourceToolResult, llm.SourceTaskResult} {
				if u.Sources[source].AddedItems == 0 || u.Sources[source].Exposures == 0 {
					t.Fatalf("missing source %s: %+v", source, u)
				}
			}
			if u.Sources[llm.SourceMemory].TextBytes != 0 || u.Sources[llm.SourceWorkerNotification].TextBytes != 0 {
				t.Fatal("text content spoofed provenance")
			}
			if len(provider.reqs) != 3 {
				t.Fatal("measurement created model calls")
			}
		})
	}
}
