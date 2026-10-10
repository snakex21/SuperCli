package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type invokeParallelFixture struct {
	loop      *Loop
	calls     []llm.ToolCall
	results   []tools.Result
	wantModel []string
}

// Exercise the real dispatch, verification, model projection and ordered append
// path. The tools have no provider, file access or retained-output payloads.
func newInvokeParallelFixture(tb testing.TB, results []tools.Result, wantModel []string, writer SessionWriter, execute func(context.Context, int) (tools.Result, error)) invokeParallelFixture {
	tb.Helper()
	if len(results) != len(wantModel) || len(results) > 32 {
		tb.Fatal("invalid parallel fixture size")
	}
	reg := tools.NewRegistry()
	calls := make([]llm.ToolCall, len(results))
	for i := range results {
		i := i
		name := fmt.Sprintf("parallel_fixture_%02d", i)
		reg.MustRegister(tools.Tool{
			Name: name, Description: "independent synthetic result", Schema: `{}`, ReadOnly: true,
			Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				if execute != nil {
					return execute(ctx, i)
				}
				return results[i], nil
			},
			Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} },
		})
		calls[i] = llm.ToolCall{ID: name, Name: name, Arguments: `{}`}
	}
	messages := make([]llm.Message, 1, len(calls)+1)
	messages[0] = llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}
	return invokeParallelFixture{
		loop:  &Loop{registry: reg, writer: writer, Messages: messages},
		calls: calls, results: results, wantModel: wantModel,
	}
}

func (f invokeParallelFixture) checkHistory(tb testing.TB, ok bool, outcomes []callOutcome, saved []llm.Message) {
	tb.Helper()
	if !ok || len(outcomes) != len(f.calls) || len(f.loop.Messages) != len(f.calls)+1 || len(saved) != len(f.calls) {
		tb.Fatalf("batch shape: ok=%v outcomes=%d history=%d saved=%d", ok, len(outcomes), len(f.loop.Messages), len(saved))
	}
	assistant := f.loop.Messages[0]
	if assistant.Role != llm.RoleAssistant || len(assistant.ToolCalls) != len(f.calls) {
		tb.Fatal("lost assistant call batch")
	}
	for i, call := range f.calls {
		if assistant.ToolCalls[i] != call {
			tb.Fatalf("assistant call %d changed", i)
		}
		for _, m := range []llm.Message{f.loop.Messages[i+1], saved[i]} {
			if m.Role != llm.RoleTool || m.ToolCallID != call.ID || m.Name != call.Name || m.Content != f.wantModel[i] || len(m.Parts) != 0 || len(m.ToolCalls) != 0 {
				tb.Fatalf("history/persistence pairing at %d: %+v", i, m)
			}
		}
		if outcomes[i].failed != (f.results[i].Err != nil) || outcomes[i].inert != f.results[i].Inert || outcomes[i].observation.valid {
			tb.Fatalf("outcome %d attached to wrong call: %+v", i, outcomes[i])
		}
	}
}

func (f invokeParallelFixture) checkEvents(tb testing.TB, out <-chan Event, ids map[string]int) {
	tb.Helper()
	var starts, ends uint64
	for range 2 * len(f.calls) {
		switch ev := (<-out).(type) {
		case ToolCallEvent:
			i, known := ids[ev.ID]
			bit := uint64(1) << i
			if !known || starts&bit != 0 || ev.Name != f.calls[i].Name || ev.Args != f.calls[i].Arguments {
				tb.Fatalf("unexpected call event: %+v", ev)
			}
			starts |= bit
		case ToolResultEvent:
			i, known := ids[ev.ID]
			bit := uint64(1) << i
			if !known || ends&bit != 0 || starts&bit == 0 || ev.Output != f.results[i].Text || ev.Err != f.results[i].Err || ev.OutputHandle != "" || len(ev.Images) != 0 {
				tb.Fatalf("unexpected result event: %+v", ev)
			}
			ends |= bit
		default:
			tb.Fatalf("unexpected event %T", ev)
		}
	}
	want := uint64(1)<<len(f.calls) - 1
	if starts != want || ends != want || len(out) != 0 {
		tb.Fatalf("UI pairing starts=%x ends=%x buffered=%d", starts, ends, len(out))
	}
}

func BenchmarkInvokeCallsParallelCollection(b *testing.B) {
	for _, n := range []int{2, 8, 32} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			results, models := make([]tools.Result, n), make([]string, n)
			for i := range results {
				models[i] = fmt.Sprintf("value=%02d", i)
				results[i] = tools.Result{Text: fmt.Sprintf("fixture value [%02d]", i), ModelText: models[i]}
			}
			writer := &recordingWriter{messages: make([]llm.Message, 0, n)}
			f := newInvokeParallelFixture(b, results, models, writer, nil)
			out := make(chan Event, 2*n)
			ids := make(map[string]int, n)
			for i, c := range f.calls {
				ids[c.ID] = i
			}
			ctx := context.Background()
			// Warm the same fixture once, without accumulating history or output
			// cache entries. Every measured iteration also validates its results.
			ok, outcomes := f.loop.invokeCallsParallel(ctx, f.calls, out)
			f.checkHistory(b, ok, outcomes, writer.messages)
			f.checkEvents(b, out, ids)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				clear(f.loop.Messages[1:])
				f.loop.Messages = f.loop.Messages[:1]
				clear(writer.messages)
				writer.messages = writer.messages[:0]
				ok, outcomes := f.loop.invokeCallsParallel(ctx, f.calls, out)
				f.checkHistory(b, ok, outcomes, writer.messages)
				f.checkEvents(b, out, ids)
			}
			b.ReportMetric(float64(n), "calls/op")
			b.ReportMetric(float64(2*n), "events/op")
			b.ReportMetric(float64(n), "followups/op")
		})
	}
}

