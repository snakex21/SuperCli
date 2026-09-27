package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestFailedWorkerHandoffKeepsRecoveryAndPartialReport(t *testing.T) {
	cause := errors.New("provider stream interrupted")
	report := "FIRST_FINDING\n" + strings.Repeat("szczegóły przeglądu\n", 400) + "LAST_FINDING"
	w := &Worker{ID: "worker-3", Agent: "review", Status: "failed", Loop: &Loop{}, LastError: cause.Error(), Runs: 1,
		lastEvidence: "== read_lines config.go ==\n2 | const RetryLimit = 731"}
	result := workerResult(w, report, cause)
	if !errors.Is(result.Err, cause) {
		t.Fatal("lost underlying error")
	}
	reg := tools.NewRegistry()
	reg.EnsureReadOutput()
	visible := reg.ModelResultContent("task", result)
	for _, want := range []string{"worker-3", "failed", "send_message", "FIRST_FINDING", "LAST_FINDING", "Partial report", "RetryLimit = 731"} {
		if !strings.Contains(visible, want) {
			t.Errorf("handoff lost %q: %s", want, visible)
		}
	}
	if len(visible) > 4096 || !utf8.ValidString(visible) {
		t.Fatal("failure handoff is unbounded or invalid UTF-8")
	}
	handle := handleInOutput(visible)
	if handle == "" {
		t.Fatal("full partial report not retained")
	}
	args, _ := json.Marshal(map[string]any{"handle": handle, "query": "szczegóły przeglądu"})
	restored, err := reg.Execute(context.Background(), "read_output", args)
	if err != nil || restored.Err != nil || !strings.Contains(restored.Text, "szczegóły przeglądu") {
		t.Fatalf("cannot retrieve omitted report: %+v / %v", restored, err)
	}
}

func TestWorkerRecoveryDoesNotUrgeResumingStoppedOrBusyWork(t *testing.T) {
	for _, status := range []string{"stopped", "running"} {
		w := &Worker{ID: "worker-2", Agent: "code", Status: status, Loop: &Loop{}}
		result := workerResult(w, "", context.Canceled)
		visible := result.ModelContent()
		if !strings.Contains(visible, "worker-2") || strings.Contains(visible, "send_message") {
			t.Fatalf("unsafe recovery for %s: %s", status, visible)
		}
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatal("lost cancellation cause")
		}
	}
}

func TestFailedWorkerContinuesWithoutDiscoveryOrRereading(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%v", thin), func(t *testing.T) {
			reads := 0
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read fixture", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				reads++
				return tools.Result{Text: "const RetryLimit = 731"}, nil
			}})
			reg.MarkAlwaysOn("read_lines")
			cause := errors.New("provider stream interrupted")
			workerProvider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "read-1", Name: "read_lines", Arguments: "{}"}}, {FinishReason: "tool_calls"}},
				{{Err: cause}},
				{{Content: "RetryLimit is 731; review complete."}, {FinishReason: "stop"}},
			}}
			parent, err := NewLoop(LoopConfig{Provider: workerProvider, Registry: reg, ThinTools: thin, StableToolset: true, CatalogHoist: true})
			if err != nil {
				t.Fatal(err)
			}
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, parent, reg, workerProvider, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			reg.MustRegister(NewSendMessageTool(task.Workers).Spec())
			reg.MarkAlwaysOn("send_message")
			before := parent.buildToolDefs()
			beforePreamble := parent.thinToolsPreamble()
			envelope := "{\"tool\":\"send_message\",\"args\":{\"to\":\"worker-1\",\"message\":\"Finish from the evidence already collected.\"}}"
			if _, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: "invoke_tool", Arguments: envelope}); err == nil {
				t.Fatal("continuation should be dormant before any worker exists")
			}
			result, err := task.execute(context.Background(), json.RawMessage("{\"agent\":\"review\",\"prompt\":\"Inspect the retry limit.\"}"))
			if err != nil || result.Err == nil {
				t.Fatalf("expected worker failure: %+v / %v", result, err)
			}
			if !strings.Contains(result.ModelContent(), "send_message") {
				t.Error("missing continuation guidance")
			}
			call, err := resolveInvokeToolCall(reg, llm.ToolCall{Name: "invoke_tool", Arguments: envelope})
			if err != nil {
				t.Fatalf("continuation needs redundant discovery: %v", err)
			}
			if thin && (!reflect.DeepEqual(before, parent.buildToolDefs()) || beforePreamble != parent.thinToolsPreamble()) {
				t.Fatal("stable tool prefix changed")
			}
			resumed, err := reg.Execute(context.Background(), call.Name, json.RawMessage(call.Arguments))
			if err != nil || resumed.Err != nil || !strings.Contains(resumed.Text, "RetryLimit is 731") {
				t.Fatalf("continuation failed: %+v / %v", resumed, err)
			}
			if reads != 1 || workerProvider.calls != 3 || len(task.Workers.List()) != 1 {
				t.Fatalf("work was restarted: reads=%d model calls=%d workers=%d", reads, workerProvider.calls, len(task.Workers.List()))
			}
			found := false
			for _, m := range workerProvider.reqs[2] {
				if m.Role == llm.RoleTool && strings.Contains(m.Content, "RetryLimit = 731") {
					found = true
				}
			}
			if !found {
				t.Fatal("resumed worker lost previous evidence")
			}
			worker, _ := task.Workers.Get("worker-1")
			if _, exists := worker.Loop.registry.Get("send_message"); exists {
				t.Fatal("continuation leaked into child registry")
			}
		})
	}
}

func TestWorkerRecoveryPreservesExhaustedBudget(t *testing.T) {
	budget := newTokenBudget(1)
	cause := budget.Record(context.Background(), 2, 0, "fixture")
	w := &Worker{ID: "worker-4", Agent: "code", Status: "failed", Loop: &Loop{creditTracker: budget}}
	got := workerResult(w, "", cause).ModelContent()
	if !strings.Contains(got, "token budget exceeded") || strings.Contains(got, "send_message") {
		t.Fatalf("exhausted worker should not be invited to retry: %s", got)
	}
	used, _ := budget.Used()
	if used != 2 || budget.SessionCap() != 1 {
		t.Fatal("handoff changed the worker budget")
	}
}
