package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLoopRefreshContextWindowBeforeModelAndOncePerRun(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{
		Name: "read_fixture", Description: "Read the test fixture", ReadOnly: true, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "fixture read successfully"}, nil
		},
	})
	provider := &stubProvider{name: "refresh-fixture", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "read", Name: "read_fixture", Arguments: `{}`}}},
		{{Content: "The inspection is complete.", FinishReason: "stop"}},
	}}
	refreshes, window := 0, 128000
	var loop *Loop
	provider.onCalled = func(call int) {
		want := 1
		if call >= 2 {
			want = 2
		}
		if refreshes != want || loop.ContextReport().Window != 4096*want {
			t.Errorf("model call %d saw refreshes=%d window=%d", call, refreshes, loop.ContextReport().Window)
		}
	}
	var err error
	loop, err = NewLoop(LoopConfig{
		Provider: provider, Registry: registry, MaxSteps: 4,
		RefreshContextWindow: func(context.Context) error {
			refreshes++
			window = refreshes * 4096
			return nil
		},
		ContextWindowFor: func(string) ContextWindowResolution {
			return ContextWindowResolution{Tokens: window, Source: "provider-runtime"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"Inspect the fixture.", "Give another answer."} {
		for _, event := range drainEvents(t, mustRun(t, loop, prompt)) {
			if failure, ok := event.(ErrorEvent); ok {
				t.Fatal(failure.Err)
			}
		}
	}
	if refreshes != 2 || provider.calls != 3 {
		t.Fatalf("refreshes=%d model calls=%d", refreshes, provider.calls)
	}
}

func TestLoopRefreshContextWindowCancellationReleasesRun(t *testing.T) {
	provider := &capturingProvider{reply: "Completed."}
	started := make(chan struct{})
	loop, err := NewLoop(LoopConfig{
		Provider: provider, Registry: tools.NewRegistry(),
		RefreshContextWindow: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, runErr := loop.Run(ctx, "Inspect the fixture.")
		completed <- runErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata refresh did not start")
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled run=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata refresh did not release the canceled run")
	}
	if len(provider.requests()) != 0 || len(loop.Messages) != 0 {
		t.Fatal("canceled refresh reached the model or appended user history")
	}
	// A new selected endpoint replaces the previous callback between runs.
	refreshes := 0
	loop.SetContextWindowRefresh(func(context.Context) error { refreshes++; return nil })
	drainEvents(t, mustRun(t, loop, "Try the current endpoint."))
	if refreshes != 1 || len(provider.requests()) != 1 {
		t.Fatalf("next run refreshes=%d requests=%d", refreshes, len(provider.requests()))
	}
}
