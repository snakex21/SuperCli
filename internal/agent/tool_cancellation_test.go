package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Tools deliberately ignore ctx: cancellation must stop dispatch in the harness,
// without relying on every built-in, extension and worker to implement a check.
func TestCancelledBatchDoesNotDispatch(t *testing.T) {
	for _, mode := range []string{"sequential", "reads", "workers", "mixed", "file waves", "invoke", "zen placeholder"} {
		t.Run(mode, func(t *testing.T) {
			reg := tools.NewRegistry()
			var executed, verified atomic.Int32
			register := func(name string, readOnly bool) {
				reg.MustRegister(tools.Tool{Name: name, Description: "fixture", Schema: "{}", ReadOnly: readOnly,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						executed.Add(1)
						return tools.Result{Text: "unexpected side effect"}, nil
					},
					Verify: func(tools.Result) tools.VerifyVerdict { verified.Add(1); return tools.VerifyVerdict{OK: true} },
				})
				reg.Activate(name)
			}
			register("write_fixture", false)
			register("read_fixture", true)
			register("task", false)
			register("write_file", false)
			register("ctx_execute", false)
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn("invoke_tool")
			names := []string{"write_fixture", "write_fixture"}
			args := []string{"{}", "{}"}
			switch mode {
			case "reads":
				names = []string{"read_fixture", "read_fixture"}
			case "workers":
				names = []string{"task", "task"}
			case "mixed":
				names = []string{"read_fixture", "write_fixture", "read_fixture"}
				args = append(args, "{}")
			case "file waves":
				names = []string{"write_file", "write_file"}
				args = []string{`{"path":"a.txt","content":"a"}`, `{"path":"b.txt","content":"b"}`}
			case "invoke":
				names = []string{"invoke_tool", "invoke_tool"}
				args = []string{`{"tool":"write_fixture","args":{}}`, `{"tool":"write_fixture","args":{}}`}
			case "zen placeholder":
				names = []string{"bash", "bash"}
				args = []string{`{"command":"echo a"}`, `{"command":"echo b"}`}
			}
			calls := make([]llm.ToolCall, len(names))
			for i, name := range names {
				calls[i] = llm.ToolCall{ID: fmt.Sprintf("c%d", i), Name: name, Arguments: args[i]}
			}
			w := &recordingWriter{}
			l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, Writer: w, TaskParallel: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			events := make(chan Event, 20)
			ok, outcomes := l.invokeToolCalls(ctx, calls, events)
			if !ok || len(outcomes) != len(calls) {
				t.Fatalf("batch incomplete: %v %+v", ok, outcomes)
			}
			if executed.Load() != 0 || verified.Load() != 0 {
				t.Fatalf("cancelled dispatch executed=%d verified=%d", executed.Load(), verified.Load())
			}
			if len(w.messages) != len(calls) {
				t.Fatalf("saved results=%d, want %d", len(w.messages), len(calls))
			}
			for i, m := range w.messages {
				if m.ToolCallID != calls[i].ID || !strings.Contains(m.Content, "TOOL_NOT_STARTED") {
					t.Fatalf("result %d: %+v", i, m)
				}
			}
			close(events)
			starts, ends := 0, 0
			for ev := range events {
				switch ev := ev.(type) {
				case ToolCallEvent:
					starts++
				case ToolResultEvent:
					ends++
					if !errors.Is(ev.Err, context.Canceled) {
						t.Fatalf("event error=%v", ev.Err)
					}
				}
			}
			if starts != len(calls) || ends != len(calls) {
				t.Fatalf("UI pairing=%d/%d", starts, ends)
			}
		})
	}
}

func TestCancellationKeepsCompletedWorkForContinuation(t *testing.T) {
	for _, thin := range []bool{false, true} {
		t.Run(fmt.Sprintf("thin=%v", thin), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reg := tools.NewRegistry()
			first, second := 0, 0
			reg.MustRegister(tools.Tool{Name: "first_write", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				first++
				cancel()
				return tools.Result{Text: "first change saved"}, nil
			}})
			reg.MustRegister(tools.Tool{Name: "second_write", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				second++
				return tools.Result{Text: "second change saved"}, nil
			}})
			reg.Activate("first_write", "second_write")
			p := &stubProvider{name: "test", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "first", Name: "first_write", Arguments: "{}"}}, {ToolCall: &llm.ToolCall{ID: "second", Name: "second_write", Arguments: "{}"}}},
				{{Content: "The first change is saved; the second was not started.", FinishReason: "stop"}},
			}}
			store, err := session.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			sess, err := store.Create(t.TempDir(), "test", "")
			if err != nil {
				t.Fatal(err)
			}
			w := session.NewWriter(store, sess.ID)
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: w, ThinTools: thin, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := l.Run(ctx, "make both changes")
			if err != nil {
				t.Fatal(err)
			}
			var stopped bool
			for ev := range ch {
				if e, ok := ev.(ErrorEvent); ok && errors.Is(e.Err, context.Canceled) {
					stopped = true
				}
			}
			if !stopped || first != 1 || second != 0 || p.calls != 1 {
				t.Fatalf("stop=%v first=%d second=%d requests=%d", stopped, first, second, p.calls)
			}
			// Resume from the durable transcript, as a reopened CLI/GUI session would.
			loaded, err := store.ReadModelContext(context.Background(), sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := NewLoop(LoopConfig{Provider: p, Registry: reg, InitialMessages: loaded, ThinTools: thin, MaxSteps: 2})
			if err != nil {
				t.Fatal(err)
			}
			for range mustRun(t, resumed, "what finished?") {
			}
			if p.calls != 2 || first != 1 || second != 0 {
				t.Fatalf("replayed work or extra request: %d/%d/%d", first, second, p.calls)
			}
			results := map[string]string{}
			for _, m := range p.reqs[1] {
				if m.Role == llm.RoleTool {
					results[m.ToolCallID] = m.Content
				}
			}
			if results["first"] != "first change saved" || !strings.Contains(results["second"], "TOOL_NOT_STARTED") {
				t.Fatalf("lost outcomes: %+v", results)
			}
		})
	}
}

