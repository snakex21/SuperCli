package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

type notificationRequestTrace struct {
	HumanTurns    int
	WorkerReports int
	ToolResults   int
	MessageBytes  int
	ApproxTokens  int
}

func traceNotificationRequest(messages []llm.Message) notificationRequestTrace {
	trace := notificationRequestTrace{ApproxTokens: llm.EstimateTokens(messages)}
	for _, message := range messages {
		trace.MessageBytes += len(message.Content)
		if message.Role == llm.RoleTool {
			trace.ToolResults++
		}
		if strings.HasPrefix(message.Content, "<task-notification>") && (message.Role == llm.RoleUser || message.Role == llm.RoleTool) {
			trace.WorkerReports++
		} else if message.Role == llm.RoleUser {
			trace.HumanTurns++
		}
	}
	return trace
}

func requestTraceWorkerNotice(number int) string {
	return fmt.Sprintf("<task-notification>\n<task-id>worker-%d</task-id>\n<agent>review</agent>\n<status>done</status>\n<summary>Checked fixture</summary>\n<result>REPORT_%d: fixture checked; no remaining issue.</result>\n</task-notification>", number, number)
}

// These are real Loop.Run -> provider.Complete requests, with deterministic
// completion timing. There are no network calls, sleeps, model inference or UI.
func TestWorkerNotificationRequestTrace(t *testing.T) {
	for _, mode := range []string{"baseline", "progress_only", "during_tool", "during_final", "two_during_final", "after_done"} {
		t.Run(mode, func(t *testing.T) {
			registry := tools.NewRegistry()
			provider := &stubProvider{name: "request-trace", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "inspect-1", Name: "inspect_fixture", Arguments: `{"pass":1}`}, FinishReason: "tool_calls"}},
				{{ToolCall: &llm.ToolCall{ID: "inspect-2", Name: "inspect_fixture", Arguments: `{"pass":2}`}, FinishReason: "tool_calls"}},
				{{ToolCall: &llm.ToolCall{ID: "inspect-3", Name: "inspect_fixture", Arguments: `{"pass":3}`}, FinishReason: "tool_calls"}},
				{{Content: "Inspection complete.", FinishReason: "stop"}},
			}}
			parent, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Stable fixture policy", MaxSteps: 8})
			if err != nil {
				t.Fatal(err)
			}
			external := make(chan Event, 16)
			parent.SetExternalSink(external)
			var deliver func(context.Context, string)
			var workerContext context.Context
			var finish func()
			toolCalls := 0
			registry.MustRegister(tools.Tool{
				Name: "inspect_fixture", Description: "Read an isolated fixture", Schema: `{"type":"object"}`, ReadOnly: true,
				Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
					toolCalls++
					if mode == "progress_only" {
						parent.Emit(WorkerProgressEvent{TaskID: "worker-1", Agent: "review", Kind: "tool_result", Tool: "read_lines", Output: "UI_PROGRESS_ONLY"})
					}
					if toolCalls == 1 && mode != "baseline" && mode != "progress_only" {
						deliver, workerContext = parent.backgroundDelivery(ctx), ctx
						if mode != "after_done" {
							finish = backgroundRunFrom(ctx).start()
						}
						if mode == "during_tool" {
							deliver(ctx, requestTraceWorkerNotice(1))
							finish()
						}
					}
					return tools.Result{Text: fmt.Sprintf("Fixture pass %d checked.", toolCalls)}, nil
				},
			})
			registry.MarkAlwaysOn("inspect_fixture")
			var traces []notificationRequestTrace
			provider.onCalled = func(index int) {
				traces = append(traces, traceNotificationRequest(provider.reqs[index]))
				for _, message := range provider.reqs[index] {
					if strings.Contains(message.Content, "UI_PROGRESS_ONLY") {
						t.Error("UI progress leaked into the provider request")
					}
				}
				if index == 3 && (mode == "during_final" || mode == "two_during_final") {
					deliver(workerContext, requestTraceWorkerNotice(1))
					if mode == "two_during_final" {
						deliver(workerContext, requestTraceWorkerNotice(2))
					}
					finish()
				}
			}
			done := 0
			for _, event := range drainEvents(t, mustRun(t, parent, "Inspect the fixture with three separate reads, then report findings.")) {
				switch event := event.(type) {
				case DoneEvent:
					done++
				case ErrorEvent:
					t.Fatal(event.Err)
				}
			}
			wantCalls := 4
			if mode == "during_final" || mode == "two_during_final" {
				wantCalls = 5 // one synthesis after the previously unseen report(s)
			}
			if done != 1 || toolCalls != 3 || len(traces) != wantCalls {
				t.Fatalf("unexpected continuation: done=%d tools=%d requests=%d want=%d", done, toolCalls, len(traces), wantCalls)
			}
			for index, trace := range traces {
				wantReports := 0
				if mode == "during_tool" && index > 0 || mode == "during_final" && index == 4 {
					wantReports = 1
				} else if mode == "two_during_final" && index == 4 {
					wantReports = 2
				}
				if trace.HumanTurns != 1 || trace.WorkerReports != wantReports {
					t.Fatalf("request %d duplicated a report or created a human turn: %+v, want reports=%d", index+1, trace, wantReports)
				}
				t.Logf("request=%d human_turns=%d worker_reports=%d tool_results=%d message_bytes=%d approx_message_tokens=%d", index+1, trace.HumanTurns, trace.WorkerReports, trace.ToolResults, trace.MessageBytes, trace.ApproxTokens)
			}
			if mode == "after_done" {
				before := len(provider.reqs)
				deliver(workerContext, requestTraceWorkerNotice(1))
				if len(provider.reqs) != before {
					t.Fatal("idle worker report autonomously started a model request")
				}
				for _, event := range drainEvents(t, mustRun(t, parent, "Report the inspection status.")) {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
				last := traces[len(traces)-1]
				if len(traces) != before+1 || last.HumanTurns != 2 || last.WorkerReports != 1 {
					t.Fatalf("next human request did not retain exactly one report: %+v", traces)
				}
				t.Logf("after idle delivery: no autonomous request; next human request=%+v", last)
			}
		})
	}
}

