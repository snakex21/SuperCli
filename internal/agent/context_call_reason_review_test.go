package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type contextReasonReviewProvider struct {
	stub    *stubProvider
	reasons []llm.CallReason
}

func (p *contextReasonReviewProvider) Name() string { return p.stub.Name() }
func (p *contextReasonReviewProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.reasons = append(p.reasons, llm.CallReasonFromContext(ctx))
	return p.stub.Complete(ctx, messages, tools)
}

func TestContextCallReasonFollowsActualContinuation(t *testing.T) {
	tool := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "inspect-fixture", Name: "inspect_fixture", Arguments: `{}`}, FinishReason: "tool_calls"}}
	final := []llm.Delta{{Content: "Fixture complete.", FinishReason: "stop"}}
	empty := []llm.Delta{{FinishReason: "stop"}}
	for _, tc := range []struct {
		name    string
		scripts [][]llm.Delta
		want    []llm.CallReason
	}{
		{"tool_results", [][]llm.Delta{tool, final}, []llm.CallReason{llm.ReasonUserInput, llm.ReasonToolResults}},
		{"empty_reply_nudge", [][]llm.Delta{tool, empty, final}, []llm.CallReason{llm.ReasonUserInput, llm.ReasonToolResults, llm.ReasonUnknown}},
		{"human_steering", [][]llm.Delta{final, final}, []llm.CallReason{llm.ReasonUserInput, llm.ReasonUserInput}},
		{"worker_steering", [][]llm.Delta{final, final}, []llm.CallReason{llm.ReasonUserInput, llm.ReasonResume}},
		{"worker_delivery", [][]llm.Delta{tool, final, final}, []llm.CallReason{llm.ReasonUserInput, llm.ReasonToolResults, llm.ReasonWorkerDelivery}},
		{"explicit_resume", [][]llm.Delta{final}, []llm.CallReason{llm.ReasonResume}},
		{"explicit_resume_with_tools", [][]llm.Delta{tool, final}, []llm.CallReason{llm.ReasonResume, llm.ReasonToolResults}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &contextReasonReviewProvider{stub: &stubProvider{name: "context-reason-fixture", scripts: tc.scripts}}
			registry := tools.NewRegistry()
			var loop *Loop
			var deliver func(context.Context, string)
			var workerCtx context.Context
			var finish func()
			registry.MustRegister(tools.Tool{
				Name: "inspect_fixture", Description: "Read a fixture", Schema: `{"type":"object"}`, ReadOnly: true,
				Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
					if llm.CallReasonFromContext(ctx) != llm.ReasonUnknown {
						t.Error("main-call admission reason leaked into later helper/tool contexts")
					}
					if tc.name == "worker_delivery" {
						deliver, workerCtx = loop.backgroundDelivery(ctx), ctx
						finish = backgroundRunFrom(ctx).start()
					}
					return tools.Result{Text: "Fixture inspected."}, nil
				},
			})
			registry.MarkAlwaysOn("inspect_fixture")
			var err error
			loop, err = NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Fixture policy", SkipImplementationHint: true, MaxSteps: 5})
			if err != nil {
				t.Fatal(err)
			}
			// Deliberately stale state must not leak across a new Run.
			loop.nextCallReason = llm.ReasonToolResults
			provider.stub.onCalled = func(index int) {
				if index == 0 && (tc.name == "human_steering" || tc.name == "worker_steering") {
					if _, err := loop.queueInterjection("Continue the fixture with this correction.", tc.name == "worker_steering"); err != nil {
						t.Error(err)
					}
				}
				if index == 1 && tc.name == "worker_delivery" {
					deliver(workerCtx, requestTraceWorkerNotice(1))
					finish()
				}
			}
			ctx := context.Background()
			if tc.name == "explicit_resume" || tc.name == "explicit_resume_with_tools" {
				ctx = llm.WithCallReason(ctx, llm.ReasonResume)
			}
			events, err := loop.Run(ctx, "Inspect the fixture.")
			if err != nil {
				t.Fatal(err)
			}
			done := false
			for event := range events {
				switch event := event.(type) {
				case ErrorEvent:
					t.Errorf("run failed: %v", event.Err)
				case DoneEvent:
					done = true
				}
			}
			if !done || !reflect.DeepEqual(provider.reasons, tc.want) {
				t.Fatalf("done=%t reasons=%v want=%v", done, provider.reasons, tc.want)
			}
		})
	}
}
