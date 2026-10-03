package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
	labels    []string // optional human labels, 1:1 with providers
	mu        sync.Mutex
	active    int // most recently selected account, for usage/UI
	next      int // round-robin cursor for the next independent call
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

// ModelName is the undecorated model id used when rebuilding an account pool.
// Name remains a display label for compatibility with existing callers.
func (r *RouterProvider) ModelName() string { return r.providers[0].Name() }

// order reserves the next start slot, advancing once per independent call.
// Only fresh, explicit quota evidence excludes an account; missing or expired
// snapshots remain eligible. It does no network or catalog discovery.
func (r *RouterProvider) order() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.providers)
	start := r.next
	out := make([]int, 0, n)
	now := time.Now()
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if rp, ok := r.providers[idx].(interface {
			RateLimits() (CodexRateLimits, bool)
		}); ok {
			if limits, known := rp.RateLimits(); known && limits.ExhaustedAt(now) {
				continue
			}
		}
		out = append(out, idx)
	}
	if len(out) > 0 {
		r.next = (out[0] + 1) % n
	}
	return out
}

// noteFailure preserves the last attempted slot for usage/diagnostics.
// Failover does not consume another round-robin reservation.
func (r *RouterProvider) noteFailure(failedIdx int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if failedIdx == r.active {
		r.active = (r.active + 1) % len(r.providers)
	}
}

func (r *RouterProvider) noteAttempt(idx int) {
	r.mu.Lock()
	r.active = idx
	r.mu.Unlock()
}

// ActiveIndex reports which account slot is currently in use (0-based).
func (r *RouterProvider) ActiveIndex() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// SetLabels attaches human-readable labels (e.g. account names) to
// the pool, 1:1 with the providers passed to NewRouter. Extra or
// missing labels are tolerated (ActiveLabel falls back to an index
// string). Call once right after NewRouter.
func (r *RouterProvider) SetLabels(labels []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append([]string(nil), labels...)
}

// ActiveLabel returns the label of the active account, or a 1-based
// "N" string when no label is known. Used by the HUD to show WHICH
// account is in use, not just its slot number.
func (r *RouterProvider) ActiveLabel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active < len(r.labels) && r.labels[r.active] != "" {
		return r.labels[r.active]
	}
	return fmt.Sprintf("%d", r.active+1)
}

// LabelAt returns the label for pool slot i, or a 1-based "N"
// fallback. Used by /usage to name each account in the breakdown.
func (r *RouterProvider) LabelAt(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= 0 && i < len(r.labels) && r.labels[i] != "" {
		return r.labels[i]
	}
	return fmt.Sprintf("%d", i+1)
}

// Complete tries providers in round-robin order, failing over on an
// early error. It returns an output channel that the router owns and
// closes exactly once, preserving the Provider streaming contract.
func (r *RouterProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seq := r.order()
	if len(seq) == 0 {
		return nil, fmt.Errorf("llm.Router: all accounts have an observed exhausted quota")
	}

	// Initiate the first provider synchronously so a hard config
	// error (Complete returning err) can fail over before we even
	// open the output channel — and so the caller sees a plain
	// error if EVERY provider refuses to start.
	var (
		stream    <-chan Delta
		err       error
		firstErrs []string
		startIdx  int
	)
	consumed := 0
	for ; consumed < len(seq); consumed++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		startIdx = seq[consumed]
		r.noteAttempt(startIdx)
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
	go r.relay(ctx, out, stream, startIdx, msgs, tools, remaining, firstErrs)
	return out, nil
}

// relay forwards deltas from the active stream to out. If an error
// arrives before any real output was forwarded, it transparently
// fails over to the next provider in remaining. Once output has been
// forwarded, errors pass through and no failover happens. curIdx is
// the pool index of the stream currently being relayed.
func (r *RouterProvider) relay(ctx context.Context, out chan<- Delta, stream <-chan Delta, curIdx int, msgs []Message, tools []ToolDef, remaining []int, priorErrs []string) {
	defer close(out)
	emitted := false
	for {
		for d := range stream {
			if ctx.Err() != nil {
				return
			}
			if d.Content != "" || d.Reasoning != "" || d.NativeReasoning != nil || d.ToolCall != nil || d.OutputStarted || d.ReasoningStarted {
				emitted = true
			}
			if d.Err != nil && !emitted && len(remaining) > 0 {
				// Safe failover: nothing forwarded yet, try next.
				// Keep diagnostics on the account selected for this attempt.
				idx := remaining[0]
				remaining = remaining[1:]
				curIdx = idx
				r.noteAttempt(idx)
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
// and the HUD work behind the router. Other accounts retain their own
// snapshots for the dashboard. Providers without usage support cause a
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

// FetchUsageAll refreshes the usage snapshot for EVERY account in the
// pool, each with its own token — not just the active one. This is what
// allows the UI to show every account without combining unrelated quota
// denominators. It does not run automatically in the background.
//
// Each account's provider has its own CodexTokenSource (a per-account
// auth Manager), so fetching per-provider naturally uses the right
// token without ever switching the active account under the user.
//
// Manual fetches run serially under one overall timeout. A per-account
// failure is collected without discarding successful snapshots.
//
// It returns the active account's refreshed snapshot (so existing
// callers that want "the current account's numbers" keep working) and
// a combined error describing any per-account failures (nil when all
// accounts that support usage succeeded). Providers that do not support
// usage are skipped silently.
func (r *RouterProvider) FetchUsageAll(ctx context.Context) (CodexRateLimits, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	active := r.ActiveIndex()
	var activeRL CodexRateLimits
	var errs []string
	for i, p := range r.providers {
		f, ok := p.(interface {
			FetchUsage(context.Context) (CodexRateLimits, error)
		})
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: no usage", r.LabelAt(i)))
			continue
		}
		rl, err := f.FetchUsage(ctx)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", r.LabelAt(i), safeCodexUsageError(err)))
			continue
		}
		if i == active {
			activeRL = rl
		}
	}
	if len(errs) > 0 {
		return activeRL, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return activeRL, nil
}

// PoolUsageSummary counts account observations without averaging percentages
// across potentially different plans, window lengths or quota capacities.
func (r *RouterProvider) PoolUsageSummary() CodexUsageSummary {
	var summary CodexUsageSummary
	seen := make(map[string]bool, len(r.providers))
	for _, provider := range r.providers {
		if p, ok := Unwrap(provider).(*CodexProvider); ok && p.cfg.AccountID != "" {
			if seen[p.cfg.AccountID] {
				continue
			}
			seen[p.cfg.AccountID] = true
		}
		var snapshot *CodexUsageSnapshot
		if p, ok := provider.(interface {
			RateLimits() (CodexRateLimits, bool)
		}); ok {
			if limits, has := p.RateLimits(); has {
				snapshot = limits.Snapshot
			}
		}
		summary.add(snapshot, time.Now())
	}
	return summary
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

// PoolAggregate is a legacy arithmetic API, not a capacity or entitlement
// estimate. Deprecated: presentation must use PoolUsageSummary and each
// account's actual windows; account plans and denominators may differ.
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