func TestSynchronousTaskNotificationDoesNotCreateExtraUserRequest(t *testing.T) {
	registry := tools.NewRegistry()
	parentProvider := &stubProvider{name: "parent-trace", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "review-1", Name: "task", Arguments: `{"agent":"review","prompt":"Review fixture","async":false}`}, FinishReason: "tool_calls"}},
		{{Content: "Review complete.", FinishReason: "stop"}},
	}}
	child := &stubProvider{name: "child-trace", scripts: [][]llm.Delta{{{Content: "REVIEW_RESULT: no issues found.", FinishReason: "stop"}}}}
	parent, err := NewLoop(LoopConfig{Provider: parentProvider, Registry: registry, System: "Stable parent policy"})
	if err != nil {
		t.Fatal(err)
	}
	external := make(chan Event, 32)
	parent.SetExternalSink(external)
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, registry, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	registry.MustRegister(task.Spec())
	registry.MarkAlwaysOn("task")
	for _, event := range drainEvents(t, mustRun(t, parent, "Delegate a review and present its result.")) {
		if failure, ok := event.(ErrorEvent); ok {
			t.Fatal(failure.Err)
		}
	}
	if len(parentProvider.reqs) != 2 || len(child.reqs) != 1 {
		t.Fatalf("unexpected provider calls: parent=%d child=%d", len(parentProvider.reqs), len(child.reqs))
	}
	trace := traceNotificationRequest(parentProvider.reqs[1])
	if trace.HumanTurns != 1 || trace.WorkerReports != 1 || trace.ToolResults != 1 {
		t.Fatalf("synchronous notification duplicated tool output or human turn: %+v", trace)
	}
	uiNotices := 0
	for len(external) > 0 {
		if _, ok := (<-external).(WorkerNotificationEvent); ok {
			uiNotices++
		}
	}
	if uiNotices != 1 {
		t.Fatalf("UI notification count=%d, want one", uiNotices)
	}
	t.Logf("parent_requests=%d child_requests=%d UI_notifications=%d request_2=%+v", len(parentProvider.reqs), len(child.reqs), uiNotices, trace)
}

