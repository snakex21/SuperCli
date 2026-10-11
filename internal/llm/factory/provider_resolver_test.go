package factory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestProviderResolverDefersSnapshotsAndMetersOnce(t *testing.T) {
	temperature := 0.25
	cfg := config.Config{Provider: config.ProviderEcho, Model: "captured", Temperature: &temperature, Debug: true}
	var builds, calls atomic.Int32
	f := New(func(got config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds.Add(1)
		if got.Model != "captured" || got.Temperature == nil || *got.Temperature != 0.25 || !got.Debug {
			t.Fatalf("configuration snapshot changed: model=%s temperature=%v debug=%v", got.Model, got.Temperature, got.Debug)
		}
		return llm.NewEcho(got.Model)
	}, t.TempDir(), nil, func(stat llm.CallStat) {
		calls.Add(1)
		if stat.Purpose != llm.PurposeCompact || stat.Model != "captured" {
			t.Errorf("meter identity changed: %s/%s", stat.Purpose, stat.Model)
		}
	})
	resolve := f.ProviderResolver(cfg, llm.PurposeCompact)
	temperature, cfg.Model, cfg.Debug = 1.25, "later", false
	if builds.Load() != 0 {
		t.Fatal("unused resolver constructed a backend")
	}
	p, err := resolve(context.Background())
	if err != nil || p == nil || !llm.IsMetered(p) {
		t.Fatalf("resolver did not return genuine metered provider: %T %v", p, err)
	}
	for i := 0; i < 2; i++ {
		again, err := resolve(context.Background())
		if err != nil || again != p {
			t.Fatal("resolver rebuilt its provider")
		}
		stream, err := again.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "fixture"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for range stream {
		}
	}
	if builds.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("builds/metered calls=%d/%d", builds.Load(), calls.Load())
	}
}

func TestProviderResolverConcurrentConstructionAndIndependentScopes(t *testing.T) {
	var builds atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		if builds.Add(1) == 1 {
			close(started)
			<-release
		}
		return llm.NewEcho(cfg.Model)
	}, t.TempDir(), nil)
	cfg := config.Config{Provider: config.ProviderEcho, Model: "fixture"}
	resolve := f.ProviderResolver(cfg, llm.PurposeCompact)
	var wg sync.WaitGroup
	results := make(chan llm.Provider, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := resolve(context.Background())
			if err != nil {
				t.Error(err)
			}
			results <- p
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	var first llm.Provider
	for p := range results {
		if first == nil {
			first = p
		}
		if p == nil || p != first {
			t.Fatal("concurrent resolver returned different providers")
		}
	}
	if builds.Load() != 1 {
		t.Fatalf("concurrent construction count=%d", builds.Load())
	}
	other, err := f.ProviderResolver(cfg, llm.PurposeCompact)(context.Background())
	if err != nil || other == first || builds.Load() != 2 {
		t.Fatal("independent loop scopes shared provider construction")
	}
}

func TestProviderResolverFailureAndCancellation(t *testing.T) {
	var builds atomic.Int32
	failure := errors.New("backend construction failed")
	f := New(func(config.Config, string, *llm.CapabilityRegistry) (llm.Provider, error) {
		builds.Add(1)
		return nil, failure
	}, t.TempDir(), nil)
	resolve := f.ProviderResolver(config.Config{}, llm.PurposeCompact)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := resolve(cancelled); p != nil || !errors.Is(err, context.Canceled) || builds.Load() != 0 {
		t.Fatal("cancelled request started construction")
	}
	for i := 0; i < 2; i++ {
		if p, err := resolve(context.Background()); p != nil || !errors.Is(err, failure) {
			t.Fatal("ordinary failure changed")
		}
	}
	if builds.Load() != 1 {
		t.Fatal("ordinary failure repeated construction")
	}
}

func TestBuildContextCancelsDefaultDiscoveryWithoutGeneration(t *testing.T) {
	started := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected generation request: %s", r.URL.Path)
		}
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	f := New(nil, t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		p, err := f.BuildContext(ctx, config.Config{Provider: config.ProviderOpencode, BaseURL: srv.URL + "/v1", Model: "fixture"}, llm.PurposeCompact)
		if p != nil {
			t.Error("cancelled construction returned a provider")
		}
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || requests.Load() != 1 {
		t.Fatalf("discovery cancellation changed: %v requests=%d", err, requests.Load())
	}
}

func TestProviderResolverCancelledOwnerDoesNotPoisonNextCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var builds atomic.Int32
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		if builds.Add(1) == 1 {
			cancel()
		}
		return llm.NewEcho(cfg.Model)
	}, t.TempDir(), nil)
	resolve := f.ProviderResolver(config.Config{Provider: config.ProviderEcho, Model: "fixture"}, llm.PurposeCompact)
	if p, err := resolve(ctx); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled owner returned provider")
	}
	if p, err := resolve(context.Background()); p == nil || err != nil || builds.Load() != 2 {
		t.Fatal("cancelled construction poisoned the resolver")
	}
}

// Done signals entry into the pending-construction select, so cancellation
// tests don't need sleeps, progress polling or test hooks in production.
type resolverWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *resolverWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestProviderResolverFollowerCancellationDoesNotCancelOwner(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		builds.Add(1)
		close(started)
		<-release
		return llm.NewEcho(cfg.Model)
	}, t.TempDir(), nil)
	resolve := f.ProviderResolver(config.Config{Provider: config.ProviderEcho, Model: "fixture"}, llm.PurposeCompact)
	owner := make(chan llm.Provider, 1)
	go func() { p, _ := resolve(context.Background()); owner <- p }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := &resolverWaitingContext{Context: ctx, waiting: make(chan struct{})}
	follower := make(chan error, 1)
	go func() { _, err := resolve(waiting); follower <- err }()
	<-waiting.waiting
	cancel()
	if err := <-follower; !errors.Is(err, context.Canceled) {
		t.Fatalf("follower did not cancel its own wait: %v", err)
	}
	close(release)
	p := <-owner
	if p == nil || builds.Load() != 1 {
		t.Fatal("follower cancellation interrupted or duplicated owner")
	}
	if again, err := resolve(context.Background()); err != nil || again != p {
		t.Fatal("healthy owner was not retained")
	}
}

func TestProviderResolverHealthyFollowerRetriesCancelledOwner(t *testing.T) {
	ownerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var builds atomic.Int32
	f := New(func(cfg config.Config, _ string, _ *llm.CapabilityRegistry) (llm.Provider, error) {
		if builds.Add(1) == 1 {
			close(started)
			<-ownerCtx.Done()
		}
		return llm.NewEcho(cfg.Model)
	}, t.TempDir(), nil)
	resolve := f.ProviderResolver(config.Config{Provider: config.ProviderEcho, Model: "fixture"}, llm.PurposeCompact)
	owner := make(chan error, 1)
	go func() { _, err := resolve(ownerCtx); owner <- err }()
	<-started
	waiting := &resolverWaitingContext{Context: context.Background(), waiting: make(chan struct{})}
	follower := make(chan llm.Provider, 1)
	go func() { p, _ := resolve(waiting); follower <- p }()
	<-waiting.waiting
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner cancellation changed: %v", err)
	}
	p := <-follower
	if p == nil || builds.Load() != 2 {
		t.Fatal("healthy follower inherited cancelled construction")
	}
	if again, err := resolve(context.Background()); err != nil || again != p || builds.Load() != 2 {
		t.Fatal("healthy retry did not become the retained provider")
	}
}
