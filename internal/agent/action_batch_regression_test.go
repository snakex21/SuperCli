package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func actionBatchEnvelope(t *testing.T, call llm.ToolCall, wrapped bool) llm.ToolCall {
	t.Helper()
	if !wrapped {
		return call
	}
	raw, err := json.Marshal(map[string]any{"tool": call.Name, "args": json.RawMessage(call.Arguments)})
	if err != nil {
		t.Fatal(err)
	}
	call.Name, call.Arguments = invokeToolName, string(raw)
	return call
}

// ReadOnly permits parallel inspections, but it says nothing about the state
// inspected by an extension. A file update between two inspection groups must
// remain a barrier even when the extension has no declared file footprint.
func TestActionBatchUnknownInspectionsWaitForFileMutation(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%v", wrapped), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			root := t.TempDir()
			path := filepath.Join(root, "state.txt")
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			var started, finished [2]atomic.Int32
			var inspectionsVerified, mutations, mutationsVerified atomic.Int32
			ready := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
			for _, name := range []string{"service_inspect", "runtime_probe"} {
				reg.MustRegister(tools.Tool{
					Name: name, Description: "Inspect independent service or runtime evidence", ReadOnly: true,
					Schema: `{"type":"object","properties":{"phase":{"type":"integer","enum":[0,1]}},"required":["phase"],"additionalProperties":false}`,
					Fn: func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
						var args struct{ Phase int }
						if err := json.Unmarshal(raw, &args); err != nil {
							return tools.Result{Err: err}, nil
						}
						if started[args.Phase].Add(1) == 2 {
							close(ready[args.Phase])
						}
						select {
						case <-ready[args.Phase]:
						case <-ctx.Done():
							return tools.Result{Err: ctx.Err()}, nil
						}
						body, err := os.ReadFile(path)
						want := "before"
						if args.Phase == 1 {
							want = "after"
						}
						if err != nil || string(body) != want {
							return tools.Result{Err: fmt.Errorf("phase %d observed %q instead of %q: %v", args.Phase, body, want, err)}, nil
						}
						finished[args.Phase].Add(1)
						return tools.Result{Text: name + "=" + string(body)}, nil
					},
					Verify: func(tools.Result) tools.VerifyVerdict {
						inspectionsVerified.Add(1)
						return tools.VerifyVerdict{OK: true}
					},
				})
				reg.Activate(name)
			}
			write := tools.NewWriteFile(root).Spec()
			writeFn := write.Fn
			write.Fn = func(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
				mutations.Add(1)
				if finished[0].Load() != 2 || started[1].Load() != 0 {
					return tools.Result{Err: errors.New("file mutation crossed the inspection barrier")}, nil
				}
				return writeFn(ctx, raw)
			}
			write.Verify = func(result tools.Result) tools.VerifyVerdict {
				mutationsVerified.Add(1)
				return (tools.DefaultVerifier{}).Verify(tools.Check{
					Tool: "write_file", Family: "file_write", BaseDir: root, Result: result,
					Args: json.RawMessage(`{"path":"state.txt","content":"after"}`),
				})
			}
			reg.MustRegister(write)
			reg.Activate(write.Name)
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			calls := []llm.ToolCall{
				{ID: "service-before", Name: "service_inspect", Arguments: `{"phase":0}`},
				{ID: "runtime-before", Name: "runtime_probe", Arguments: `{"phase":0}`},
				{ID: "update", Name: "write_file", Arguments: `{"path":"state.txt","content":"after"}`},
				{ID: "service-after", Name: "service_inspect", Arguments: `{"phase":1}`},
				{ID: "runtime-after", Name: "runtime_probe", Arguments: `{"phase":1}`},
			}
			var deltas []llm.Delta
			for _, call := range calls {
				call = actionBatchEnvelope(t, call, wrapped)
				deltas = append(deltas, llm.Delta{ToolCall: &call})
			}
			provider := &stubProvider{name: "inspection-batch-fixture", scripts: [][]llm.Delta{
				deltas, {{Content: "The update and both follow-up inspections are complete.", FinishReason: "stop"}},
			}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, BaseDir: root, ThinTools: wrapped, StableToolset: true, MaxSteps: 3})
			if err != nil {
				t.Fatal(err)
			}
			if _, known := loop.toolConflictWavesContext(ctx, calls); known {
				t.Fatal("unknown inspection footprint was assumed independent of a file mutation")
			}
			events, err := loop.Run(ctx, "Inspect the service and runtime, update the state, and verify both again.")
			if err != nil {
				t.Fatal(err)
			}
			var done, starts, ends int
			for event := range events {
				switch event := event.(type) {
				case ToolCallEvent:
					starts++
				case ToolResultEvent:
					ends++
					if event.Err != nil {
						t.Errorf("tool %s failed: %v", event.ID, event.Err)
					}
				case ErrorEvent:
					t.Errorf("run failed: %v", event.Err)
				case DoneEvent:
					done++
				}
			}
			if provider.calls != 2 || done != 1 || starts != len(calls) || ends != len(calls) || finished[0].Load() != 2 || finished[1].Load() != 2 || mutations.Load() != 1 || mutationsVerified.Load() != 1 || inspectionsVerified.Load() != 4 {
				t.Fatalf("lost execution/verification/completion: requests=%d done=%d pairing=%d/%d reads=%d/%d mutation=%d/%d read verification=%d", provider.calls, done, starts, ends, finished[0].Load(), finished[1].Load(), mutations.Load(), mutationsVerified.Load(), inspectionsVerified.Load())
			}
			var results []llm.Message
			for _, message := range provider.reqs[1] {
				if message.Role == llm.RoleTool {
					results = append(results, message)
				}
			}
			if len(results) != len(calls) {
				t.Fatalf("result count=%d", len(results))
			}
			for i, result := range results {
				if result.ToolCallID != calls[i].ID || result.Name != calls[i].Name {
					t.Fatalf("call/result order changed: %+v", result)
				}
				if i != 2 {
					want := calls[i].Name + "=before"
					if i > 2 {
						want = calls[i].Name + "=after"
					}
					if result.Content != want {
						t.Fatalf("inspection evidence changed: %q; want %q", result.Content, want)
					}
				}
			}
		})
	}
}

