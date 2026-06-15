package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptedProvider is a stub Provider whose Complete behaviour is
// fully scripted: it can fail to start, or emit a fixed sequence of
// deltas (including a terminal error). It records how many times it
// was called so tests can assert routing/round-robin.
type scriptedProvider struct {
	name      string
	startErr  error   // if set, Complete returns this (never starts)
	deltas    []Delta // streamed when started
	callCount int
}

func (p *scriptedProvider) Name() string { return p.name }

func (p *scriptedProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	p.callCount++
	if p.startErr != nil {
		return nil, p.startErr
	}
	ch := make(chan Delta, len(p.deltas)+1)
	go func() {
		defer close(ch)
		for _, d := range p.deltas {
			ch <- d
		}
	}()
	return ch, nil
}

func drainRouter(t *testing.T, ch <-chan Delta) []Delta {
	t.Helper()
	var out []Delta
	for d := range ch {
		out = append(out, d)
	}
	return out
}

func TestRouter_RejectsEmptyPool(t *testing.T) {
	if _, err := NewRouter(); err == nil {
		t.Fatal("want error for empty pool")
	}
}

func TestRouter_NameReportsModelNotRouterString(t *testing.T) {
	// Multi-account pool must report the MODEL (so a model swap UI
	// shows "gpt-5.5 (2 accounts)", never "router(2 providers)").
	a := &scriptedProvider{name: "gpt-5.5"}
	b := &scriptedProvider{name: "gpt-5.5"}
	r, _ := NewRouter(a, b)
	got := r.Name()
	if !strings.HasPrefix(got, "gpt-5.5") {
		t.Errorf("Name = %q, want it to start with the model name", got)
	}
	if strings.Contains(got, "router(") {
		t.Errorf("Name = %q leaks internal router string", got)
	}
	if !strings.Contains(got, "2 accounts") {
		t.Errorf("Name = %q should still show the pool size", got)
	}
}

func TestRouter_SingleProviderPassesThrough(t *testing.T) {
	p := &scriptedProvider{name: "solo", deltas: []Delta{{Content: "hello"}, {FinishReason: "stop"}}}
	r, err := NewRouter(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != "solo" {
		t.Errorf("Name = %q, want solo", r.Name())
	}
	ch, _ := r.Complete(context.Background(), nil, nil)
	ds := drainRouter(t, ch)
	if len(ds) != 2 || ds[0].Content != "hello" {
		t.Fatalf("deltas = %+v", ds)
	}
}

func TestRouter_FailsOverWhenFirstWontStart(t *testing.T) {
	bad := &scriptedProvider{name: "bad", startErr: errors.New("429 rate limited")}
	good := &scriptedProvider{name: "good", deltas: []Delta{{Content: "ok"}, {FinishReason: "stop"}}}
	r, _ := NewRouter(bad, good)
	ch, err := r.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("router should fail over, got start err: %v", err)
	}
	ds := drainRouter(t, ch)
	if len(ds) == 0 || ds[0].Content != "ok" {
		t.Fatalf("expected good provider output, got %+v", ds)
	}
	if good.callCount != 1 {
		t.Errorf("good not called: %d", good.callCount)
	}
}

func TestRouter_FailsOverOnEarlyStreamError(t *testing.T) {
	// First provider starts but immediately errors with nothing
	// emitted → router must switch to the second.
	bad := &scriptedProvider{name: "bad", deltas: []Delta{{Err: errors.New("stream blew up")}}}
	good := &scriptedProvider{name: "good", deltas: []Delta{{Content: "recovered"}, {FinishReason: "stop"}}}
	r, _ := NewRouter(bad, good)
	ch, _ := r.Complete(context.Background(), nil, nil)
	ds := drainRouter(t, ch)
	// Caller must see ONLY the good output, no error delta.
	for _, d := range ds {
		if d.Err != nil {
			t.Errorf("caller saw an error after successful failover: %v", d.Err)
		}
	}
	if len(ds) == 0 || ds[0].Content != "recovered" {
		t.Fatalf("expected recovered output, got %+v", ds)
	}
}

func TestRouter_NoFailoverAfterContentEmitted(t *testing.T) {
	// First provider emits content THEN errors. Router must NOT
	// fail over (would duplicate output); the error passes through.
	partial := &scriptedProvider{name: "partial", deltas: []Delta{
		{Content: "half a sentence"},
		{Err: errors.New("dropped mid-stream")},
	}}
	backup := &scriptedProvider{name: "backup", deltas: []Delta{{Content: "SHOULD NOT APPEAR"}, {FinishReason: "stop"}}}
	r, _ := NewRouter(partial, backup)
	ch, _ := r.Complete(context.Background(), nil, nil)
	ds := drainRouter(t, ch)

	var sawErr, sawContent bool
	for _, d := range ds {
		if d.Content == "half a sentence" {
			sawContent = true
		}
		if d.Content == "SHOULD NOT APPEAR" {
			t.Error("router duplicated output by failing over after content")
		}
		if d.Err != nil {
			sawErr = true
		}
	}
	if !sawContent || !sawErr {
		t.Fatalf("expected content then passed-through error; content=%v err=%v", sawContent, sawErr)
	}
	if backup.callCount != 0 {
		t.Errorf("backup must not be called after content emitted: %d", backup.callCount)
	}
}

func TestRouter_AllProvidersFailToStart(t *testing.T) {
	a := &scriptedProvider{name: "a", startErr: errors.New("down")}
	b := &scriptedProvider{name: "b", startErr: errors.New("down")}
	r, _ := NewRouter(a, b)
	_, err := r.Complete(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("want error when all providers fail to start")
	}
}

func TestRouter_RoundRobinSpreadsLoad(t *testing.T) {
	a := &scriptedProvider{name: "a", deltas: []Delta{{FinishReason: "stop"}}}
	b := &scriptedProvider{name: "b", deltas: []Delta{{FinishReason: "stop"}}}
	r, _ := NewRouter(a, b)
	// 4 calls → each provider should get 2 (round-robin).
	for i := 0; i < 4; i++ {
		ch, _ := r.Complete(context.Background(), nil, nil)
		drainRouter(t, ch)
	}
	if a.callCount != 2 || b.callCount != 2 {
		t.Errorf("round-robin uneven: a=%d b=%d, want 2/2", a.callCount, b.callCount)
	}
}

func TestRouter_FailsOverThroughMultipleBadProviders(t *testing.T) {
	b1 := &scriptedProvider{name: "b1", startErr: errors.New("429")}
	b2 := &scriptedProvider{name: "b2", deltas: []Delta{{Err: errors.New("early fail")}}}
	good := &scriptedProvider{name: "good", deltas: []Delta{{Content: "finally"}, {FinishReason: "stop"}}}
	r, _ := NewRouter(b1, b2, good)
	ch, _ := r.Complete(context.Background(), nil, nil)
	ds := drainRouter(t, ch)
	if len(ds) == 0 || ds[0].Content != "finally" {
		t.Fatalf("expected to reach good provider, got %+v", ds)
	}
	for _, d := range ds {
		if d.Err != nil {
			t.Errorf("caller saw error despite a healthy provider downstream: %v", d.Err)
		}
	}
}
