package darwin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// stubLoop is a deterministic Loop for SpawnPool tests.
type stubLoop struct {
	script  []LoopEvent
	calls   *int32
	delay   time.Duration
	failNow error
}

func (s *stubLoop) Run(ctx context.Context, prompt string) (<-chan LoopEvent, error) {
	if s.failNow != nil {
		return nil, s.failNow
	}
	if s.calls != nil {
		atomic.AddInt32(s.calls, 1)
	}
	ch := make(chan LoopEvent, len(s.script))
	for _, e := range s.script {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// blockingLoop is a Loop that holds the channel open until ctx is cancelled.
type blockingLoop struct{}

func (b *blockingLoop) Run(ctx context.Context, prompt string) (<-chan LoopEvent, error) {
	ch := make(chan LoopEvent)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// providerStub is defined in darwin_test.go (white-box, same package).

func validPool(t *testing.T, factory LoopFactory) PoolConfig {
	t.Helper()
	return PoolConfig{
		Factory:  factory,
		Provider: providerStub{},
		Home:     t.TempDir(),
		System:   "you are a stub agent",
	}
}

// concurrencyLoop tracks how many loops run at once so a test can
// assert the sequential path never overlaps two agents.
type concurrencyLoop struct {
	active *int32
	maxObs *int32
}

func (c *concurrencyLoop) Run(ctx context.Context, prompt string) (<-chan LoopEvent, error) {
	n := atomic.AddInt32(c.active, 1)
	for {
		m := atomic.LoadInt32(c.maxObs)
		if n <= m || atomic.CompareAndSwapInt32(c.maxObs, m, n) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	atomic.AddInt32(c.active, -1)
	ch := make(chan LoopEvent, 1)
	ch <- LoopDoneEvent{Text: "done"}
	close(ch)
	return ch, nil
}

func TestSpawnPool_SequentialRunsOneAtATime(t *testing.T) {
	var active, maxObs int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &concurrencyLoop{active: &active, maxObs: &maxObs}, nil
	})
	cfg.PoolSize = 4
	cfg.Sequential = true
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := 0
	for ev := range ch {
		if _, ok := ev.(LoopDoneEvent); ok {
			done++
		}
	}
	if done != 4 {
		t.Fatalf("done events = %d, want 4", done)
	}
	if maxObs != 1 {
		t.Fatalf("max concurrent agents = %d, want 1 (sequential)", maxObs)
	}
}

func TestSpawnPool_ParallelOverlaps(t *testing.T) {
	var active, maxObs int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &concurrencyLoop{active: &active, maxObs: &maxObs}, nil
	})
	cfg.PoolSize = 4
	cfg.Sequential = false
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if maxObs < 2 {
		t.Fatalf("max concurrent agents = %d, want >=2 (parallel)", maxObs)
	}
}

func TestSpawnPool_NilFactory(t *testing.T) {
	_, err := SpawnPool(context.Background(), PoolConfig{
		Provider: providerStub{},
		Home:     t.TempDir(),
		System:   "sys",
	})
	if err == nil {
		t.Fatal("expected error for nil factory, got nil")
	}
}

func TestSpawnPool_NilProvider(t *testing.T) {
	_, err := SpawnPool(context.Background(), PoolConfig{
		Factory: func(LoopConfig) (Loop, error) { return &stubLoop{}, nil },
		Home:    t.TempDir(),
		System:  "sys",
	})
	if err == nil {
		t.Fatal("expected error for nil provider, got nil")
	}
}

func TestSpawnPool_EmptyHome(t *testing.T) {
	_, err := SpawnPool(context.Background(), PoolConfig{
		Factory:  func(LoopConfig) (Loop, error) { return &stubLoop{}, nil },
		Provider: providerStub{},
		System:   "sys",
	})
	if err == nil {
		t.Fatal("expected error for empty Home, got nil")
	}
}

func TestSpawnPool_EmptySystem(t *testing.T) {
	_, err := SpawnPool(context.Background(), PoolConfig{
		Factory:  func(LoopConfig) (Loop, error) { return &stubLoop{}, nil },
		Provider: providerStub{},
		Home:     t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for empty System, got nil")
	}
}

func TestSpawnPool_DefaultPoolSizeIs3(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	// PoolSize left at 0 to test the default.
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("default PoolSize expected to spawn 3 agents, got %d", got)
	}
}

