package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

func TestCanceledTaskDoesNotPrepareOrRegisterWorker(t *testing.T) {
	var providers []llm.Provider
	at := newModelTestTool(t, &providers)
	prepared := 0
	at.Preflight = func() string { prepared++; return "repo context" }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := at.execute(ctx, json.RawMessage(`{"prompt":"inspect"}`))
	if err != nil || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if prepared != 0 || len(providers) != 0 || len(at.Workers.List()) != 0 {
		t.Fatalf("canceled task prepared=%d loops=%d workers=%d", prepared, len(providers), len(at.Workers.List()))
	}
}

func TestCanceledWorkerProbeDoesNotPoisonNextTask(t *testing.T) {
	var providers []llm.Provider
	at := newModelTestTool(t, &providers)
	at.WorkerProvider = &stubReplyProvider{name: "selected-worker", reply: "worker done"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pings := 0
	at.WorkerPing = func(pctx context.Context) error {
		pings++
		if pings == 1 {
			cancel()
			<-pctx.Done()
			return pctx.Err()
		}
		return nil
	}
	result, err := at.execute(ctx, json.RawMessage(`{"prompt":"canceled"}`))
	if err != nil || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled result=%+v err=%v", result, err)
	}
	// The canceled probe must not create even an idle child loop.
	if len(providers) != 0 || len(at.Workers.List()) != 0 {
		t.Errorf("canceled probe registered a worker: loops=%d workers=%d", len(providers), len(at.Workers.List()))
		providers = nil
	}
	for i := 0; i < 2; i++ {
		result, err = at.execute(context.Background(), json.RawMessage(`{"prompt":"inspect"}`))
		if err != nil || result.Err != nil {
			t.Fatalf("retry result=%+v err=%v", result, err)
		}
	}
	if pings != 2 || len(providers) != 2 {
		t.Fatalf("pings=%d providers=%d", pings, len(providers))
	}
	for _, provider := range providers {
		if provider != at.WorkerProvider {
			t.Fatal("cancellation permanently switched subsequent tasks to the main model")
		}
	}
}

func TestWorkerProbeCanceledWaiterReturnsWhileProbeIsRunning(t *testing.T) {
	at := &AgentTool{Provider: &stubReplyProvider{name: "main"}, WorkerProvider: &stubReplyProvider{name: "worker"}}
	entered, release := make(chan struct{}), make(chan struct{})
	var pings atomic.Int32
	at.WorkerPing = func(context.Context) error { pings.Add(1); close(entered); <-release; return nil }
	finished := make(chan llm.Provider, 1)
	go func() { finished <- at.workerProvider(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{})
	observed := &workerProbeWaitContext{Context: ctx, waiting: waiting}
	canceledDone := make(chan llm.Provider, 1)
	go func() { canceledDone <- at.workerProvider(observed) }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		cancel()
		close(release)
		<-finished
		<-canceledDone
		t.Fatal("waiter did not subscribe to cancellation while sharing the probe")
	}
	cancel()
	select {
	case <-canceledDone:
	case <-time.After(time.Second):
		close(release)
		<-finished
		<-canceledDone
		t.Fatal("canceled waiter remained blocked behind an unrelated probe")
	}
	close(release)
	if got := <-finished; got != at.WorkerProvider {
		t.Fatal("healthy caller lost worker provider")
	}
	if got := at.workerProvider(context.Background()); got != at.WorkerProvider || pings.Load() != 1 {
		t.Fatalf("healthy result not cached: provider=%v pings=%d", got, pings.Load())
	}
}

func TestWorkerProbeGenuineFailureStillCachesFallback(t *testing.T) {
	at := &AgentTool{Provider: &stubReplyProvider{name: "main"}, WorkerProvider: &stubReplyProvider{name: "worker"}}
	calls := 0
	at.WorkerPing = func(context.Context) error { calls++; return context.DeadlineExceeded }
	for i := 0; i < 3; i++ {
		if got := at.workerProvider(context.Background()); got != at.Provider {
			t.Fatal("actual backend failure must retain fallback")
		}
	}
	if calls != 1 {
		t.Fatalf("repeated failed probe %d times", calls)
	}
}

// Done is inspected by the shared-probe select: the test can cancel an actual
// waiting caller without timing guesses or polling goroutine state.
type workerProbeWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *workerProbeWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestWorkerProbePanicReleasesFutureDelegation(t *testing.T) {
	at := &AgentTool{Provider: &stubReplyProvider{name: "main"}, WorkerProvider: &stubReplyProvider{name: "worker"}}
	calls := 0
	at.WorkerPing = func(context.Context) error {
		calls++
		if calls == 1 {
			panic("probe panic")
		}
		return nil
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("fixture did not panic")
			}
		}()
		at.workerProvider(context.Background())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := at.workerProvider(ctx); got != at.WorkerProvider || calls != 2 || ctx.Err() != nil {
		t.Fatalf("future probe blocked: calls=%d ctx=%v", calls, ctx.Err())
	}
}

func TestWorkerProbeHealthyWaiterRetriesCanceledOwner(t *testing.T) {
	at := &AgentTool{Provider: &stubReplyProvider{name: "main"}, WorkerProvider: &stubReplyProvider{name: "worker"}}
	entered := make(chan struct{})
	var calls atomic.Int32
	at.WorkerPing = func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	ownerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerDone := make(chan llm.Provider, 1)
	go func() { ownerDone <- at.workerProvider(ownerCtx) }()
	<-entered
	waiting := make(chan struct{})
	healthyCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	healthyDone := make(chan llm.Provider, 1)
	go func() {
		healthyDone <- at.workerProvider(&workerProbeWaitContext{Context: healthyCtx, waiting: waiting})
	}()
	select {
	case <-waiting:
	case <-healthyCtx.Done():
		cancel()
		<-ownerDone
		<-healthyDone
		t.Fatal("healthy caller did not wait for existing probe")
	}
	cancel()
	<-ownerDone
	if got := <-healthyDone; got != at.WorkerProvider || calls.Load() != 2 || healthyCtx.Err() != nil {
		t.Fatalf("retry failed: calls=%d ctx=%v", calls.Load(), healthyCtx.Err())
	}
	if at.workerProvider(context.Background()) != at.WorkerProvider || calls.Load() != 2 {
		t.Fatal("successful retry was not cached")
	}
}
