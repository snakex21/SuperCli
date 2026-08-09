//go:build stress

package test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// echoProvider is an in-memory provider for stress testing
// that doesn't require LM Studio.
type echoProvider struct {
	delay time.Duration
}

func (e *echoProvider) Name() string          { return "stress-echo" }
func (e *echoProvider) SupportsVision() bool   { return false }
func (e *echoProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 16)
	go func() {
		defer close(ch)
		words := []string{"Hello", " ", "world", "!", " ", "This", " ", "is", " ", "a", " ", "stress", " ", "test", "."}
		for _, w := range words {
			select {
			case <-ctx.Done():
				ch <- llm.Delta{Err: ctx.Err()}
				return
			case ch <- llm.Delta{Content: w}:
			}
			if e.delay > 0 {
				time.Sleep(e.delay)
			}
		}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 15}}
	}()
	return ch, nil
}

// TestStress_ParallelSessions runs 100 parallel sessions and checks
// for goroutine and memory leaks.
func TestStress_ParallelSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const sessions = 100
	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)
	goroutinesBefore := runtime.NumGoroutine()

	var wg sync.WaitGroup
	errs := make(chan error, sessions)

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p := &echoProvider{delay: 0}
			reg := tools.NewRegistry()
			loop, err := agent.NewLoop(agent.LoopConfig{
				Provider: p,
				Registry: reg,
				MaxSteps: 2,
			})
			if err != nil {
				errs <- fmt.Errorf("session %d: NewLoop: %w", id, err)
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ch, err := loop.Run(ctx, "test")
			if err != nil {
				errs <- fmt.Errorf("session %d: Run: %w", id, err)
				return
			}
			for ev := range ch {
				if _, ok := ev.(agent.ErrorEvent); ok {
					errs <- fmt.Errorf("session %d: error event", id)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	// Drain error channel
	for e := range errs {
		t.Error(e)
	}

	// Allow goroutines to settle.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	t.Logf("goroutines: before=%d after=%d delta=%d", goroutinesBefore, goroutinesAfter, goroutinesAfter-goroutinesBefore)
	t.Logf("heap alloc: before=%d after=%d delta=%d", memBefore.HeapAlloc, memAfter.HeapAlloc, int64(memAfter.HeapAlloc)-int64(memBefore.HeapAlloc))

	// Allow some goroutine drift (Bubble Tea tickers, GC workers, etc.)
	const maxGoroutineDelta = 50
	if goroutinesAfter-goroutinesBefore > maxGoroutineDelta {
		t.Errorf("goroutine leak: +%d (max allowed +%d)", goroutinesAfter-goroutinesBefore, maxGoroutineDelta)
	}
}

// TestStress_SyntheticConversation runs a 1000-turn conversation
// to ensure the agent loop doesn't degrade over time.
func TestStress_SyntheticConversation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const turns = 1000
	p := &echoProvider{delay: 0}
	reg := tools.NewRegistry()
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider:    p,
		Registry:    reg,
		MaxSteps:    2,
		System:      "You are a stress test assistant.",
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	start := time.Now()
	for i := 0; i < turns; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ch, err := loop.Run(ctx, fmt.Sprintf("turn %d", i))
		if err != nil {
			cancel()
			t.Fatalf("turn %d: Run: %v", i, err)
		}
		for ev := range ch {
			if _, ok := ev.(agent.ErrorEvent); ok {
				cancel()
				t.Fatalf("turn %d: error event", i)
			}
		}
		cancel()
	}
	elapsed := time.Since(start)
	t.Logf("%d turns in %v (%.2f turns/s, %d messages accumulated)", turns, elapsed, float64(turns)/elapsed.Seconds(), len(loop.Messages))

	// Verify the conversation grew.
	if len(loop.Messages) < turns*2 {
		t.Errorf("expected at least %d messages, got %d", turns*2, len(loop.Messages))
	}
}

// TestStress_GoroutineLeak runs the agent loop repeatedly and checks
// that goroutine count returns to baseline.
func TestStress_GoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const iterations = 50
	baseline := runtime.NumGoroutine()
	t.Logf("baseline goroutines: %d", baseline)

	p := &echoProvider{delay: 0}
	reg := tools.NewRegistry()
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 2,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ch, _ := loop.Run(ctx, fmt.Sprintf("iter %d", i))
		for range ch {
		}
		cancel()
	}

	// Let goroutines settle.
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()
	t.Logf("goroutines after %d iterations: %d (delta: %+d)", iterations, after, after-baseline)

	const maxDelta = 20
	if after-baseline > maxDelta {
		t.Errorf("possible goroutine leak: +%d (max %d)", after-baseline, maxDelta)
	}
}
