package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func handoffFixture(report, evidence, status string) *Worker {
	return &Worker{
		ID: "worker-27", Agent: "general", Description: "independent work", Model: "arbitrary-provider/model",
		Status: status, CreatedAt: time.Unix(1730000000, 0), UpdatedAt: time.Unix(1730000060, 0),
		Runs: 3, Steps: 7, TokensIn: 12345, TokensOut: 6789, LastResult: report,
		ToolNames:    []string{"read_lines", "search_code", "read_lines", "patch_file", "ctx_execute"},
		lastEvidence: evidence, Loop: &Loop{},
	}
}

func assertHandoffResultsEqual(t *testing.T, got, want tools.Result, cause error) {
	t.Helper()
	if (got.Err == nil) != (want.Err == nil) {
		t.Fatalf("error presence changed: got=%v want=%v", got.Err, want.Err)
	}
	if got.Err != nil {
		if got.Err.Error() != want.Err.Error() || !errors.Is(got.Err, cause) {
			t.Fatalf("error text/cause changed: got=%v want=%v", got.Err, want.Err)
		}
	}
	got.Err, want.Err = nil, nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handoff bytes/metadata changed: text=%v retained=%v preview=%v",
			got.Text == want.Text, got.RetainedText == want.RetainedText, got.ModelPreview == want.ModelPreview)
	}
}

func TestPreparedWorkerHandoffMatchesIndependentLegacyBytes(t *testing.T) {
	longReport := "# Findings\n## First\n" + strings.Repeat("Evidence with Unicode ą字🙂.\n", 1200) + "## Last\nVerification remains required."
	for _, report := range []string{"", "Complete: files A and B.", longReport} {
		for _, evidence := range []string{"", "== read_lines A ==\nHistorical finding ą字🙂", strings.Repeat("== ctx_execute ==\nFAILED check, stderr and recovery retained.\n", 150)} {
			for _, status := range []string{"", "done", "failed", "stopped", "running"} {
				t.Run(fmt.Sprintf("report%d/evidence%d/%s", len(report), len(evidence), status), func(t *testing.T) {
					var cause error
					if status == "failed" {
						cause = errors.New("max steps interrupted after partial output")
					} else if status == "stopped" || status == "running" {
						cause = context.Canceled
					}
					w := handoffFixture(report, evidence, status)
					if cause != nil {
						w.LastError = cause.Error()
					}
					h := prepareWorkerHandoff(w, report)
					if !reflect.DeepEqual(h.snapshot, w.Snapshot()) || h.summary != legacyHandoffworkerSummary(w) || h.notification != legacyHandoffrenderWorkerNotification(w, report) {
						t.Fatal("snapshot, summary or UI wrapper changed")
					}
					assertHandoffResultsEqual(t, h.result(w, cause), legacyHandoffworkerResult(w, report, cause), cause)
				})
			}
		}
	}
}

func TestPreparedWorkerHandoffKeepsItsInvocationSnapshot(t *testing.T) {
	report := "# Original findings\n## First\n" + strings.Repeat("Original evidence.\n", 1500) + "## Last\nBoth requested outputs verified."
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			status := "done"
			var cause error
			if failed {
				status, cause = "failed", errors.New("original invocation interrupted")
			}
			w := handoffFixture(report, "== read_lines ==\nORIGINAL_HISTORICAL_EVIDENCE", status)
			if cause != nil {
				w.LastError = cause.Error()
			}
			want := legacyHandoffworkerResult(w, report, cause)
			wantUI, wantSummary := legacyHandoffrenderWorkerNotification(w, report), legacyHandoffworkerSummary(w)
			h := prepareWorkerHandoff(w, report)
			w.setState(func(w *Worker) {
				w.Status, w.LastError, w.lastEvidence = "running", "NEW_RUN_ERROR", "NEW_RUN_EVIDENCE"
				w.Runs++
				w.UpdatedAt = w.UpdatedAt.Add(time.Hour)
				w.ToolNames[0] = "NEW_RUN_TOOL"
				w.TokensIn, w.TokensOut, w.Steps = 9, 8, 1
			})
			assertHandoffResultsEqual(t, h.result(w, cause), want, cause)
			events := make(chan Event, 1)
			parent := &Loop{}
			parent.SetExternalSink(events)
			(&AgentTool{ParentLoop: parent}).emitPreparedWorkerNotification(h)
			n := (<-events).(WorkerNotificationEvent)
			if n.Text != wantUI || n.Summary != wantSummary || n.Status != status || n.TaskID != w.ID || n.Agent != w.Agent {
				t.Fatal("UI reused a later run's state")
			}
			if fresh := prepareWorkerHandoff(w, "new invocation"); fresh.notification == h.notification || fresh.evidence == h.evidence || fresh.snapshot.Runs == h.snapshot.Runs {
				t.Fatal("a later invocation reused an old handoff")
			}
		})
	}
}