func TestWorkerNotificationRequestContentBounds(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, size := range []int{2048, 6000, 65536} {
			t.Run(fmt.Sprintf("async=%t/report_bytes=%d", async, size), func(t *testing.T) {
				var body strings.Builder
				for line := 0; body.Len() < size; line++ {
					fmt.Fprintf(&body, "Finding %05d: isolated fixture inspection result with documented evidence.\n", line)
				}
				// Worker completion intentionally trims outer whitespace; keep the
				// fixture canonical so full-report checks test content, not trimming.
				report := strings.TrimSpace(body.String()[:size])
				registry := tools.NewRegistry()
				parentProvider := &stubProvider{name: "parent-content-trace", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "review-1", Name: "task", Arguments: fmt.Sprintf(`{"agent":"review","prompt":"Review fixture","async":%t}`, async)}, FinishReason: "tool_calls"}},
					{{Content: "Review complete.", FinishReason: "stop"}},
				}}
				child := &stubProvider{name: "child-content-trace", scripts: [][]llm.Delta{{{Content: report, FinishReason: "stop"}}}}
				parent, err := NewLoop(LoopConfig{Provider: parentProvider, Registry: registry, System: "Stable parent policy", MaxSteps: 5})
				if err != nil {
					t.Fatal(err)
				}
				external := make(chan Event, 32)
				parent.SetExternalSink(external)
				specs := NewSubAgentRegistry()
				MustRegisterAll(specs, BuiltinSubAgents())
				task, err := NewAgentTool(specs, parent, registry, child, nil, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				registry.MustRegister(task.Spec())
				registry.MarkAlwaysOn("task")
				var uiText string
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				release := make(chan struct{})
				var releaseOnce sync.Once
				releaseChild := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(releaseChild)
				if async {
					child.onCalled = func(int) { <-release }
					parentProvider.onCalled = func(index int) {
						if index != 1 {
							return
						}
						releaseChild()
						for {
							select {
							case event := <-external:
								if notice, ok := event.(WorkerNotificationEvent); ok {
									uiText = notice.Text
									return
								}
							case <-ctx.Done():
								t.Error("worker notification was not delivered before timeout")
								return
							}
						}
					}
				}
				events, err := parent.Run(ctx, "Delegate a review and present the findings.")
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range drainEvents(t, events) {
					if failure, ok := event.(ErrorEvent); ok {
						t.Fatal(failure.Err)
					}
				}
				for len(external) > 0 {
					if notice, ok := (<-external).(WorkerNotificationEvent); ok {
						if uiText != "" {
							t.Fatal("duplicate UI completion notice")
						}
						uiText = notice.Text
					}
				}
				wantCalls := 2
				if async {
					wantCalls = 3 // the report was deliberately released after request 2 was assembled
				}
				if len(parentProvider.reqs) != wantCalls || len(child.reqs) != 1 || !strings.Contains(uiText, report) {
					t.Fatalf("delivery changed: parent calls=%d child calls=%d full UI report=%t", len(parentProvider.reqs), len(child.reqs), strings.Contains(uiText, report))
				}
				var modelText string
				for _, message := range parentProvider.reqs[len(parentProvider.reqs)-1] {
					if message.Role == llm.RoleUser && message.Name == llm.WorkerNotificationName ||
						message.Role == llm.RoleTool && message.Name == "task" && strings.Contains(message.Content, "<status>done</status>") {
						if modelText != "" {
							t.Fatal("same completion entered the request more than once")
						}
						modelText = message.Content
					}
				}
				if modelText == "" {
					t.Fatal("coordinator did not receive a worker result")
				}
				if size < core.ModelOutputInlineBytes {
					if !strings.Contains(modelText, report) || len(modelText) > core.ModelOutputInlineBytes {
						t.Fatal("small worker report was unexpectedly truncated or expanded")
					}
				} else {
					if len(modelText) > core.ModelOutputPreviewBytes+1024 || strings.Contains(modelText, report) {
						t.Fatalf("large UI report leaked into model context: %d bytes", len(modelText))
					}
					handle := core.StoredOutputHandle(modelText)
					if handle == "" {
						t.Fatal("large report lost its retrieval handle")
					}
					raw, _ := json.Marshal(map[string]string{"handle": handle, "query": "Finding 00400"})
					retained, err := registry.Execute(ctx, "read_output", raw)
					if err != nil || retained.Err != nil || !strings.Contains(retained.Text, "Finding 00400") {
						t.Fatalf("full report not retrievable outside context: %v / %+v", err, retained)
					}
				}
				t.Logf("report_bytes=%d UI_notification_bytes=%d model_result_bytes=%d approx_result_tokens=%d parent_requests=%d child_requests=%d", len(report), len(uiText), len(modelText), llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: modelText}}), len(parentProvider.reqs), len(child.reqs))
			})
		}
	}
}
