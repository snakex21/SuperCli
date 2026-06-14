package llm

import (
	"context"
	"fmt"
	"sync"
)

// RouterProvider wraps a pool of providers and spreads requests
// across them round-robin, failing over to the next on error.
//
// It is a pure mechanism — it does not know or care what the
// underlying providers are (local model, several API keys, several
// accounts). The operator decides what goes in the pool and is
// responsible for that choice, including any provider terms of
// service around rotating multiple accounts. The router only does
// load-spreading + failover, exactly like a standard load balancer
// (nginx upstream, LiteLLM).
//
// Round-robin: each Complete starts at the next provider in the
// pool, so load is spread evenly across calls.
//
// Failover is SAFE-ONLY: the router buffers the stream from the
// chosen provider and switches to the next one only when an error
// arrives BEFORE any content/tool-call has been forwarded to the
// caller. Once real output has been emitted, a later error is
// passed through unchanged — never retried — so the caller can
// never see duplicated or interleaved output from two providers.
type RouterProvider struct {
	providers []Provider
	mu        sync.Mutex
	next      int // round-robin cursor
}

// NewRouter returns a RouterProvider over the given pool. The pool
// must be non-empty. Order matters: round-robin starts at index 0
// and the failover sequence follows pool order (wrapping around).
func NewRouter(pool ...Provider) (*RouterProvider, error) {
	if len(pool) == 0 {
		return nil, fmt.Errorf("llm.NewRouter: empty provider pool")
	}
	for i, p := range pool {
		if p == nil {
			return nil, fmt.Errorf("llm.NewRouter: provider %d is nil", i)
		}
	}
	return &RouterProvider{providers: pool}, nil
}

// Name reports the router and its pool size. The active provider
// changes per call, so the router reports its own stable identity.
func (r *RouterProvider) Name() string {
	if len(r.providers) == 1 {
		return r.providers[0].Name()
	}
	return fmt.Sprintf("router(%d providers)", len(r.providers))
}

// order returns the provider indices to try for this call, starting
// at the round-robin cursor and wrapping around the whole pool, then
// advances the cursor for the next call. Guarded by mu.
func (r *RouterProvider) order() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.providers)
	start := r.next
	r.next = (r.next + 1) % n
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, (start+i)%n)
	}
	return out
}

// Complete tries providers in round-robin order, failing over on an
// early error. It returns an output channel that the router owns and
// closes exactly once, preserving the Provider streaming contract.
func (r *RouterProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	seq := r.order()

	// Initiate the first provider synchronously so a hard config
	// error (Complete returning err) can fail over before we even
	// open the output channel — and so the caller sees a plain
	// error if EVERY provider refuses to start.
	var (
		stream <-chan Delta
		err    error
		firstErrs []string
		startIdx  int
	)
	consumed := 0
	for ; consumed < len(seq); consumed++ {
		startIdx = seq[consumed]
		stream, err = r.providers[startIdx].Complete(ctx, msgs, tools)
		if err == nil {
			break
		}
		firstErrs = append(firstErrs, fmt.Sprintf("%s: %v", r.providers[startIdx].Name(), err))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("llm.Router: all providers failed to start: %v", firstErrs)
	}

	out := make(chan Delta, 32)
	remaining := seq[consumed+1:]
	go r.relay(ctx, out, stream, msgs, tools, remaining, firstErrs)
	return out, nil
}

// relay forwards deltas from the active stream to out. If an error
// arrives before any real output was forwarded, it transparently
// fails over to the next provider in remaining. Once output has been
// forwarded, errors pass through and no failover happens.
func (r *RouterProvider) relay(ctx context.Context, out chan<- Delta, stream <-chan Delta, msgs []Message, tools []ToolDef, remaining []int, priorErrs []string) {
	defer close(out)
	emitted := false
	for {
		for d := range stream {
			if d.Err != nil && !emitted && len(remaining) > 0 {
				// Safe failover: nothing forwarded yet, try next.
				idx := remaining[0]
				remaining = remaining[1:]
				next, startErr := r.providers[idx].Complete(ctx, msgs, tools)
				if startErr != nil {
					priorErrs = append(priorErrs, fmt.Sprintf("%s: %v", r.providers[idx].Name(), startErr))
					// try the one after that on the next loop turn
					stream = closedErr(fmt.Errorf("%v", startErr))
					goto nextProvider
				}
				stream = next
				goto nextProvider
			}
			if d.Content != "" || d.ToolCall != nil {
				emitted = true
			}
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
		return
	nextProvider:
	}
}

// closedErr returns a pre-closed channel carrying a single error
// delta, so relay's failover can uniformly treat a Complete()
// start-failure like a stream error and advance to the next
// provider on the following loop turn.
func closedErr(err error) <-chan Delta {
	ch := make(chan Delta, 1)
	ch <- Delta{Err: err}
	close(ch)
	return ch
}
