package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestWorkerUIJournalBoundsAndPairing(t *testing.T) {
	a := workerUIActivity{Version: 1, ID: "worker-1", Run: 1}
	for i := 0; i < 100; i++ {
		id := fmt.Sprint(i)
		a.call(ToolCallEvent{ID: id, Name: "read_lines", Args: strings.Repeat("界", 1000)})
		a.result(ToolResultEvent{ID: id, Output: strings.Repeat("visible output\n", 1000)})
	}
	a.call(ToolCallEvent{ID: "failed", Name: "read_lines", Args: `{"file":"missing.go"}`})
	a.result(ToolResultEvent{ID: "failed", Err: errors.New("file missing")})
	a.call(ToolCallEvent{ID: "incomplete", Name: "read_lines"})
	a.result(ToolResultEvent{ID: "unrelated", Output: "not paired"})
	raw := a.encode()
	if len(raw) > workerUIByteLimit || !json.Valid(raw) {
		t.Fatal("unbounded/invalid activity")
	}
	var decoded workerUIActivity
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Calls) > workerUICallLimit || decoded.Omitted != 102-len(decoded.Calls) {
		t.Fatal("activity cap concealed omissions")
	}
	last := decoded.Calls[len(decoded.Calls)-2:]
	if !last[0].Completed || last[0].Error != "file missing" || last[1].Completed || strings.Contains(string(raw), "not paired") {
		t.Fatal("outcomes attached to wrong calls")
	}
	for _, c := range decoded.Calls {
		if len(c.Args) > 1024 {
			t.Fatal("arguments exceeded budget")
		}
	}
}

func TestWorkerUIPersistedButAbsentFromModelAndContinuation(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%t", async), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			data := t.TempDir()
			store, err := session.OpenStore(data)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			sess, err := store.Create("fixture", "fixture", "worker UI")
			if err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			fullOutput := "VISIBLE_RESULT zażółć <script>unsafe()</script>\n" + strings.Repeat("observation\n", 1000) + "MIDDLE_DETAIL_CANARY\n" + strings.Repeat("tail\n", 1000)
			reg.MustRegister(tools.Tool{Name: "read_lines", Description: "Read fixture", ReadOnly: true, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Text: fullOutput}, nil
			}})
			main := &stubProvider{name: "main", scripts: [][]llm.Delta{
				{{ToolCall: &llm.ToolCall{ID: "delegate", Name: "task", Arguments: fmt.Sprintf(`{"agent":"explore","prompt":"Read fixture.go","async":%t}`, async)}, FinishReason: "tool_calls"}},
				{{Content: "Complete.", FinishReason: "stop"}},
			}}
			child := &stubProvider{name: "child", scripts: [][]llm.Delta{
				{{Reasoning: "PRIVATE_REASONING_CANARY"}, {ToolCall: &llm.ToolCall{ID: "read", Name: "read_lines", Arguments: `{"file":"fixture.go"}`}, FinishReason: "tool_calls"}},
				{{Content: "Worker report.", FinishReason: "stop"}},
				{{Content: "No further tools.", FinishReason: "stop"}},
			}}
			parent, err := NewLoop(LoopConfig{Provider: main, Registry: reg, Writer: session.NewWriter(store, sess.ID)})
			if err != nil {
				t.Fatal(err)
			}
			external := make(chan Event, 64)
			parent.SetExternalSink(external)
			specs := NewSubAgentRegistry()
			MustRegisterAll(specs, BuiltinSubAgents())
			task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			reg.MustRegister(task.Spec())
			if async {
				release := make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				child.onCalled = func(i int) {
					if i == 0 {
						select {
						case <-release:
						case <-ctx.Done():
						}
					}
				}
				main.onCalled = func(i int) {
					if i != 1 {
						return
					}
					unblock()
					for {
						select {
						case ev := <-external:
							if _, ok := ev.(WorkerNotificationEvent); ok {
								return
							}
						case <-ctx.Done():
							t.Error("worker completion timed out")
							return
						}
					}
				}
			}
			for _, ev := range drainEvents(t, mustRun(t, parent, "Delegate a fixture inspection.")) {
				if e, ok := ev.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			for _, req := range main.reqs {
				for _, m := range req {
					if m.Origin != nil && len(m.Origin.WorkerActivity) > 0 {
						t.Fatal("UI trail entered model request")
					}
				}
			}
			// Pruning/compaction changes only the provider projection. Exercise
			// repeated real compaction boundaries before reopening the SQL store.
			for i := 0; i < 3; i++ {
				if parent.CompactWithSummary("The fixture inspection is complete; preserve the user's instructions.") == 0 {
					t.Fatal("fixture did not compact")
				}
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = session.OpenStore(data)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := store.ReadMessages(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			var canonical []llm.Message
			for _, row := range encoded {
				message, err := row.ToMessage()
				if err != nil {
					t.Fatal(err)
				}
				canonical = append(canonical, message)
			}
			var saved json.RawMessage
			for _, m := range canonical {
				if m.Origin != nil && len(m.Origin.WorkerActivity) > 0 {
					saved = m.Origin.WorkerActivity
				}
			}
			if !strings.Contains(string(saved), "VISIBLE_RESULT") || strings.Contains(string(saved), "PRIVATE_REASONING_CANARY") {
				t.Fatal("saved UI activity missing or contains reasoning")
			}
			modelContext, err := store.ReadModelContext(ctx, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range modelContext {
				if m.Origin != nil && len(m.Origin.WorkerActivity) > 0 {
					t.Fatal("UI trail entered storage model projection")
				}
			}
			var log workerUIActivity
			if err = json.Unmarshal(saved, &log); err != nil {
				t.Fatal(err)
			}
			if log.Run != 1 || len(log.Calls) != 1 || !log.Calls[0].Completed || log.Calls[0].Args != `{"file":"fixture.go"}` {
				t.Fatalf("wrong persisted run: %+v", log)
			}
			if log.Calls[0].DetailHandle == "" || !log.Calls[0].Partial {
				t.Fatal("long output has no complete human detail")
			}
			detailJSON, err := store.ReadSessionToolOutput(ctx, sess.ID, log.Calls[0].DetailHandle, WorkerToolDetailLimit)
			var detail WorkerToolDetail
			if err != nil || json.Unmarshal([]byte(detailJSON), &detail) != nil || detail.Output != fullOutput || detail.Args != log.Calls[0].Args {
				t.Fatal("complete tool detail did not survive restart")
			}
			for _, req := range main.reqs {
				for _, m := range req {
					if strings.Contains(m.Content, log.Calls[0].DetailHandle) {
						t.Fatal("UI handle entered model request")
					}
				}
			}
			resumed, err := NewLoop(LoopConfig{Provider: main, Registry: tools.NewRegistry(), InitialMessages: canonical})
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range resumed.Messages {
				if m.Origin != nil && len(m.Origin.WorkerActivity) > 0 {
					t.Fatal("restart retained UI trail in model RAM")
				}
			}
			w, _ := task.Workers.Get("worker-1")
			before := string(saved)
			if _, err = runWorkerLoop(ctx, w, "Confirm completion without tools."); err != nil {
				t.Fatal(err)
			}
			next := prepareWorkerHandoff(w, "No further tools.")
			if err = json.Unmarshal(next.uiActivity, &log); err != nil {
				t.Fatal(err)
			}
			if log.Run != 2 || len(log.Calls) != 0 || string(saved) != before {
				t.Fatal("continuation copied or mutated earlier activity")
			}
		})
	}
}
