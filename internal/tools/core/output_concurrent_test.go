package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gatedOutputPersistence struct {
	reads atomic.Int32
	read  func(context.Context, string, int32) (string, error)
}

func (p *gatedOutputPersistence) SaveToolOutput(context.Context, string, string) error { return nil }
func (p *gatedOutputPersistence) ReadToolOutput(ctx context.Context, h string) (string, error) {
	return p.read(ctx, h, p.reads.Add(1))
}

// Done is evaluated when load enters its select, so this acknowledges an actual
// waiting caller without sleeps, polling, or timing assumptions.
type observedOutputWait struct {
	context.Context
	once    sync.Once
	waiting chan<- struct{}
}

func (c *observedOutputWait) Done() <-chan struct{} {
	c.once.Do(func() { c.waiting <- struct{}{} })
	return c.Context.Done()
}

type outputLoadResult struct {
	text string
	err  error
}

func waitOutputEvent[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("output load did not complete")
	}
	var zero T
	return zero
}
func beginOutputRead(s *OutputStore, ctx context.Context, h string) <-chan outputLoadResult {
	done := make(chan outputLoadResult, 1)
	go func() { text, err := s.load(ctx, h); done <- outputLoadResult{text, err} }()
	return done
}

func TestConcurrentOutputRestoreReadsPersistenceOnce(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	const text = "saved output \x00 Żółw 🙂"
	p := &gatedOutputPersistence{read: func(context.Context, string, int32) (string, error) {
		started <- struct{}{}
		<-release
		return text, nil
	}}
	s := NewOutputStore()
	leader := beginOutputRead(s, WithOutputPersistence(context.Background(), p), "out_one")
	waitOutputEvent(t, started)
	waiting := make(chan struct{}, 7)
	readers := []<-chan outputLoadResult{leader}
	for i := 0; i < 7; i++ {
		ctx := &observedOutputWait{Context: context.Background(), waiting: waiting}
		readers = append(readers, beginOutputRead(s, WithOutputPersistence(ctx, p), "out_one"))
	}
	for i := 0; i < 7; i++ {
		waitOutputEvent(t, waiting)
	}
	close(release)
	for _, done := range readers {
		result := waitOutputEvent(t, done)
		if result.err != nil || result.text != text {
			t.Fatalf("%+v", result)
		}
	}
	if p.reads.Load() != 1 {
		t.Fatalf("persistence reads=%d", p.reads.Load())
	}
}

func TestWaitingOutputCancellationDoesNotCancelOwner(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	p := &gatedOutputPersistence{read: func(ctx context.Context, _ string, _ int32) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "evidence", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}
	s := NewOutputStore()
	owner := beginOutputRead(s, WithOutputPersistence(context.Background(), p), "out_one")
	waitOutputEvent(t, started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan struct{}, 1)
	waiter := beginOutputRead(s, WithOutputPersistence(&observedOutputWait{Context: ctx, waiting: waiting}, p), "out_one")
	waitOutputEvent(t, waiting)
	cancel()
	if result := waitOutputEvent(t, waiter); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("%+v", result)
	}
	close(release)
	if result := waitOutputEvent(t, owner); result.err != nil || result.text != "evidence" {
		t.Fatalf("%+v", result)
	}
	if _, err := s.load(WithOutputPersistence(context.Background(), p), "out_one"); err != nil || p.reads.Load() != 1 {
		t.Fatalf("err=%v reads=%d", err, p.reads.Load())
	}
}

func TestCanceledOutputOwnerDoesNotPoisonLiveWaiter(t *testing.T) {
	started := make(chan struct{}, 1)
	p := &gatedOutputPersistence{read: func(ctx context.Context, _ string, n int32) (string, error) {
		if n == 1 {
			started <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "retried evidence", nil
	}}
	s := NewOutputStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := beginOutputRead(s, WithOutputPersistence(ctx, p), "out_one")
	waitOutputEvent(t, started)
	waiting := make(chan struct{}, 1)
	waiter := beginOutputRead(s, WithOutputPersistence(&observedOutputWait{Context: context.Background(), waiting: waiting}, p), "out_one")
	waitOutputEvent(t, waiting)
	cancel()
	if result := waitOutputEvent(t, owner); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("%+v", result)
	}
	if result := waitOutputEvent(t, waiter); result.err != nil || result.text != "retried evidence" {
		t.Fatalf("%+v", result)
	}
	if p.reads.Load() != 2 {
		t.Fatalf("reads=%d", p.reads.Load())
	}
}

func TestDifferentOutputsRestoreConcurrently(t *testing.T) {
	started, release := make(chan string, 2), make(chan struct{})
	p := &gatedOutputPersistence{read: func(_ context.Context, h string, _ int32) (string, error) {
		started <- h
		<-release
		return h, nil
	}}
	s := NewOutputStore()
	ctx := WithOutputPersistence(context.Background(), p)
	a, b := beginOutputRead(s, ctx, "a"), beginOutputRead(s, ctx, "b")
	first, second := waitOutputEvent(t, started), waitOutputEvent(t, started)
	if first == second {
		t.Fatalf("same handle: %s", first)
	}
	close(release)
	if r := waitOutputEvent(t, a); r.text != "a" || r.err != nil {
		t.Fatalf("%+v", r)
	}
	if r := waitOutputEvent(t, b); r.text != "b" || r.err != nil {
		t.Fatalf("%+v", r)
	}
}

func TestOutputRestoreFailureAllowsLaterRetry(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "read_error", true: "oversized"}[oversized], func(t *testing.T) {
			p := &gatedOutputPersistence{read: func(_ context.Context, _ string, n int32) (string, error) {
				if n == 1 {
					if oversized {
						return strings.Repeat("x", outputStoreBytes+1), nil
					}
					return "", errors.New("storage unavailable")
				}
				return "recovered", nil
			}}
			s := NewOutputStore()
			ctx := WithOutputPersistence(context.Background(), p)
			if text, err := s.load(ctx, "out_one"); err == nil || text != "" {
				t.Fatalf("text bytes=%d err=%v", len(text), err)
			}
			if text, err := s.load(ctx, "out_one"); err != nil || text != "recovered" {
				t.Fatalf("%q %v", text, err)
			}
		})
	}
}

func TestOutputRestorePanicReleasesWaiters(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	p := &gatedOutputPersistence{read: func(_ context.Context, _ string, n int32) (string, error) {
		if n == 1 {
			started <- struct{}{}
			<-release
			panic("backend failed")
		}
		return "recovered", nil
	}}
	s := NewOutputStore()
	owner := make(chan any, 1)
	go func() {
		defer func() { owner <- recover() }()
		_, _ = s.load(WithOutputPersistence(context.Background(), p), "out_one")
	}()
	waitOutputEvent(t, started)
	waiting := make(chan struct{}, 1)
	waiter := beginOutputRead(s, WithOutputPersistence(&observedOutputWait{Context: context.Background(), waiting: waiting}, p), "out_one")
	waitOutputEvent(t, waiting)
	close(release)
	if got := waitOutputEvent(t, owner); got != "backend failed" {
		t.Fatalf("owner panic=%v", got)
	}
	if result := waitOutputEvent(t, waiter); result.err == nil || !strings.Contains(result.err.Error(), "interrupted") {
		t.Fatalf("%+v", result)
	}
	if text, err := s.load(WithOutputPersistence(context.Background(), p), "out_one"); err != nil || text != "recovered" {
		t.Fatalf("%q %v", text, err)
	}
}