// An arbitrary effect may change state before reporting a failure. Within one
// batch, that failure still invalidates inspection reuse and stays attached to
// its own call; subsequent successful reads do not rewrite its error result.
func TestActionBatchFailedEffectInvalidatesInspection(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%v", wrapped), func(t *testing.T) {
			reg := tools.NewRegistry()
			var revision, reads, verified, effects atomic.Int32
			for _, name := range []string{"account_snapshot", "service_snapshot"} {
				reg.MustRegister(tools.Tool{
					Name: name, Description: "Registered reusable inspection", ReadOnly: true,
					Schema: reuseTestSchema, ReuseTTL: time.Minute, RefreshArg: "refresh",
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						reads.Add(1)
						return tools.Result{Text: fmt.Sprintf("%s revision=%d", name, revision.Load())}, nil
					},
					Verify: func(tools.Result) tools.VerifyVerdict {
						verified.Add(1)
						return tools.VerifyVerdict{OK: true}
					},
				})
				reg.Activate(name)
			}
			failure := errors.New("profile update stopped after a partial change")
			reg.MustRegister(tools.Tool{
				Name: "apply_profile", Description: "Update externally observed state", Schema: `{}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					effects.Add(1)
					revision.Store(1)
					return tools.Result{Text: "partial revision=1", Err: failure}, nil
				},
			})
			reg.Activate("apply_profile")
			reg.MustRegister(NewInvokeTool(reg).Spec())
			reg.MarkAlwaysOn(invokeToolName)
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: reg, BaseDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			calls := []llm.ToolCall{
				{ID: "account-before", Name: "account_snapshot", Arguments: `{"query":"state"}`},
				{ID: "service-before", Name: "service_snapshot", Arguments: `{"query":"state"}`},
				{ID: "failed-effect", Name: "apply_profile", Arguments: `{}`},
				{ID: "account-after", Name: "account_snapshot", Arguments: `{"query":"state"}`},
				{ID: "service-after", Name: "service_snapshot", Arguments: `{"query":"state"}`},
			}
			for i := range calls {
				calls[i] = actionBatchEnvelope(t, calls[i], wrapped)
			}
			calls = loop.resolveInvokeToolCalls(calls)
			ok, outcomes := loop.invokeToolCalls(context.Background(), calls, make(chan Event, 16))
			if !ok || len(outcomes) != len(calls) || countFailures(outcomes) != 1 || !outcomes[2].failed || reads.Load() != 4 || verified.Load() != 4 || effects.Load() != 1 {
				t.Fatalf("failed effect or fresh evidence lost: ok=%v outcomes=%+v reads=%d verified=%d effects=%d", ok, outcomes, reads.Load(), verified.Load(), effects.Load())
			}
			if len(loop.Messages) != len(calls) {
				t.Fatalf("result count=%d", len(loop.Messages))
			}
			for i, result := range loop.Messages {
				if result.Name != calls[i].Name || result.ToolCallID != calls[i].ID {
					t.Fatalf("call/result pairing changed: %+v", result)
				}
				if i == 2 {
					if !strings.Contains(result.Content, failure.Error()) || !strings.Contains(result.Content, "partial revision=1") {
						t.Fatal("failed effect lost its error or partial evidence")
					}
					continue
				}
				wantRevision := 0
				if i > 2 {
					wantRevision = 1
				}
				want := fmt.Sprintf("%s revision=%d", calls[i].Name, wantRevision)
				if result.Content != want || strings.Contains(result.Content, "[reuse]") {
					t.Fatalf("stale or altered inspection: %q; want %q", result.Content, want)
				}
			}
		})
	}
}
