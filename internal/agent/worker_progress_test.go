package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestRunWorkerLoopReportsToolActivity(t *testing.T) {
	provider := &stubProvider{
		name: "worker",
		scripts: [][]llm.Delta{
			{
				{Role: llm.RoleAssistant},
				{ToolCall: &llm.ToolCall{ID: "c1", Name: "search_code", Arguments: `{"query":"worker"}`}},
				{FinishReason: "tool_calls"},
			},
			{
				{Role: llm.RoleAssistant, Content: "report"},
				{FinishReason: "stop"},
			},
		},
	}
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{
		Name: "search_code", Description: "search", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "match"}, nil
		},
	})
	loop := makeLoop(t, provider, registry, "")
	w := &Worker{ID: "worker-1", Agent: "explore", Loop: loop}
	var progress []WorkerProgressEvent
	w.progress = func(ev WorkerProgressEvent) { progress = append(progress, ev) }

	text, err := runWorkerLoop(context.Background(), w, "inspect")
	if err != nil || text != "report" {
		t.Fatalf("run = %q, %v", text, err)
	}
	if len(progress) != 2 || progress[0].Kind != "tool_call" || progress[1].Kind != "tool_result" {
		t.Fatalf("progress = %+v", progress)
	}
	if progress[0].TaskID != "worker-1" || progress[0].Tool != "search_code" || progress[1].Output != "match" {
		t.Errorf("progress fields = %+v", progress)
	}
	snapshot := w.Snapshot()
	if len(snapshot.ToolNames) != 1 || snapshot.ToolNames[0] != "search_code" {
		t.Errorf("snapshot tools = %v", snapshot.ToolNames)
	}
	notification := renderWorkerNotification(w, text)
	if !strings.Contains(notification, "<tools>search_code</tools>") {
		t.Errorf("notification missing tool summary: %s", notification)
	}
}

func TestWorkerToolSummaryCountsInFirstSeenOrder(t *testing.T) {
	got := workerToolSummary([]string{"read_lines", "search_code", "read_lines"})
	if got != "read_lines×2, search_code" {
		t.Fatalf("summary = %q", got)
	}
}
