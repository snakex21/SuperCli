package agent

// Phase telemetry tests ("ekonomia tur" pakiet 2, krok 1): the loop
// records one stats turn per step with a phase timing breakdown and
// the raw tool-calls-per-step counter — the metric that decides
// whether read-only tool parallelism is worth building.

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

// echoToolRegistry returns a registry with a trivial always-on tool.
func echoToolRegistry(t *testing.T) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "echo",
		Description: "echo",
		Schema:      `{"type":"object","properties":{"msg":{"type":"string"}}}`,
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "ok"}, nil
		},
	})
	reg.MarkAlwaysOn("echo")
	return reg
}

func makeStatsLoop(t *testing.T, p llm.Provider, reg *tools.Registry, rec stats.Recorder) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 5,
		Stats:    rec,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	return l
}

func echoCall(id string) llm.Delta {
	return llm.Delta{ToolCall: &llm.ToolCall{ID: id, Name: "echo", Arguments: `{"msg":"hi"}`}}
}

// TestLoop_Stats_PhasesRecorded proves a plain text turn (zero tool
// calls) produces exactly one stats turn carrying the wall-clock
// phases, the provider-reported tokens, and ToolCalls == 0.
func TestLoop_Stats_PhasesRecorded(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{{
			{Role: llm.RoleAssistant, Content: "hello"},
			{FinishReason: "stop", Usage: &llm.Usage{Input: 7, Output: 3, Total: 10}},
		}},
	}
	rec := stats.NewMemory()
	l := makeStatsLoop(t, p, echoToolRegistry(t), rec)
	ch, err := l.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	turns := rec.Snapshot()
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	tr := turns[0]
	if tr.Step != 1 {
		t.Fatalf("Step = %d, want 1", tr.Step)
	}
	if tr.ToolCalls != 0 {
		t.Fatalf("ToolCalls = %d, want 0", tr.ToolCalls)
	}
	if tr.TokensIn != 7 || tr.TokensOut != 3 {
		t.Fatalf("tokens = %d/%d, want 7/3", tr.TokensIn, tr.TokensOut)
	}
	for _, phase := range []string{
		stats.PhaseContextPrepare,
		stats.PhaseRequestEncode,
		stats.PhaseBackendWait,
		stats.PhaseStreamTotal,
		stats.PhaseNextTurnPrepare,
	} {
		if _, ok := tr.Phases[phase]; !ok {
			t.Errorf("phase %q missing from %v", phase, tr.Phases)
		}
	}
	// No tool calls → no tool_execution phase for this step.
	if _, ok := tr.Phases[stats.PhaseToolExecution]; ok {
		t.Errorf("unexpected tool_execution phase on a text-only step: %v", tr.Phases)
	}
}

// TestLoop_Stats_ToolCallCounts proves the per-step tool-call counter
// for the 1-call and N-call shapes, the per-tool phase entries, and
// the tool_execution wall phase.
func TestLoop_Stats_ToolCallCounts(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{ // step 1: a batch of three calls in ONE step
				echoCall("c1"), echoCall("c2"), echoCall("c3"),
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
			{ // step 2: a single call
				echoCall("c4"),
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
			{ // step 3: done
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
		},
	}
	rec := stats.NewMemory()
	l := makeStatsLoop(t, p, echoToolRegistry(t), rec)
	ch, err := l.Run(context.Background(), "use tools")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drainEvents(t, ch)

	turns := rec.Snapshot()
	if len(turns) != 3 {
		t.Fatalf("turns = %d, want 3 (steps in this run)", len(turns))
	}
	wantCalls := []int{3, 1, 0}
	for i, want := range wantCalls {
		if turns[i].ToolCalls != want {
			t.Errorf("step %d ToolCalls = %d, want %d", i+1, turns[i].ToolCalls, want)
		}
	}
	for i := 0; i < 2; i++ {
		if _, ok := turns[i].Phases[stats.PhaseToolExecution]; !ok {
			t.Errorf("step %d: tool_execution phase missing: %v", i+1, turns[i].Phases)
		}
		if _, ok := turns[i].Phases["tool:echo"]; !ok {
			t.Errorf("step %d: per-tool phase tool:echo missing: %v", i+1, turns[i].Phases)
		}
	}
	total := stats.Sum(turns)
	if total.ToolCalls != 4 {
		t.Errorf("Total.ToolCalls = %d, want 4", total.ToolCalls)
	}
	if total.MultiCall != 1 {
		t.Errorf("Total.MultiCall = %d, want 1 (only the 3-call step)", total.MultiCall)
	}
}

// TestLoop_Stats_NilRecorderNoPanic proves the loop runs a tool-using
// turn end to end with telemetry disabled (nil Stats — the default
// for child workers and most tests).
func TestLoop_Stats_NilRecorderNoPanic(t *testing.T) {
	p := &stubProvider{
		name: "m",
		scripts: [][]llm.Delta{
			{
				echoCall("c1"),
				{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
			{
				{Role: llm.RoleAssistant, Content: "done"},
				{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}},
			},
		},
	}
	l := makeStatsLoop(t, p, echoToolRegistry(t), nil)
	ch, err := l.Run(context.Background(), "use tools")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := drainEvents(t, ch)
	for _, ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("unexpected error event: %v", e.Err)
		}
	}
}
