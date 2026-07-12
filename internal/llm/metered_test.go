package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// sinkCapture collects CallStats thread-safely.
type sinkCapture struct {
	mu    sync.Mutex
	stats []CallStat
}

func (s *sinkCapture) sink() CallSink {
	return func(c CallStat) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.stats = append(s.stats, c)
	}
}

func (s *sinkCapture) all() []CallStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CallStat, len(s.stats))
	copy(out, s.stats)
	return out
}

// meterStub is a minimal scripted provider for the wrapper tests.
type meterStub struct {
	name   string
	deltas []Delta
	err    error
	delay  time.Duration // before the first delta
}

func (p *meterStub) Name() string { return p.name }

func (p *meterStub) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan Delta, len(p.deltas))
	go func() {
		defer close(ch)
		if p.delay > 0 {
			time.Sleep(p.delay)
		}
		for _, d := range p.deltas {
			ch <- d
		}
	}()
	return ch, nil
}

func drainMetered(t *testing.T, ch <-chan Delta) {
	t.Helper()
	for range ch {
	}
}

func TestMetered_DefaultPurposeAndUsage(t *testing.T) {
	cap := &sinkCapture{}
	p := Metered(&meterStub{
		name:  "m1",
		delay: 2 * time.Millisecond, // so TTFT is measurably > 0 on Windows clocks
		deltas: []Delta{
			{Content: "hi"},
			{FinishReason: "stop", Usage: &Usage{Input: 11, Output: 4}},
		},
	}, "openai", "main", cap.sink())
	ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainMetered(t, ch)
	stats := cap.all()
	if len(stats) != 1 {
		t.Fatalf("stats = %d, want 1", len(stats))
	}
	s := stats[0]
	if s.Purpose != "main" || s.Provider != "openai" || s.Model != "m1" {
		t.Errorf("identity = %q/%q/%q", s.Purpose, s.Provider, s.Model)
	}
	if s.TokensIn != 11 || s.TokensOut != 4 {
		t.Errorf("tokens = %d/%d, want 11/4", s.TokensIn, s.TokensOut)
	}
	if s.TTFT <= 0 || s.Duration < s.TTFT {
		t.Errorf("timing: ttft=%v duration=%v", s.TTFT, s.Duration)
	}
	if s.Background || s.Canceled || s.Failed {
		t.Errorf("flags = %+v", s)
	}
}

func TestMetered_ContextPurposeOverridesAndBackground(t *testing.T) {
	cap := &sinkCapture{}
	p := Metered(&meterStub{name: "m", deltas: []Delta{{Content: "ok"}}}, "t", "draft", cap.sink())
	ctx := WithBackground(WithPurpose(context.Background(), "memory"))
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainMetered(t, ch)
	s := cap.all()[0]
	if s.Purpose != "memory" {
		t.Errorf("Purpose = %q, want memory (ctx override)", s.Purpose)
	}
	if !s.Background {
		t.Error("Background = false, want true")
	}
}

func TestMetered_ErrorAndCancel(t *testing.T) {
	cap := &sinkCapture{}
	p := Metered(&meterStub{name: "m", err: errors.New("boom")}, "t", "main", cap.sink())
	if _, err := p.Complete(context.Background(), nil, nil); err == nil {
		t.Fatal("want error")
	}
	if s := cap.all(); len(s) != 1 || !s[0].Failed {
		t.Fatalf("failed call not recorded: %+v", s)
	}

	// Canceled mid-stream.
	cap2 := &sinkCapture{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p2 := Metered(&meterStub{name: "m", deltas: []Delta{{Content: "a"}}, delay: 20 * time.Millisecond}, "t", "main", cap2.sink())
	ch, err := p2.Complete(ctx, []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	drainMetered(t, ch)
	deadline := time.Now().Add(2 * time.Second)
	for len(cap2.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s2 := cap2.all()
	if len(s2) != 1 || !s2[0].Canceled {
		t.Fatalf("canceled call not recorded: %+v", s2)
	}
}

func TestMetered_NilSinkOrInnerPassthrough(t *testing.T) {
	inner := &meterStub{name: "m"}
	if got := Metered(inner, "t", "main", nil); got != Provider(inner) {
		t.Error("nil sink must return inner unchanged")
	}
	if got := Metered(nil, "t", "main", (&sinkCapture{}).sink()); got != nil {
		t.Error("nil inner must return nil")
	}
}

func TestUnwrap(t *testing.T) {
	inner := &meterStub{name: "m"}
	wrapped := Metered(inner, "t", "main", (&sinkCapture{}).sink())
	if got := Unwrap(wrapped); got != Provider(inner) {
		t.Errorf("Unwrap = %v, want inner", got)
	}
	if got := Unwrap(inner); got != Provider(inner) {
		t.Error("Unwrap on plain provider must be identity")
	}
	if got := Unwrap(nil); got != nil {
		t.Error("Unwrap(nil) must be nil")
	}
}
