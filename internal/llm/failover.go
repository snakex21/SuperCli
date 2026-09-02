package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FailoverProvider is an ordered, opt-in chain of potentially different
// models/providers. Unlike RouterProvider's account magazine it always prefers
// slot zero and only skips a failed backend for a bounded cooldown.
//
// The safety rule is identical to RouterProvider: another backend is attempted
// only before any content or tool call has reached the caller.
type FailoverProvider struct {
	providers   []Provider
	labels      []string
	cooldown    time.Duration
	mu          sync.Mutex
	failedUntil []time.Time
	failureRuns []int
	active      int
}

func NewFailover(cooldown time.Duration, labels []string, providers ...Provider) (*FailoverProvider, error) {
	if len(providers) < 2 {
		return nil, fmt.Errorf("llm.NewFailover: need primary and at least one fallback")
	}
	for i, p := range providers {
		if p == nil {
			return nil, fmt.Errorf("llm.NewFailover: provider %d is nil", i)
		}
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &FailoverProvider{
		providers:   append([]Provider(nil), providers...),
		labels:      append([]string(nil), labels...),
		cooldown:    cooldown,
		failedUntil: make([]time.Time, len(providers)),
		failureRuns: make([]int, len(providers)),
	}, nil
}

// Name preserves the selected primary model in pickers and session metadata.
func (f *FailoverProvider) Name() string { return f.providers[0].Name() }

func (f *FailoverProvider) order(now time.Time) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	ready, cooling := make([]int, 0, len(f.providers)), make([]int, 0, len(f.providers))
	for i := range f.providers {
		if now.Before(f.failedUntil[i]) {
			cooling = append(cooling, i)
		} else {
			ready = append(ready, i)
		}
	}
	if len(ready) == 0 {
		return append([]int(nil), cooling...)
	}
	return append(ready, cooling...)
}

func (f *FailoverProvider) markFailure(index int, err error) {
	f.mu.Lock()
	f.failureRuns[index]++
	run := f.failureRuns[index]
	cooldown := f.failureCooldown(err, run)
	f.failedUntil[index] = time.Now().Add(cooldown)
	f.mu.Unlock()
}

// failureCooldown is an adaptive circuit breaker. One transient failure gets
// the configured base pause; repeated failures back off progressively. Typed
// HTTP failures get class-specific treatment so auth/quota/capacity failures
// are not probed as aggressively as an ordinary network hiccup.
func (f *FailoverProvider) failureCooldown(err error, run int) time.Duration {
	base := f.cooldown
	if base <= 0 {
		base = 30 * time.Second
	}
	if run < 1 {
		run = 1
	}
	mult := 1 << min(run-1, 4) // 1x,2x,4x,8x,16x; bounded below.
	cooldown := time.Duration(mult) * base
	capAt := 5 * time.Minute

	var responseErr *HTTPResponseError
	if errors.As(err, &responseErr) {
		switch responseErr.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			// Credentials/permissions rarely heal within seconds. Avoid hammering
			// a known-bad account while still allowing recovery after a key fix.
			if cooldown < 5*time.Minute {
				cooldown = 5 * time.Minute
			}
			capAt = 30 * time.Minute
		case http.StatusTooManyRequests:
			// An explicit server quota window is authoritative and may be much
			// longer than our normal breaker cap (for example a daily reset).
			capAt = 24 * time.Hour
			if responseErr.HasRetryAfter && responseErr.RetryAfter > cooldown {
				cooldown = responseErr.RetryAfter
			}
		case http.StatusBadRequest, http.StatusNotFound:
			// Often a model/route mismatch. Retrying the same endpoint every few
			// seconds is wasted latency; let configured fallbacks carry traffic.
			if cooldown < 2*time.Minute {
				cooldown = 2 * time.Minute
			}
			capAt = 15 * time.Minute
		case http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			capAt = 10 * time.Minute
		}
	}
	if cooldown > capAt {
		cooldown = capAt
	}
	return cooldown
}

func (f *FailoverProvider) markActive(index int) {
	f.mu.Lock()
	f.active = index
	f.failedUntil[index] = time.Time{}
	f.failureRuns[index] = 0
	f.mu.Unlock()
}

func (f *FailoverProvider) ActiveLabel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active < len(f.labels) && f.labels[f.active] != "" {
		return f.labels[f.active]
	}
	return f.providers[f.active].Name()
}

// Unwrap exposes the currently active concrete provider for optional capability
// interfaces such as subscription usage. Metered children remain intact.
func (f *FailoverProvider) Unwrap() Provider {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.providers[f.active]
}

func (f *FailoverProvider) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	seq := f.order(time.Now())
	var stream <-chan Delta
	var err error
	var failures []string
	consumed := 0
	current := -1
	for ; consumed < len(seq); consumed++ {
		current = seq[consumed]
		stream, err = f.providers[current].Complete(ctx, msgs, tools)
		if err == nil {
			break
		}
		f.markFailure(current, err)
		failures = append(failures, f.describe(current, err))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("llm.Failover: all configured backends failed to start: %s", strings.Join(failures, "; "))
	}
	out := make(chan Delta, 32)
	go f.relay(ctx, out, stream, current, msgs, tools, seq[consumed+1:], failures)
	return out, nil
}

func (f *FailoverProvider) relay(ctx context.Context, out chan<- Delta, stream <-chan Delta, current int, msgs []Message, tools []ToolDef, remaining []int, failures []string) {
	defer close(out)
	emitted := false
	for {
		for delta := range stream {
			if delta.Err != nil && !emitted {
				// Cancellation is user intent, not backend failure. In
				// particular, never turn Stop into an unexpected cloud call.
				if ctx.Err() != nil {
					return
				}
				f.markFailure(current, delta.Err)
				failures = append(failures, f.describe(current, delta.Err))
				for len(remaining) > 0 {
					current = remaining[0]
					remaining = remaining[1:]
					next, err := f.providers[current].Complete(ctx, msgs, tools)
					if err == nil {
						stream = next
						goto nextProvider
					}
					f.markFailure(current, err)
					failures = append(failures, f.describe(current, err))
				}
				delta.Err = fmt.Errorf("llm.Failover: all configured backends failed before output: %s", strings.Join(failures, "; "))
				select {
				case out <- delta:
				case <-ctx.Done():
				}
				return
			}
			if delta.Content != "" || delta.Reasoning != "" || delta.ToolCall != nil {
				emitted = true
				f.markActive(current)
			}
			select {
			case out <- delta:
			case <-ctx.Done():
				return
			}
		}
		if !emitted {
			f.markActive(current)
		}
		return
	nextProvider:
	}
}

func (f *FailoverProvider) describe(index int, err error) string {
	label := f.providers[index].Name()
	if index < len(f.labels) && f.labels[index] != "" {
		label = f.labels[index]
	}
	return fmt.Sprintf("%s: %v", label, err)
}