type invokeParallelGuardedWriter struct {
	recordingWriter
	beforeAppend func()
}

func (w *invokeParallelGuardedWriter) AppendMessage(ctx context.Context, m llm.Message) error {
	w.beforeAppend()
	return w.recordingWriter.AppendMessage(ctx, m)
}

type invokeParallelCompletion struct {
	ok          bool
	outcomes    []callOutcome
	allFinished bool
}

func invokeParallelAwaitSignal(t *testing.T, guard context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-guard.Done():
		t.Fatal("synthetic parallel handshake did not complete")
	}
}

// Existing cancellation tests cover interrupted vs unstarted outcomes. This
// fixture holds all started Fns after cancellation, so returning early cannot
// hide behind promptly cooperative tools. There is no sleep or progress poll.
func TestInvokeCallsParallelCollectionCompletionOrder(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			guard, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			entered, sawCancel, release := make([]chan struct{}, 3), make([]chan struct{}, 3), make([]chan struct{}, 3)
			once := make([]sync.Once, 3)
			finished := make([]atomic.Bool, 3)
			for i := range entered {
				entered[i], sawCancel[i], release[i] = make(chan struct{}), make(chan struct{}), make(chan struct{})
			}
			allFinished := func() bool {
				for i := range finished {
					if !finished[i].Load() {
						return false
					}
				}
				return true
			}
			var prematureAppend atomic.Bool
			writer := &invokeParallelGuardedWriter{beforeAppend: func() {
				if !allFinished() {
					prematureAppend.Store(true)
				}
			}}
			failure := errors.New("synthetic failure")
			models := []string{"value=0", "value=1", "error: synthetic failure\ntool output:\ndiagnostic 2"}
			results := []tools.Result{
				{Text: "fixture value [0]", ModelText: models[0]},
				{Text: "fixture value [1]", ModelText: models[1], Inert: true},
				{Text: "diagnostic 2", Err: failure},
			}
			if cancelled {
				failure = fmt.Errorf("TOOL_OUTCOME_UNKNOWN: interrupted after dispatch; side effects may have occurred. Check current state before retrying (%w)", context.Canceled)
				models[2] = "error: TOOL_OUTCOME_UNKNOWN: interrupted after dispatch; side effects may have occurred. Check current state before retrying (context canceled)\ntool output:\ndiagnostic 2"
				results[2].Err = failure
			}
			f := newInvokeParallelFixture(t, results, models, writer, func(ctx context.Context, i int) (tools.Result, error) {
				close(entered[i])
				if cancelled {
					<-ctx.Done()
					close(sawCancel[i])
				}
				<-release[i]
				finished[i].Store(true)
				if cancelled && i == 2 {
					return tools.Result{Text: results[i].Text, Err: ctx.Err()}, nil
				}
				return results[i], nil
			})
			out := make(chan Event, 6)
			done := make(chan invokeParallelCompletion, 1)
			drained := make(chan struct{})
			go func() {
				defer close(drained)
				ok, outcomes := f.loop.invokeCallsParallel(ctx, f.calls, out)
				done <- invokeParallelCompletion{ok: ok, outcomes: outcomes, allFinished: allFinished()}
			}()
			t.Cleanup(func() {
				cancel()
				for i := range release {
					once[i].Do(func() { close(release[i]) })
				}
				cleanupCtx, finish := context.WithTimeout(context.Background(), 5*time.Second)
				defer finish()
				select {
				case <-drained:
				case <-cleanupCtx.Done():
					t.Error("synthetic dispatch did not drain during cleanup")
				}
			})
			for _, signal := range entered {
				invokeParallelAwaitSignal(t, guard, signal)
			}
			if cancelled {
				cancel()
				for _, signal := range sawCancel {
					invokeParallelAwaitSignal(t, guard, signal)
				}
			}
			starts := make(map[string]bool, 3)
			for range f.calls {
				select {
				case e := <-out:
					call, ok := e.(ToolCallEvent)
					if !ok || starts[call.ID] || call.Name != call.ID || call.Args != `{}` {
						t.Fatalf("call pairing: %+v", e)
					}
					starts[call.ID] = true
				case <-guard.Done():
					t.Fatal("missing tool-call event")
				}
			}
			for i := len(f.calls) - 1; i >= 0; i-- {
				once[i].Do(func() { close(release[i]) })
				select {
				case e := <-out:
					result, ok := e.(ToolResultEvent)
					if !ok || !starts[result.ID] || result.ID != f.calls[i].ID || result.Output != results[i].Text || result.OutputHandle != "" || len(result.Images) != 0 {
						t.Fatalf("reverse completion at %d: %+v", i, e)
					}
					if i == 2 {
						if result.Err == nil || result.Err.Error() != failure.Error() || (cancelled && !errors.Is(result.Err, context.Canceled)) {
							t.Fatalf("result failure at %d: %v", i, result.Err)
						}
					} else if result.Err != nil {
						t.Fatalf("successful result at %d: %v", i, result.Err)
					}
				case <-guard.Done():
					t.Fatal("missing tool-result event")
				}
			}
			select {
			case batch := <-done:
				if !batch.allFinished || prematureAppend.Load() {
					t.Fatal("batch returned/persisted before every started Fn finished")
				}
				f.checkHistory(t, batch.ok, batch.outcomes, writer.messages)
			case <-guard.Done():
				t.Fatal("parallel batch did not complete")
			}
			if len(starts) != len(f.calls) || len(out) != 0 {
				t.Fatal("extra or missing UI events")
			}
			if cancelled && f.loop.identicalFails.attempts(f.calls[2].Name, `{}`) != 0 {
				t.Fatal("cancellation became a repeatable model failure")
			}
		})
	}
}
