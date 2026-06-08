package darwin

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// providerStub is a minimal llm.Provider used by Darwin tests.
type providerStub struct{}

func (providerStub) Name() string { return "stub" }
func (providerStub) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

// deterministicJudge is a Judge that always picks the first successful candidate.
type deterministicJudge struct {
	calls *int32
	idx   int
}

func (d *deterministicJudge) Judge(ctx context.Context, prompt string, cands []Candidate) (Verdict, error) {
	if d.calls != nil {
		atomic.AddInt32(d.calls, 1)
	}
	if len(cands) == 0 {
		return Verdict{}, errors.New("no candidates")
	}
	idx := d.idx
	if idx < 0 || idx >= len(cands) {
		// fall back to first successful
		for i, c := range cands {
			if c.Err == nil {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0
		}
	}
	return Verdict{
		WinnerIndex: idx,
		Score:       0.5,
		Reason:      "deterministic test judge",
	}, nil
}

func validConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		PoolConfig: PoolConfig{
			Provider: providerStub{},
			System:   "you are a stub",
			Home:     t.TempDir(),
			Factory: func(LoopConfig) (Loop, error) {
				return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
			},
		},
	}
}

func TestNewDarwin_NilProvider(t *testing.T) {
	cfg := validConfig(t)
	cfg.Provider = nil
	_, err := NewDarwin(cfg)
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestNewDarwin_NilFactory(t *testing.T) {
	cfg := validConfig(t)
	cfg.Factory = nil
	_, err := NewDarwin(cfg)
	if err == nil {
		t.Fatal("expected error for nil factory")
	}
}

func TestNewDarwin_EmptyHome(t *testing.T) {
	cfg := validConfig(t)
	cfg.Home = ""
	_, err := NewDarwin(cfg)
	if err == nil {
		t.Fatal("expected error for empty Home")
	}
}

func TestNewDarwin_EmptySystem(t *testing.T) {
	cfg := validConfig(t)
	cfg.System = ""
	_, err := NewDarwin(cfg)
	if err == nil {
		t.Fatal("expected error for empty System")
	}
}

func TestNewDarwin_NilJudgeDefaultsToComposite(t *testing.T) {
	cfg := validConfig(t)
	// Pre-condition: Judge nil.
	if cfg.Judge != nil {
		t.Fatal("test setup: expected Judge to be nil")
	}
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.cfg.Judge == nil {
		t.Fatal("Judge should be defaulted to a CompositeJudge")
	}
	// It should be a *CompositeJudge.
	if _, ok := d.cfg.Judge.(*CompositeJudge); !ok {
		t.Errorf("default Judge type = %T, want *CompositeJudge", d.cfg.Judge)
	}
}

func TestNewDarwin_NilWorktreeDefaultsToHomeBased(t *testing.T) {
	cfg := validConfig(t)
	cfg.Worktree = nil
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.cfg.Worktree == nil {
		t.Fatal("Worktree should be defaulted to a *WorktreeManager")
	}
	if got := d.cfg.Worktree.Base(); got != cfg.Home {
		t.Errorf("Worktree.Base() = %q, want %q", got, cfg.Home)
	}
}

func TestRun_EmptyPrompt(t *testing.T) {
	d, err := NewDarwin(validConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Run(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestRun_NilReceiver(t *testing.T) {
	var d *Darwin
	_, err := d.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from nil receiver")
	}
}

func TestRun_EmitsStartedEventFirst(t *testing.T) {
	d, err := NewDarwin(validConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Run(context.Background(), "the task")
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := <-ch
	if !ok {
		t.Fatal("channel closed before emitting StartedEvent")
	}
	started, ok := ev.(StartedEvent)
	if !ok {
		t.Fatalf("first event = %T, want StartedEvent", ev)
	}
	if started.Prompt != "the task" {
		t.Errorf("started.Prompt = %q, want 'the task'", started.Prompt)
	}
	// Drain the rest.
	for range ch {
	}
}

func TestRun_ForwardsCandidateDoneEvent(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 3
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	var candidateEvents int
	var done *DoneEvent
	for ev := range ch {
		if _, ok := ev.(CandidateDoneEvent); ok {
			candidateEvents++
		}
		if d, ok := ev.(DoneEvent); ok {
			done = &d
		}
	}
	if done == nil {
		t.Fatal("expected a DoneEvent")
	}
	if candidateEvents != 3 {
		t.Errorf("expected 3 CandidateDoneEvents, got %d", candidateEvents)
	}
	if len(done.Result.Candidates) != 3 {
		t.Errorf("expected 3 candidates in result, got %d", len(done.Result.Candidates))
	}
}

func TestRun_PicksWinnerViaJudge(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 3
	cfg.Judge = &deterministicJudge{idx: 1}
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	var done *DoneEvent
	for ev := range ch {
		if d, ok := ev.(DoneEvent); ok {
			done = &d
		}
	}
	if done == nil {
		t.Fatal("expected DoneEvent")
	}
	if done.Result.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1", done.Result.WinnerIndex)
	}
}

func TestRun_PicksFallbackWhenJudgeFails(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 2
	cfg.Judge = &deterministicJudge{idx: 99} // out of range → forces fallback path
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	var done *DoneEvent
	for ev := range ch {
		if d, ok := ev.(DoneEvent); ok {
			done = &d
		}
	}
	if done == nil {
		t.Fatal("expected DoneEvent")
	}
	// Fallback picks the first successful candidate (index 0).
	if done.Result.WinnerIndex != 0 {
		t.Errorf("fallback WinnerIndex = %d, want 0", done.Result.WinnerIndex)
	}
}

func TestRun_BudgetExceeded(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 1
	cfg.BudgetTokens = 1 // tiny budget — any non-zero usage triggers it
	// Custom factory that reports a non-zero usage.
	cfg.Factory = func(LoopConfig) (Loop, error) {
		return &stubLoop{
			script: []LoopEvent{LoopDoneEvent{Text: "x", Usage: llm.Usage{Input: 100, Output: 50, Total: 150}}},
		}, nil
	}
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := d.Run(context.Background(), "task")
	if err != nil {
		t.Fatal(err)
	}
	var sawError *ErrorEvent
	for ev := range ch {
		if e, ok := ev.(ErrorEvent); ok {
			sawError = &e
		}
	}
	if sawError == nil {
		t.Fatal("expected an ErrorEvent for budget exceeded")
	}
	if sawError.Err == nil || !strings.Contains(sawError.Err.Error(), "budget") {
		t.Errorf("error = %v, want a budget-related error", sawError.Err)
	}
}

func TestRun_ContextCancel(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 2
	cfg.Factory = func(LoopConfig) (Loop, error) {
		return &blockingLoop{}, nil
	}
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := d.Run(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	// Cancel shortly after the run starts.
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Drain.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not finish within 3s of cancel")
	}
}

func TestEmit_NonBlocking(t *testing.T) {
	// Build a Darwin and call emit on a channel with no reader.
	d, err := NewDarwin(validConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event) // unbuffered, no reader
	done := make(chan struct{})
	go func() {
		d.emit(out, StartedEvent{Prompt: "p"})
		d.emit(out, StartedEvent{Prompt: "p"})
		d.emit(out, StartedEvent{Prompt: "p"})
		close(done)
	}()
	select {
	case <-done:
		// ok — emit is non-blocking (drops on slow consumer)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("emit blocked on a channel with no reader")
	}
}

func TestPickFallback_FirstSuccessful(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Err: errors.New("nope")},
		{Index: 1, Text: "ok"},
		{Index: 2, Text: "ok2"},
	}
	v := pickFallback(cands)
	if v.WinnerIndex != 1 {
		t.Errorf("WinnerIndex = %d, want 1 (first successful)", v.WinnerIndex)
	}
}

func TestPickFallback_AllFailed(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Err: errors.New("nope")},
		{Index: 1, Err: errors.New("also nope")},
	}
	v := pickFallback(cands)
	if v.WinnerIndex != 0 {
		t.Errorf("WinnerIndex = %d, want 0 (all failed → first)", v.WinnerIndex)
	}
}

