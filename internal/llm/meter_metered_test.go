package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestMeteredRecordsPrefillTelemetryAndBudget(t *testing.T) {
	cap := &sinkCapture{}
	p := Metered(&meterStub{
		name:  "m",
		delay: 2 * time.Millisecond,
		deltas: []Delta{
			{Notice: "queued"},
			{Content: "ok"},
			{Usage: &Usage{Input: 1000, CachedInput: 750, Output: 1}},
		},
	}, "openai", PurposeMain, cap.sink())
	ctx := WithPrefillBudget(context.Background(), 20_000, "prefill-profile")
	ch, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainMetered(t, ch)
	s := cap.all()[0]
	if s.PrefillEvaluated != 250 || s.PrefillTokensPerSecond <= 0 {
		t.Fatalf("prefill telemetry = %+v", s)
	}
	if s.PrefillBudget != 20_000 || s.PrefillBudgetSource != "prefill-profile" {
		t.Fatalf("prefill budget telemetry = %+v", s)
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

// holdStub is a provider whose stream stays open until release is
// closed — it lets the gate tests hold the background semaphore
// for a controlled window.
type holdStub struct {
	name    string
	release chan struct{}
	started chan struct{}
}

func (p *holdStub) Name() string { return p.name }

func (p *holdStub) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	ch := make(chan Delta)
	go func() {
		defer close(ch)
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// TestMetered_ForegroundPreemptsStreamingBackground proves the part
// that the old background semaphore could not provide: a helper that
// already owns the backend slot is canceled when user work arrives.
func TestMetered_ForegroundPreemptsStreamingBackground(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	bgStats := &sinkCapture{}
	bg := Metered(&holdStub{name: "bg", release: release, started: started}, "t", "memory", bgStats.sink())

	bgCh, err := bg.Complete(WithBackground(context.Background()), nil, nil)
	if err != nil {
		t.Fatalf("background Complete: %v", err)
	}
	bgDone := make(chan struct{})
	go func() { defer close(bgDone); drainMetered(t, bgCh) }()
	<-started

	fg := Metered(&meterStub{name: "fg", deltas: []Delta{{Content: "ok"}}}, "t", "main", (&sinkCapture{}).sink())
	fgCh, err := fg.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("foreground Complete: %v", err)
	}
	drainMetered(t, fgCh)

	select {
	case <-bgDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming background was not preempted by foreground")
	}
	stats := bgStats.all()
	if len(stats) != 1 || !stats[0].Canceled || !stats[0].Background {
		t.Fatalf("preempted background stat = %+v, want Canceled+Background", stats)
	}
	close(release)
}

// TestMetered_BackgroundWaitsForAllForeground streams guards against
// the race left by a cancel-only implementation: helper work starting
// AFTER foreground must still wait, and two concurrent foreground
// streams keep it waiting until both are done.
func TestMetered_BackgroundWaitsForAllForeground(t *testing.T) {
	fgRelease1 := make(chan struct{})
	fgRelease2 := make(chan struct{})
	fg1 := Metered(&holdStub{name: "fg1", release: fgRelease1}, "t", "main", (&sinkCapture{}).sink())
	fg2 := Metered(&holdStub{name: "fg2", release: fgRelease2}, "t", "main", (&sinkCapture{}).sink())

	fgCh1, err := fg1.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fgCh2, err := fg2.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go drainMetered(t, fgCh1)
	go drainMetered(t, fgCh2)

	bgStarted := make(chan struct{}, 1)
	bgRelease := make(chan struct{})
	bg := Metered(&holdStub{name: "bg", release: bgRelease, started: bgStarted}, "t", "memory", (&sinkCapture{}).sink())
	bgDone := make(chan error, 1)
	go func() {
		ch, err := bg.Complete(WithBackground(context.Background()), nil, nil)
		if err == nil {
			drainMetered(t, ch)
		}
		bgDone <- err
	}()

	select {
	case <-bgStarted:
		t.Fatal("background entered provider while foreground was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(fgRelease1)
	select {
	case <-bgStarted:
		t.Fatal("background entered provider while second foreground was active")
	case <-time.After(50 * time.Millisecond):
	}
	close(fgRelease2)
	select {
	case <-bgStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background did not start after all foreground streams finished")
	}
	close(bgRelease)
	if err := <-bgDone; err != nil {
		t.Fatalf("background Complete after idle: %v", err)
	}
}

// TestMetered_BackgroundGateSerializes: at most ONE background
// model call runs at a time — a second background Complete blocks
// until the first stream closes.
func TestMetered_BackgroundGateSerializes(t *testing.T) {
	release := make(chan struct{})
	p := Metered(&holdStub{name: "m", release: release}, "t", "main", (&sinkCapture{}).sink())
	bctx := WithBackground(context.Background())

	// First background call: acquires the gate and holds it while
	// its stream is open.
	ch1, err := p.Complete(bctx, nil, nil)
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); drainMetered(t, ch1) }()

	// Second background call: must NOT get past the gate yet.
	var secondStarted atomic.Bool
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		ch2, err := p.Complete(bctx, nil, nil)
		secondStarted.Store(true)
		if err != nil {
			t.Errorf("second Complete: %v", err)
			return
		}
		drainMetered(t, ch2)
	}()
	time.Sleep(50 * time.Millisecond)
	if secondStarted.Load() {
		t.Fatal("second background call ran while the first held the gate")
	}

	close(release) // first stream closes → gate released → second runs
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second background call never ran after the gate was released")
	}
	<-firstDone
}

