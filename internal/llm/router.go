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
	active    int // "magazine" cursor: the account currently in use
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

// Name reports the model behind the pool. All pooled providers
// serve the same model (the pool is multiple ACCOUNTS for one
// model), so the router reports that model's name — not an opaque
// "router" string that would leak into the UI on a model swap.
// The pool size is appended in parentheses so multi-account is
// still visible without hiding the model.
func (r *RouterProvider) Name() string {
	if len(r.providers) == 1 {
		return r.providers[0].Name()
	}
	return fmt.Sprintf("%s (%d accounts)", r.providers[0].Name(), len(r.providers))
}

// order returns the provider indices to try for this call in
// "magazine" order: the currently-active account first, then the
// rest as failover, wrapping around. It does NOT advance the
// cursor — a request sticks to the active account until that
// account fails (see noteFailure), which is what makes one account
// drain before the next is touched. Guarded by mu.
func (r *RouterProvider) order() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.providers)
	start := r.active
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, (start+i)%n)
	}
	return out
}

// noteFailure advances the active account to idx+1 (mod n), but
// only if the failed account is still the active one — so the
// magazine moves forward exactly one slot per exhausted account and
// concurrent failures don't skip past healthy accounts. Called when
// a provider errors before emitting output and failover succeeds.
func (r *RouterProvider) noteFailure(failedIdx int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if failedIdx == r.active {
		r.active = (r.active + 1) % len(r.providers)
	}
}

// ActiveIndex reports which account slot is currently in use (0-based).
func (r *RouterProvider) ActiveIndex() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
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
		// This account could not start: advance the magazine past it
		// so the next request starts from a healthy account.
		r.noteFailure(startIdx)
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
	go r.relay(ctx, out, stream, startIdx, msgs, tools, remaining, firstErrs)
	return out, nil
}

// relay forwards deltas from the active stream to out. If an error
// arrives before any real output was forwarded, it transparently
// fails over to the next provider in remaining. Once output has been
// forwarded, errors pass through and no failover happens. curIdx is
// the pool index of the stream currently being relayed, used to
// advance the magazine cursor on failover.
func (r *RouterProvider) relay(ctx context.Context, out chan<- Delta, stream <-chan Delta, curIdx int, msgs []Message, tools []ToolDef, remaining []int, priorErrs []string) {
	defer close(out)
	emitted := false
	for {
		for d := range stream {
			if d.Err != nil && !emitted && len(remaining) > 0 {
				// Safe failover: nothing forwarded yet, try next.
				// Advance the magazine past the account that just
				// failed so future requests skip it too.
				r.noteFailure(curIdx)
				idx := remaining[0]
				remaining = remaining[1:]
				curIdx = idx
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

// FetchUsage delegates to the active account's provider so /usage
// and the HUD work behind the router. With the magazine strategy
// there is exactly one active account, so its usage is the usage
// that matters. Providers that do not support usage cause a
// graceful "not supported" error.
func (r *RouterProvider) FetchUsage(ctx context.Context) (CodexRateLimits, error) {
	p := r.providers[r.ActiveIndex()]
	f, ok := p.(interface {
		FetchUsage(context.Context) (CodexRateLimits, error)
	})
	if !ok {
		return CodexRateLimits{}, fmt.Errorf("llm.Router: active provider %q has no usage", p.Name())
	}
	return f.FetchUsage(ctx)
}

// RateLimits returns the active account's last known snapshot, so
// the HUD tile renders behind the router without a network call.
func (r *RouterProvider) RateLimits() (CodexRateLimits, bool) {
	p := r.providers[r.ActiveIndex()]
	rp, ok := p.(interface {
		RateLimits() (CodexRateLimits, bool)
	})
	if !ok {
		return CodexRateLimits{}, false
	}
	return rp.RateLimits()
}

// PoolUsage returns each account's label-less usage snapshot in pool
// order, with the active index — for UI that shows "this account +
// all accounts". Accounts without a snapshot yield (zero, false).
func (r *RouterProvider) PoolUsage() (snaps []CodexRateLimits, oks []bool, active int) {
	active = r.ActiveIndex()
	for _, p := range r.providers {
		if rp, ok := p.(interface {
			RateLimits() (CodexRateLimits, bool)
		}); ok {
			s, has := rp.RateLimits()
			snaps = append(snaps, s)
			oks = append(oks, has)
		} else {
			snaps = append(snaps, CodexRateLimits{})
			oks = append(oks, false)
		}
	}
	return snaps, oks, active
}

// PoolAggregate returns the pool-wide usage: the average 5h and 7d
// used-percent across all accounts that have a snapshot, plus how
// many accounts were counted. This is the "whole pool" figure —
// the point of the magazine strategy is summed capacity, so the
// user wants to see total headroom across every account, not just
// the active one. Averaging is right because the accounts have
// equal per-account limits: 0% + 12% over two accounts means ~6%
// of the combined capacity is spent. counted is 0 when no account
// has usable data yet (caller then shows nothing).
func (r *RouterProvider) PoolAggregate() (primaryPct, secondaryPct, counted int) {
	var pSum, sSum int
	for _, p := range r.providers {
		rp, ok := p.(interface {
			RateLimits() (CodexRateLimits, bool)
		})
		if !ok {
			continue
		}
		s, has := rp.RateLimits()
		if !has || !s.OK {
			continue
		}
		pSum += s.PrimaryUsedPct
		sSum += s.SecondaryUsedPct
		counted++
	}
	if counted == 0 {
		return 0, 0, 0
	}
	return pSum / counted, sSum / counted, counted
}