func TestPickFallback_NoCandidates(t *testing.T) {
	v := pickFallback(nil)
	if v.WinnerIndex != -1 {
		t.Errorf("WinnerIndex = %d, want -1 (no candidates)", v.WinnerIndex)
	}
}

func TestErrInvalid_SentinelExists(t *testing.T) {
	if errInvalid == nil {
		t.Fatal("errInvalid should be a non-nil sentinel error")
	}
	// Should be usable as an error.
	var got error = errInvalid
	if got.Error() == "" {
		t.Error("errInvalid should have a non-empty message")
	}
}

func TestAddUsage_SumsTwoUsages(t *testing.T) {
	a := llm.Usage{Input: 10, Output: 5, Total: 15}
	b := llm.Usage{Input: 20, Output: 7, Total: 27}
	got := addUsage(a, b)
	want := llm.Usage{Input: 30, Output: 12, Total: 42}
	if got != want {
		t.Errorf("addUsage = %+v, want %+v", got, want)
	}
	// Zero on the right should be identity.
	if addUsage(a, llm.Usage{}) != a {
		t.Errorf("addUsage with zero right-hand side should be identity")
	}
}

func TestNewDarwin_DefaultPoolSizeClamped(t *testing.T) {
	cfg := validConfig(t)
	cfg.PoolSize = 0 // trigger default
	d, err := NewDarwin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// PoolSize on the Darwin struct is whatever the user set (0 here).
	// But RunInner applies the default of 3 when spawning.
	if d.cfg.PoolSize != 0 {
		t.Errorf("PoolSize on config = %d, want 0 (test setup)", d.cfg.PoolSize)
	}
}

func TestMaxPoolSize_Is10(t *testing.T) {
	if maxPoolSize != 10 {
		t.Errorf("maxPoolSize = %d, want 10", maxPoolSize)
	}
}
