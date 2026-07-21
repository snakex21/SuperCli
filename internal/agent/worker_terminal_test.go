package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestRunWorkerLoop_RetainsFailedRunAccountingAndStripsThinking(t *testing.T) {
	provider := &stubProvider{
		name: "worker",
		scripts: [][]llm.Delta{{
			{Content: "<thinking>probing paths</thinking>"},
			{ToolCall: &llm.ToolCall{ID: "loop", Name: "noop", Arguments: `{}`}},
			{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 3, Output: 2, Total: 5}},
		}},
	}
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{
		Name: "noop", Description: "noop", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "ok"}, nil
		},
	})
	loop, err := NewLoop(LoopConfig{
		Provider: provider, Registry: registry, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := &Worker{ID: "worker-1", Agent: "code", Loop: loop}

	result, runErr := runWorkerLoop(context.Background(), worker, "do it")
	if runErr == nil || !strings.Contains(runErr.Error(), "max steps") {
		t.Fatalf("run error = %v, want max steps", runErr)
	}
	if result != "" || strings.Contains(result, "<thinking>") {
		t.Fatalf("failed worker leaked reasoning as result: %q", result)
	}
	snapshot := worker.Snapshot()
	if snapshot.Steps != 2 || snapshot.TokensIn != 6 || snapshot.TokensOut != 4 {
		t.Fatalf("worker accounting = %+v", snapshot)
	}
	summary := workerSummary(worker)
	if !strings.Contains(summary, "2 steps") || !strings.Contains(summary, "send_message to worker-1") {
		t.Fatalf("worker summary lacks recovery guidance: %q", summary)
	}
}
