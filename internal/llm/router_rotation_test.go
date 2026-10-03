package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRouterDoesNotFailOverAfterAnyGeneratedOutput(t *testing.T) {
	for name, first := range map[string]Delta{"reasoning": {Reasoning: "fixture"}, "native": {NativeReasoning: &ReasoningBlock{}}, "tool-start": {OutputStarted: true}, "reasoning-start": {ReasoningStarted: true}, "tool": {ToolCall: &ToolCall{Name: "fixture"}}} {
		t.Run(name, func(t *testing.T) {
			partial := &scriptedProvider{name: "fixture", deltas: []Delta{first, {Err: errors.New("fixture failure")}}}
			backup := &scriptedProvider{name: "fixture", deltas: []Delta{{Content: "should not happen"}}}
			r, _ := NewRouter(partial, backup)
			ch, err := r.Complete(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			ds := drainRouter(t, ch)
			if backup.callCount != 0 || len(ds) != 2 || ds[1].Err == nil {
				t.Fatalf("generated output retried: backup=%d deltas=%+v", backup.callCount, ds)
			}
		})
	}
}

type cancelAtStartProvider struct {
	cancel context.CancelFunc
	calls  int
}

func (p *cancelAtStartProvider) Name() string { return "fixture" }
func (p *cancelAtStartProvider) Complete(context.Context, []Message, []ToolDef) (<-chan Delta, error) {
	p.calls++
	p.cancel()
	return nil, errors.New("fixture cancellation")
}

func TestRouterCancellationDoesNotStartBackupOrAdvanceBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := &cancelAtStartProvider{cancel: cancel}
	backup := &scriptedProvider{name: "fixture"}
	r, _ := NewRouter(first, backup)
	if _, err := r.Complete(ctx, nil, nil); err != context.Canceled || backup.callCount != 0 {
		t.Fatalf("cancel failed over: %v/%d", err, backup.callCount)
	}
	before := r.next
	if _, err := r.Complete(ctx, nil, nil); err != context.Canceled || r.next != before {
		t.Fatal("pre-canceled request advanced rotation")
	}
}

func TestRouterSkipsOnlyFreshObservedExhaustionAndReturnsAfterReset(t *testing.T) {
	now := time.Now()
	snapshot := &CodexUsageSnapshot{CapturedAt: codexPtr(now), RateLimits: []CodexUsageLimit{{ID: "codex", Primary: &CodexUsageWindow{UsedPercent: codexPtr(float64(100)), ResetsAt: codexPtr(now.Add(time.Hour).Unix())}}}}
	bad := &usageProvider{scriptedProvider: scriptedProvider{name: "fixture", deltas: []Delta{{FinishReason: "stop"}}}, rl: CodexRateLimits{OK: true, Snapshot: snapshot}}
	good := &scriptedProvider{name: "fixture", deltas: []Delta{{FinishReason: "stop"}}}
	r, _ := NewRouter(bad, good)
	for i := 0; i < 2; i++ {
		ch, err := r.Complete(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		drainRouter(t, ch)
	}
	if bad.callCount != 0 || good.callCount != 2 {
		t.Fatal("fresh exhaustion was not skipped")
	}
	bad.rl.Snapshot.RateLimits[0].Primary.ResetsAt = codexPtr(time.Now().Add(-time.Second).Unix())
	ch, err := r.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainRouter(t, ch)
	if bad.callCount != 1 {
		t.Fatal("reset account did not reenter rotation")
	}
	bad.rl.Snapshot = nil
	r.next = 0
	ch, err = r.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainRouter(t, ch)
	if bad.callCount != 2 {
		t.Fatal("unknown usage excluded an account")
	}
}

func TestRouterAllObservedExhaustedStartsNoProvider(t *testing.T) {
	now := time.Now()
	snapshot := &CodexUsageSnapshot{CapturedAt: codexPtr(now), RateLimits: []CodexUsageLimit{{ID: "codex", Secondary: &CodexUsageWindow{UsedPercent: codexPtr(float64(100)), ResetsAt: codexPtr(now.Add(time.Hour).Unix())}}}}
	p := &usageProvider{scriptedProvider: scriptedProvider{name: "fixture"}, rl: CodexRateLimits{OK: true, Snapshot: snapshot}}
	r, _ := NewRouter(p)
	if _, err := r.Complete(context.Background(), nil, nil); err == nil || p.callCount != 0 {
		t.Fatal("exhausted pool started a provider")
	}
}

func TestRouterDoesNotDiscardOutputSharingErrorDelta(t *testing.T) {
	first := &scriptedProvider{name: "fixture", deltas: []Delta{{Content: "partial answer", Err: errors.New("same-frame failure")}}}
	backup := &scriptedProvider{name: "fixture", deltas: []Delta{{Content: "duplicate"}}}
	r, _ := NewRouter(first, backup)
	ch, err := r.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := drainRouter(t, ch)
	if backup.callCount != 0 || len(got) != 1 || got[0].Content != "partial answer" || got[0].Err == nil {
		t.Fatal("error frame discarded output or retried generation")
	}
}
