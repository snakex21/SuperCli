package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestRouter_MagazineSticksToActiveUntilFailure(t *testing.T) {
	// Magazine strategy: all requests drain account A while it is
	// healthy; account B is untouched until A fails.
	a := &scriptedProvider{name: "a", deltas: []Delta{{FinishReason: "stop"}}}
	b := &scriptedProvider{name: "b", deltas: []Delta{{FinishReason: "stop"}}}
	r, _ := NewRouter(a, b)
	for i := 0; i < 4; i++ {
		ch, _ := r.Complete(context.Background(), nil, nil)
		drainRouter(t, ch)
	}
	if a.callCount != 4 || b.callCount != 0 {
		t.Errorf("magazine should drain A first: a=%d b=%d, want 4/0", a.callCount, b.callCount)
	}
	if r.ActiveIndex() != 0 {
		t.Errorf("active should still be A (0), got %d", r.ActiveIndex())
	}
}

func TestRouter_MagazineAdvancesAfterFailure(t *testing.T) {
	// A fails to start → magazine advances to B; subsequent calls
	// then stick to B.
	a := &scriptedProvider{name: "a", startErr: errors.New("429")}
	b := &scriptedProvider{name: "b", deltas: []Delta{{Content: "ok"}, {FinishReason: "stop"}}}
	r, _ := NewRouter(a, b)

	ch, _ := r.Complete(context.Background(), nil, nil)
	drainRouter(t, ch)
	if r.ActiveIndex() != 1 {
		t.Fatalf("after A failed, active should be B (1), got %d", r.ActiveIndex())
	}
	// Next call should go straight to B, not retry A.
	aBefore := a.callCount
	ch2, _ := r.Complete(context.Background(), nil, nil)
	drainRouter(t, ch2)
	if a.callCount != aBefore {
		t.Errorf("A should not be retried once magazine moved past it (a=%d, was %d)", a.callCount, aBefore)
	}
	if b.callCount < 2 {
		t.Errorf("B should serve subsequent calls, got callCount=%d", b.callCount)
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

// usageProvider is a scriptedProvider that also reports a fixed
// RateLimits snapshot, so PoolUsage/PoolAggregate can be tested.
type usageProvider struct {
	scriptedProvider
	rl CodexRateLimits
}

func (p *usageProvider) RateLimits() (CodexRateLimits, bool) { return p.rl, p.rl.OK }

func TestRouter_PoolAggregate_AveragesAcrossAccounts(t *testing.T) {
	a := &usageProvider{scriptedProvider: scriptedProvider{name: "a"}, rl: CodexRateLimits{PrimaryUsedPct: 0, SecondaryUsedPct: 0, OK: true}}
	b := &usageProvider{scriptedProvider: scriptedProvider{name: "b"}, rl: CodexRateLimits{PrimaryUsedPct: 12, SecondaryUsedPct: 20, OK: true}}
	r, _ := NewRouter(a, b)
	p5, p7, n := r.PoolAggregate()
	if n != 2 || p5 != 6 || p7 != 10 {
		t.Errorf("PoolAggregate = (%d,%d,%d), want (6,10,2) - each account weighs half", p5, p7, n)
	}
}

func TestRouter_PoolAggregate_ThreeAccounts(t *testing.T) {
	mk := func(name string, p int) *usageProvider {
		return &usageProvider{scriptedProvider: scriptedProvider{name: name}, rl: CodexRateLimits{PrimaryUsedPct: p, OK: true}}
	}
	r, _ := NewRouter(mk("a", 0), mk("b", 0), mk("c", 30))
	p5, _, n := r.PoolAggregate()
	if n != 3 || p5 != 10 {
		t.Errorf("PoolAggregate 5h = %d over %d accts, want 10 over 3 (each weighs 1/3)", p5, n)
	}
}

func TestRouter_PoolAggregate_SkipsAccountsWithoutData(t *testing.T) {
	a := &usageProvider{scriptedProvider: scriptedProvider{name: "a"}, rl: CodexRateLimits{PrimaryUsedPct: 20, OK: true}}
	b := &usageProvider{scriptedProvider: scriptedProvider{name: "b"}, rl: CodexRateLimits{OK: false}}
	r, _ := NewRouter(a, b)
	p5, _, n := r.PoolAggregate()
	if n != 1 || p5 != 20 {
		t.Errorf("PoolAggregate = (%d, n=%d), want (20, 1) - only accounts with data count", p5, n)
	}
}

// fetchUsageProvider simulates a CodexProvider: FetchUsage returns a
// preconfigured snapshot (or error) and, on success, stores it into rl
// just like setRateLimits does, so RateLimits() then reports it. It
// records how many times FetchUsage was called for assertions.
type fetchUsageProvider struct {
	scriptedProvider
	mu       sync.Mutex
	rl       CodexRateLimits
	fetched  CodexRateLimits // what FetchUsage will store
	fetchErr error
	calls    int
}

func (p *fetchUsageProvider) RateLimits() (CodexRateLimits, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rl, p.rl.OK
}

func (p *fetchUsageProvider) FetchUsage(ctx context.Context) (CodexRateLimits, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fetchErr != nil {
		return CodexRateLimits{}, p.fetchErr
	}
	p.rl = p.fetched
	return p.fetched, nil
}

func TestRouter_FetchUsageAll_RefreshesEveryAccount(t *testing.T) {
	a := &fetchUsageProvider{scriptedProvider: scriptedProvider{name: "a"},
		fetched: CodexRateLimits{PrimaryUsedPct: 2, SecondaryUsedPct: 10, OK: true}}
	b := &fetchUsageProvider{scriptedProvider: scriptedProvider{name: "b"},
		fetched: CodexRateLimits{PrimaryUsedPct: 8, SecondaryUsedPct: 20, OK: true}}
	r, _ := NewRouter(a, b)
	r.SetLabels([]string{"default", "drugie"})

	rl, err := r.FetchUsageAll(context.Background())
	if err != nil {
		t.Fatalf("FetchUsageAll err = %v, want nil", err)
	}
	// Returns the ACTIVE account's snapshot (account 0 = default).
	if !rl.OK || rl.PrimaryUsedPct != 2 {
		t.Errorf("active snapshot = %+v, want default's (2%%)", rl)
	}
	// BOTH accounts were fetched, not just the active one.
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("fetch calls: a=%d b=%d, want 1 each (every account refreshed)", a.calls, b.calls)
	}
	// Pool now aggregates BOTH accounts: (2+8)/2=5, (10+20)/2=15, n=2.
	p5, p7, n := r.PoolAggregate()
	if n != 2 || p5 != 5 || p7 != 15 {
		t.Errorf("PoolAggregate = (%d,%d,%d), want (5,15,2) after refreshing all", p5, p7, n)
	}
}

func TestRouter_FetchUsageAll_PartialFailureKeepsOthers(t *testing.T) {
	a := &fetchUsageProvider{scriptedProvider: scriptedProvider{name: "a"},
		fetched: CodexRateLimits{PrimaryUsedPct: 3, OK: true}}
	b := &fetchUsageProvider{scriptedProvider: scriptedProvider{name: "b"},
		fetchErr: errors.New("token expired")}
	r, _ := NewRouter(a, b)
	r.SetLabels([]string{"default", "drugie"})

	rl, err := r.FetchUsageAll(context.Background())
	if err == nil {
		t.Fatal("want error reporting the failed account")
	}
	if !strings.Contains(err.Error(), "drugie") {
		t.Errorf("err = %v, want it to name the failed account 'drugie'", err)
	}
	// Active account still refreshed despite the other's failure.
	if !rl.OK || rl.PrimaryUsedPct != 3 {
		t.Errorf("active snapshot = %+v, want default's (3%%) despite drugie failing", rl)
	}
	// Pool counts only the account that succeeded.
	_, _, n := r.PoolAggregate()
	if n != 1 {
		t.Errorf("PoolAggregate counted %d accounts, want 1 (only the one that refreshed)", n)
	}
}

func TestRouter_ActiveLabel(t *testing.T) {
	a := &scriptedProvider{name: "gpt-5.5"}
	b := &scriptedProvider{name: "gpt-5.5"}
	r, _ := NewRouter(a, b)
	// No labels yet: falls back to 1-based index.
	if got := r.ActiveLabel(); got != "1" {
		t.Errorf("ActiveLabel without labels = %q, want 1", got)
	}
	r.SetLabels([]string{"default", "drugie"})
	if got := r.ActiveLabel(); got != "default" {
		t.Errorf("ActiveLabel = %q, want default", got)
	}
	r.noteFailure(0) // advance magazine to account 2
	if got := r.ActiveLabel(); got != "drugie" {
		t.Errorf("after advance ActiveLabel = %q, want drugie", got)
	}
}