func TestInterruptedToolOutcomeIsNotConfusedWithUnstarted(t *testing.T) {
	for _, goError := range []bool{false, true} {
		t.Run(fmt.Sprintf("goError=%v", goError), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{Name: "write_fixture", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				cancel()
				result := tools.Result{Text: "partial progress recorded"}
				if goError {
					return result, fmt.Errorf("stopped: %w", context.Canceled)
				}
				result.Err = fmt.Errorf("stopped: %w", context.Canceled)
				return result, nil
			}})
			l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
			if err != nil {
				t.Fatal(err)
			}
			ev := l.invoke(ctx, llm.ToolCall{ID: "c1", Name: "write_fixture", Arguments: "{}"}, make(chan Event, 2))
			if !ev.failed || len(ev.followUps) != 1 || !strings.Contains(ev.followUps[0].Content, "TOOL_OUTCOME_UNKNOWN") || strings.Contains(ev.followUps[0].Content, "TOOL_NOT_STARTED") {
				t.Fatalf("outcome=%+v", ev)
			}
			if l.identicalFails.attempts("write_fixture", "{}") != 0 {
				t.Fatal("user cancellation counted as a repeated tool failure")
			}
		})
	}
}

func TestExpiredTurnSkipsTool(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "write_fixture", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		t.Fatal("expired turn dispatched")
		return tools.Result{}, nil
	}})
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 2)
	result := l.invoke(ctx, llm.ToolCall{ID: "c1", Name: "write_fixture", Arguments: "{}"}, events)
	if !result.failed || !strings.Contains(result.followUps[0].Content, "TOOL_NOT_STARTED") {
		t.Fatalf("result=%+v", result)
	}
	<-events
	event := (<-events).(ToolResultEvent)
	if !errors.Is(event.Err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", event.Err)
	}
}

// One parallel read is interrupted while another finishes successfully; the
// following mutation is a barrier and must never start on the cancelled turn.
func TestCancellationPreservesEveryParallelOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	secondStarted := make(chan struct{})
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "interrupted_read", Description: "fixture", Schema: "{}", ReadOnly: true, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		select {
		case <-secondStarted:
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
		cancel()
		return tools.Result{Text: "partial read", Err: ctx.Err()}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "completed_read", Description: "fixture", Schema: "{}", ReadOnly: true, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		close(secondStarted)
		<-ctx.Done()
		return tools.Result{Text: "complete evidence"}, nil
	}})
	reg.MustRegister(tools.Tool{Name: "write_barrier", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		t.Error("write started after cancellation")
		return tools.Result{Text: "unexpected write"}, nil
	}})
	calls := []llm.ToolCall{
		{ID: "a", Name: "interrupted_read", Arguments: "{}"},
		{ID: "b", Name: "completed_read", Arguments: "{}"},
		{ID: "c", Name: "write_barrier", Arguments: "{}"},
	}
	w := &recordingWriter{}
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	ok, outcomes := l.invokeToolCalls(ctx, calls, make(chan Event, 10))
	if !ok || len(outcomes) != 3 || !outcomes[0].failed || outcomes[1].failed || !outcomes[2].failed {
		t.Fatalf("outcomes=%+v", outcomes)
	}
	if len(w.messages) != 3 {
		t.Fatalf("lost completed results: %+v", w.messages)
	}
	for i, m := range w.messages {
		if m.ToolCallID != calls[i].ID {
			t.Fatalf("order=%+v", w.messages)
		}
	}
	if !strings.Contains(w.messages[0].Content, "TOOL_OUTCOME_UNKNOWN") || w.messages[1].Content != "complete evidence" || !strings.Contains(w.messages[2].Content, "TOOL_NOT_STARTED") {
		t.Fatalf("incorrect outcomes: %+v", w.messages)
	}
}