func TestPreparedWorkerHandoffTaskDelivery(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("async%v/fail%v", async, fail), func(t *testing.T) {
				cause := errors.New("controlled stream failure")
				final := []llm.Delta{{Content: "Final report for both requested outputs."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 7, Output: 5, Total: 12}}}
				if fail {
					final = []llm.Delta{{Content: "Partial report; second output remains unfinished."}, {Err: cause}}
				}
				provider := &stubProvider{name: "arbitrary-backend", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "read_lines", Arguments: "{}"}}, {FinishReason: "tool_calls"}}, final,
				}}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{Name: "read_lines", Description: "controlled evidence", ReadOnly: true, Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "EVIDENCE_FIRST_OUTPUT; SECOND_OUTPUT_STATUS"}, nil
				}})
				reg.MarkAlwaysOn("read_lines")
				parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg})
				if err != nil {
					t.Fatal(err)
				}
				events := make(chan Event, 32)
				parent.SetExternalSink(events)
				specs := NewSubAgentRegistry()
				specs.MustRegister(SubAgent{Name: "general", Description: "isolated work"})
				task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
				if err != nil {
					t.Fatal(err)
				}
				args := json.RawMessage(fmt.Sprintf(`{"prompt":"complete both independent outputs","async":%v}`, async))
				result, err := task.execute(context.Background(), args)
				if err != nil || (!async && (result.Err != nil) != fail) || (async && result.Err != nil) {
					t.Fatalf("task result: %v %v", err, result.Err)
				}
				var notice WorkerNotificationEvent
				for {
					event := <-events // completion event, no progress polling
					if n, ok := event.(WorkerNotificationEvent); ok {
						notice = n
						break
					}
				}
				w, _ := task.Workers.Get("worker-1")
				w.stateMu.RLock()
				report := w.LastResult
				w.stateMu.RUnlock()
				wantNotice := legacyHandoffrenderWorkerNotification(w, report)
				if notice.Text != wantNotice || notice.Summary != legacyHandoffworkerSummary(w) || provider.calls != 2 {
					t.Fatal("completion wrapper, call count or summary changed")
				}
				var resultErr error
				if fail {
					resultErr = cause
				}
				want := legacyHandoffworkerResult(w, report, resultErr)
				if async {
					messages := parent.AllMessages()
					if len(messages) != 1 || messages[0].Content != reg.ModelResultContent("task", want) {
						t.Fatal("background result/evidence changed")
					}
				} else {
					assertHandoffResultsEqual(t, result, want, resultErr)
				}
			})
		}
	}
}

type handoffCancellationProvider struct{ started chan struct{} }

func (p *handoffCancellationProvider) Name() string         { return "arbitrary-cancel-backend" }
func (p *handoffCancellationProvider) SupportsVision() bool { return false }
func (p *handoffCancellationProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	stream := make(chan llm.Delta)
	go func() {
		defer close(stream)
		select {
		case stream <- llm.Delta{Content: "Partial invocation evidence."}:
		case <-ctx.Done():
			return
		}
		close(p.started)
		<-ctx.Done()
	}()
	return stream, nil
}

func TestPreparedWorkerHandoffCanceledTaskKeepsCauseAndUI(t *testing.T) {
	provider := &handoffCancellationProvider{started: make(chan struct{})}
	reg := tools.NewRegistry()
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	parent.SetExternalSink(events)
	specs := NewSubAgentRegistry()
	specs.MustRegister(SubAgent{Name: "general", Description: "isolated work"})
	task, err := NewAgentTool(specs, parent, reg, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan tools.Result, 1)
	go func() {
		result, _ := task.execute(ctx, json.RawMessage(`{"prompt":"complete both outputs"}`))
		completed <- result
	}()
	<-provider.started
	cancel()
	result := <-completed
	if !errors.Is(result.Err, context.Canceled) || strings.Contains(result.Err.Error(), "send_message") {
		t.Fatalf("canceled task lost cause or urges continuation: %v", result.Err)
	}
	w, _ := task.Workers.Get("worker-1")
	w.stateMu.RLock()
	report := w.LastResult
	w.stateMu.RUnlock()
	assertHandoffResultsEqual(t, result, legacyHandoffworkerResult(w, report, context.Canceled), context.Canceled)
	for {
		if n, ok := (<-events).(WorkerNotificationEvent); ok {
			if n.Text != legacyHandoffrenderWorkerNotification(w, report) || n.Status != w.status() {
				t.Fatal("canceled UI handoff changed")
			}
			break
		}
	}
}

var preparedWorkerHandoffBenchmarkNotice WorkerNotificationEvent
var preparedWorkerHandoffBenchmarkResult tools.Result

func BenchmarkPreparedWorkerHandoff(b *testing.B) {
	for _, size := range []int{128, 4096, 32768, 262144} {
		report := strings.Repeat("Evidence and final report.\n", size/26+1)[:size]
		w := handoffFixture(report, "== read_lines ==\nHistorical evidence for both outputs.", "done")
		b.Run(fmt.Sprintf("bytes%d", size), func(b *testing.B) {
			b.Run("legacy", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					preparedWorkerHandoffBenchmarkNotice = WorkerNotificationEvent{
						TaskID: w.ID, Agent: w.Agent, Status: w.status(), Summary: legacyHandoffworkerSummary(w),
						Text: legacyHandoffrenderWorkerNotification(w, report),
					}
					preparedWorkerHandoffBenchmarkResult = legacyHandoffworkerResult(w, report, nil)
				}
			})
			b.Run("prepared", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					h := prepareWorkerHandoff(w, report)
					preparedWorkerHandoffBenchmarkNotice = WorkerNotificationEvent{
						TaskID: h.snapshot.ID, Agent: h.snapshot.Agent, Status: h.snapshot.Status, Summary: h.summary, Text: h.notification,
					}
					preparedWorkerHandoffBenchmarkResult = h.result(w, nil)
				}
			})
		})
	}
}
