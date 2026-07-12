package llm

import (
	"context"
	"sync"
	"time"
)

// CallStat is one measured model invocation (one Provider.Complete
// call, successful or not). It is the raw record the stats layer
// aggregates per purpose — main step calls and every helper
// inference (navigator, compact, reflection, draft, memory, ...)
// flow through the same funnel so no model time can hide inside a
// CLI phase.
type CallStat struct {
	// Purpose labels why the call was made ("main", "navigator",
	// "compact", ...). Default comes from the Metered wrapper;
	// call sites override it via WithPurpose on the context.
	Purpose string
	// Provider is a human label for the transport (config
	// provider type or name). Model is the wrapped provider's
	// Name() at call time.
	Provider string
	Model    string
	// Background marks calls made outside the user's foreground
	// turn (memory autosave, startup raw-memory summarization).
	// Set via WithBackground on the context.
	Background bool
	// Canceled reports that the context was canceled before the
	// stream finished.
	Canceled bool
	// Failed reports that the call could not even start or the
	// stream delivered an error delta.
	Failed bool
	// TTFT is the time from Complete() to the first delta.
	// Zero when no delta ever arrived.
	TTFT time.Duration
	// Duration is Complete() to stream close (or error).
	Duration time.Duration
	// TokensIn/TokensOut are the provider-reported usage for the
	// call (0 when the backend sent no usage frame).
	TokensIn  int
	TokensOut int
	StartedAt time.Time
}

// CallSink receives one CallStat per Complete call. It must be
// safe for concurrent use (background calls report from their own
// goroutines).
type CallSink func(CallStat)

type purposeCtxKey struct{}
type backgroundCtxKey struct{}

// Common purpose labels. Plain strings on purpose (call sites in
// other packages may define their own).
const (
	PurposeMain      = "main"
	PurposeNavigator = "navigator"
	PurposeCompact   = "compact"
	PurposeReflect   = "reflection"
	PurposeDraft     = "draft"
	PurposeVerdict   = "verdict"
	PurposeMemory    = "memory"
	PurposeGoal      = "goal"
	PurposeConsult   = "consult"
	PurposeJudge     = "judge"
	PurposeDarwin    = "darwin_judge"
	PurposeProbe     = "probe"
	PurposeTitle     = "title"
	PurposeTask      = "task"
)

// WithPurpose labels every model call made under ctx. The label
// wins over the Metered wrapper's default, so a shared provider
// (main/draft) still attributes helper calls correctly.
func WithPurpose(ctx context.Context, purpose string) context.Context {
	if purpose == "" {
		return ctx
	}
	return context.WithValue(ctx, purposeCtxKey{}, purpose)
}

// PurposeFromContext returns the purpose label set by WithPurpose,
// or "" when unset.
func PurposeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	p, _ := ctx.Value(purposeCtxKey{}).(string)
	return p
}

// WithBackground marks calls under ctx as background work (not
// part of the user's foreground turn).
func WithBackground(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundCtxKey{}, true)
}

// IsBackground reports whether ctx carries the background mark.
func IsBackground(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	b, _ := ctx.Value(backgroundCtxKey{}).(bool)
	return b
}

// backgroundGate serializes background model calls process-wide:
// at most ONE background inference (memory autosave, startup
// raw-memory summarization, webgui title) runs at a time, so
// helper work never piles multiple requests onto a single local
// backend. Foreground calls never touch the gate — the user's
// turn is never queued behind background work. The gate is held
// from Complete() until the stream closes (the backend is busy
// for the whole stream, not just the request).
var backgroundGate = make(chan struct{}, 1)

// acquireBackgroundGate blocks until the gate is free or ctx is
// done. Returns a release func (no-op on failure) and ctx.Err().
func acquireBackgroundGate(ctx context.Context) (func(), error) {
	select {
	case backgroundGate <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-backgroundGate }) }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}

// metered decorates a Provider and reports one CallStat per
// Complete call to the sink. The stat is emitted BEFORE the output
// channel closes, so a consumer that drains the stream observes
// the record as soon as its range loop ends.
type metered struct {
	inner    Provider
	provider string // transport label for CallStat.Provider
	purpose  string // default purpose when the ctx carries none
	sink     CallSink
}

// Metered wraps inner so every Complete call is measured and
// reported to sink with the given default purpose. providerLabel
// is a human transport label (config provider type/name). A nil
// inner or sink returns inner unchanged — metering is never a
// hard dependency.
func Metered(inner Provider, providerLabel, purpose string, sink CallSink) Provider {
	if inner == nil || sink == nil {
		return inner
	}
	return &metered{inner: inner, provider: providerLabel, purpose: purpose, sink: sink}
}

// Unwrap exposes the wrapped provider so capability type
// assertions (RouterProvider, Codex usage fetchers, ...) keep
// working on a metered provider.
func (m *metered) Unwrap() Provider { return m.inner }

// Unwrap peels every decorator implementing Unwrap() Provider and
// returns the innermost provider. Safe on nil and non-wrapped
// providers.
func Unwrap(p Provider) Provider {
	for p != nil {
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return p
		}
		inner := u.Unwrap()
		if inner == nil {
			return p
		}
		p = inner
	}
	return p
}

func (m *metered) Name() string { return m.inner.Name() }

func (m *metered) Complete(ctx context.Context, msgs []Message, tools []ToolDef) (<-chan Delta, error) {
	stat := CallStat{
		Purpose:    m.purpose,
		Provider:   m.provider,
		Model:      m.inner.Name(),
		Background: IsBackground(ctx),
		StartedAt:  time.Now().UTC(),
	}
	if p := PurposeFromContext(ctx); p != "" {
		stat.Purpose = p
	}
	start := time.Now()
	// Background calls are serialized process-wide (see
	// backgroundGate): only one helper inference at a time, and
	// canceling ctx (user sent a new prompt) abandons the wait.
	release := func() {}
	if stat.Background {
		var err error
		release, err = acquireBackgroundGate(ctx)
		if err != nil {
			stat.Duration = time.Since(start)
			stat.Failed = true
			stat.Canceled = true
			m.sink(stat)
			return nil, err
		}
	}
	in, err := m.inner.Complete(ctx, msgs, tools)
	if err != nil {
		release()
		stat.Duration = time.Since(start)
		stat.Failed = true
		stat.Canceled = ctx != nil && ctx.Err() != nil
		m.sink(stat)
		return nil, err
	}
	out := make(chan Delta)
	go func() {
		// LIFO defers: the gate is released LAST — after the stat
		// is recorded and close(out) unblocks the consumer.
		defer release()
		defer close(out)
		defer func() {
			stat.Duration = time.Since(start)
			if ctx != nil && ctx.Err() != nil {
				stat.Canceled = true
			}
			m.sink(stat)
		}()
		gotFirst := false
		for d := range in {
			if !gotFirst {
				stat.TTFT = time.Since(start)
				gotFirst = true
			}
			if d.Usage != nil {
				stat.TokensIn = d.Usage.Input
				stat.TokensOut = d.Usage.Output
			}
			if d.Err != nil {
				stat.Failed = true
			}
			select {
			case out <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