func TestSpawnPool_PoolSizeClampedTo10(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	cfg.PoolSize = 50 // > maxPoolSize
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != int32(maxPoolSize) {
		t.Errorf("PoolSize > 10 should clamp to %d, got %d", maxPoolSize, got)
	}
}

func TestSpawnPool_NegativePoolSizeBecomesDefault(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	cfg.PoolSize = -1
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("negative PoolSize should default to 3, got %d", got)
	}
}

func TestSpawnPool_PoolSizeOneWorks(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	cfg.PoolSize = 1
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("PoolSize=1 should spawn 1 agent, got %d", got)
	}
}

func TestSpawnPool_AllAgentsRun(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	cfg.PoolSize = 3
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 factory calls, got %d", got)
	}
}

func TestSpawnPool_FactoryCallCountMatchesPoolSize(t *testing.T) {
	var calls int32
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "ok"}},
			calls:  &calls,
		}, nil
	})
	cfg.PoolSize = 5
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Errorf("expected 5 factory calls, got %d", got)
	}
}

func TestSpawnPool_PerAgentErrorBecomesLoopErrorEvent(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{failNow: errors.New("agent crashed")}, nil
	})
	cfg.PoolSize = 1
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var sawErr *LoopErrorEvent
	for ev := range ch {
		if e, ok := ev.(LoopErrorEvent); ok {
			sawErr = &e
		}
	}
	if sawErr == nil {
		t.Fatal("expected a LoopErrorEvent on the channel")
	}
	if sawErr.Err == nil || sawErr.Err.Error() != "agent crashed" {
		t.Errorf("error = %v, want 'agent crashed'", sawErr.Err)
	}
}

func TestSpawnPool_SuccessfulLoopEmitsLoopDoneEvent(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "final answer"}}}, nil
	})
	cfg.PoolSize = 1
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var sawDone *LoopDoneEvent
	for ev := range ch {
		if d, ok := ev.(LoopDoneEvent); ok {
			sawDone = &d
		}
	}
	if sawDone == nil {
		t.Fatal("expected a LoopDoneEvent on the channel")
	}
	if sawDone.Text != "final answer" {
		t.Errorf("text = %q, want 'final answer'", sawDone.Text)
	}
}

func TestSpawnPool_ContextCancelTerminatesPool(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &blockingLoop{}, nil
	})
	cfg.PoolSize = 3
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := SpawnPool(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Give the goroutines a chance to start, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
		// Pool closed all goroutines and the channel.
	case <-time.After(3 * time.Second):
		t.Fatal("pool did not terminate within 3s of cancel")
	}
}

func TestSpawnPool_ChannelIsClosedWhenAllAgentsFinish(t *testing.T) {
	cfg := validPool(t, func(LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "ok"}}}, nil
	})
	cfg.PoolSize = 3
	ch, err := SpawnPool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Drain the channel. The for-range will end only when ch is closed.
	count := 0
	for range ch {
		count++
	}
	if count == 0 {
		t.Error("expected some events, got none")
	}
}

func TestEmitOrSkip_NonBlockingWhenChannelFull(t *testing.T) {
	ch := make(chan LoopEvent, 1)
	ch <- LoopMessageEvent{Text: "filler"}
	ctx := context.Background()

	// This send should drop immediately via the default branch.
	done := make(chan struct{})
	go func() {
		emitOrSkip(ctx, ch, LoopMessageEvent{Text: "should-drop"})
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("emitOrSkip blocked on a full channel (default branch did not fire)")
	}
}

func TestEmitOrSkip_DropsAfterContextCancel(t *testing.T) {
	ch := make(chan LoopEvent, 1)
	// Pre-fill the channel with a single event so
	// the test can assert the pre-existing filler
	// remains untouched after the cancel-branch
	// call.
	ch <- LoopMessageEvent{Text: "pre-existing"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	// Should return immediately without sending.
	done := make(chan struct{})
	go func() {
		emitOrSkip(ctx, ch, LoopMessageEvent{Text: "x"})
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("emitOrSkip blocked after ctx cancel")
	}
	// Channel should still hold only the pre-existing filler.
	if got := len(ch); got != 1 {
		t.Errorf("expected 1 event in channel, got %d", got)
	}
}

func TestFmtAgentID(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "agent-1"},
		{2, "agent-3"},
		{-1, "agent-?"},
		{1000, "agent-?"},
		{999, "agent-1000"}, // upper bound still valid
	}
	for _, c := range cases {
		got := fmtAgentID(c.in)
		if got != c.want {
			t.Errorf("fmtAgentID(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