// TestMetered_ForegroundNeverWaitsForGate: a foreground call must
// complete even while a background call holds the gate.
func TestMetered_ForegroundNeverWaitsForGate(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	p := Metered(&holdStub{name: "m", release: release}, "t", "main", (&sinkCapture{}).sink())

	ch1, err := p.Complete(WithBackground(context.Background()), nil, nil)
	if err != nil {
		t.Fatalf("background Complete: %v", err)
	}
	go drainMetered(t, ch1) // holds the gate until release closes

	fg := Metered(&meterStub{name: "m", deltas: []Delta{{Content: "ok"}}}, "t", "main", (&sinkCapture{}).sink())
	fgDone := make(chan struct{})
	go func() {
		defer close(fgDone)
		ch, err := fg.Complete(context.Background(), nil, nil)
		if err != nil {
			t.Errorf("foreground Complete: %v", err)
			return
		}
		drainMetered(t, ch)
	}()
	select {
	case <-fgDone:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground call waited behind the background gate")
	}
}

// TestMetered_BackgroundGateCancelWhileWaiting: canceling the
// context of a background call that is queued on the gate returns
// promptly with a canceled stat (the memory saver relies on this —
// a new user prompt must be able to abandon a queued autosave).
func TestMetered_BackgroundGateCancelWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	p := Metered(&holdStub{name: "m", release: release}, "t", "main", (&sinkCapture{}).sink())

	ch1, err := p.Complete(WithBackground(context.Background()), nil, nil)
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	go drainMetered(t, ch1) // holds the gate

	cap2 := &sinkCapture{}
	p2 := Metered(&holdStub{name: "m", release: release}, "t", "main", cap2.sink())
	ctx, cancel := context.WithCancel(WithBackground(context.Background()))
	errCh := make(chan error, 1)
	go func() {
		_, err := p2.Complete(ctx, nil, nil)
		errCh <- err
	}()
	time.Sleep(30 * time.Millisecond) // let it queue on the gate
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("queued background call returned nil error after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued background call did not return after cancel")
	}
	stats := cap2.all()
	if len(stats) != 1 || !stats[0].Canceled || !stats[0].Background {
		t.Fatalf("canceled queued call stat = %+v, want Canceled+Background", stats)
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

// BenchmarkMeteredStreamOverhead measures the decorator itself, not
// model latency. The same scripted provider and delta slice are used
// in both cases; the delta-normalized metric makes regressions in the
// hot streaming path visible as ns/delta and allocs/op.
func BenchmarkMeteredStreamOverhead(b *testing.B) {
	const deltaCount = 1000
	deltas := make([]Delta, deltaCount)
	for i := range deltas {
		deltas[i] = Delta{Content: "x"}
	}
	deltas[deltaCount-1].Usage = &Usage{Input: 100, Output: deltaCount}

	run := func(b *testing.B, p Provider) {
		b.Helper()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ch, err := p.Complete(context.Background(), nil, nil)
			if err != nil {
				b.Fatal(err)
			}
			for range ch {
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*deltaCount), "ns/delta")
	}

	b.Run("bare", func(b *testing.B) {
		run(b, &meterStub{name: "bare", deltas: deltas})
	})
	b.Run("metered", func(b *testing.B) {
		p := Metered(&meterStub{name: "metered", deltas: deltas}, "test", PurposeMain, func(CallStat) {})
		run(b, p)
	})
}
