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
	// TokensCached is the cached-prompt portion of TokensIn and
	// TokensReasoning the hidden chain-of-thought portion of
	// TokensOut, both provider-reported (0 when not reported).
	TokensCached    int
	TokensReasoning int
	// PrefillEvaluated is the uncached prompt work (TokensIn -
	// TokensCached). PrefillTokensPerSecond derives throughput from that work
	// and TTFT. PrefillBudget/Source describe the context-defense threshold
	// active when the request was sent.
	PrefillEvaluated       int
	PrefillTokensPerSecond float64
	PrefillBudget          int
	PrefillBudgetSource    string
	// Request is a request-shape estimate (tokens by role),
	// computed locally from the outgoing messages/tools so sinks
	// can attribute context weight without seeing the prompt.
	Request   RequestBreakdown
	StartedAt time.Time
}

// CallSink receives one CallStat per Complete call. It must be
// safe for concurrent use (background calls report from their own
// goroutines).
type CallSink func(CallStat)

// MultiSink fans one CallStat out to every non-nil sink. Nil when
// no usable sink remains, so Metered's "nil sink disables metering"
// contract keeps working for pure pass-through configurations.
func MultiSink(sinks ...CallSink) CallSink {
	nz := make([]CallSink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			nz = append(nz, s)
		}
	}
	switch len(nz) {
	case 0:
		return nil
	case 1:
		return nz[0]
	default:
		return func(stat CallStat) {
			for _, s := range nz {
				s(stat)
			}
		}
	}
}

type purposeCtxKey struct{}
type backgroundCtxKey struct{}
type callSinkCtxKey struct{}
type prefillBudgetCtxKey struct{}

type prefillBudgetContext struct {
	Tokens int
	Source string
}

// WithPrefillBudget attaches non-sensitive context-defense telemetry to a
// request. Providers ignore it; Metered copies it into CallStat.
func WithPrefillBudget(ctx context.Context, tokens int, source string) context.Context {
	if ctx == nil || tokens <= 0 {
		return ctx
	}
	return context.WithValue(ctx, prefillBudgetCtxKey{}, prefillBudgetContext{Tokens: tokens, Source: source})
}

// WithCallSink attaches an ADDITIONAL per-request sink to ctx: every
// metered Complete call under ctx reports to the wrapper's own sink
// AND to sink. Front-ends that account per session (the web GUI's
// metered-usage rows) attach their recorder here instead of stacking
// a second metering wrapper on the same provider.
func WithCallSink(ctx context.Context, sink CallSink) context.Context {
	if sink == nil {
		return ctx
	}
	prev := callSinksFromContext(ctx)
	sinks := make([]CallSink, 0, len(prev)+1)
	sinks = append(sinks, prev...)
	sinks = append(sinks, sink)
	return context.WithValue(ctx, callSinkCtxKey{}, sinks)
}

func callSinksFromContext(ctx context.Context) []CallSink {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(callSinkCtxKey{}).([]CallSink)
	return s
}

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

// Foreground/background priority. The gate above only guarantees "at
// most one background inference at a time"; by itself it neither stops
// an already-streaming helper nor prevents a new helper from starting
// while a foreground stream is active. The coordinator below provides
// both halves of the contract:
//
//   - foreground calls never wait for one another;
//   - the first foreground call cancels every running/gate-waiting
//     background call, and background stays blocked until ALL active
//     foreground streams finish;
//   - a background call that wins the idle race registers its cancel
//     before leaving the lock, so a foreground call starting one
//     instruction later still preempts it.
//
// Background jobs treat cancellation as "retry later" (memory keeps
// its backlog and web titles retain their deterministic fallback), so
// the priority switch is loss-free.
var (
	callPriorityMu  sync.Mutex
	foregroundCalls int
	foregroundIdle  = closedSignal()
	bgCallSeq       uint64
	bgCalls         = map[uint64]context.CancelFunc{}
)

func closedSignal() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// beginForeground marks one foreground stream active and preempts all
// helper calls that registered before it. The returned function must
// be held until the stream closes, not merely until Complete returns:
// the backend remains occupied for the whole stream lifetime.
func beginForeground() func() {
	callPriorityMu.Lock()
	if foregroundCalls == 0 {
		foregroundIdle = make(chan struct{})
	}
	foregroundCalls++
	cancels := make([]context.CancelFunc, 0, len(bgCalls))
	for id, cancel := range bgCalls {
		cancels = append(cancels, cancel)
		delete(bgCalls, id)
	}
	callPriorityMu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			callPriorityMu.Lock()
			foregroundCalls--
			if foregroundCalls == 0 {
				close(foregroundIdle)
			}
			callPriorityMu.Unlock()
		})
	}
}

// registerBackgroundWhenIdle waits until no foreground stream is
// active, then atomically publishes the helper's cancel function.
// Publishing under the same lock that beginForeground uses closes the
// start-vs-preempt race: either background registers first and gets
// canceled, or foreground registers first and background waits.
func registerBackgroundWhenIdle(ctx context.Context, cancel context.CancelFunc) (uint64, error) {
	for {
		callPriorityMu.Lock()
		if foregroundCalls == 0 {
			bgCallSeq++
			id := bgCallSeq
			bgCalls[id] = cancel
			callPriorityMu.Unlock()
			return id, nil
		}
		idle := foregroundIdle
		callPriorityMu.Unlock()

		select {
		case <-idle:
			// Re-check under the lock: another foreground call may
			// have started between the close and this goroutine waking.
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func unregisterBackgroundCall(id uint64) {
	callPriorityMu.Lock()
	delete(bgCalls, id)
	callPriorityMu.Unlock()
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

// meteredDeltaBuffer absorbs short provider bursts without forcing a
// goroutine hand-off for every token-sized delta. It is deliberately
// small and fixed: enough to make metering cheap, never enough to turn
// streaming into response buffering or grow with model output.
const meteredDeltaBuffer = 32

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

// IsMetered reports whether p is (already) a Metered wrapper. The
// provider factory uses it to guarantee exactly ONE metering layer
// per provider — a nested wrapper would double-report every call and
// deadlock the background gate (one goroutine acquiring it twice).
func IsMetered(p Provider) bool {
	_, ok := p.(*metered)
	return ok
}

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
		Request:    EstimateRequestBreakdown(msgs, tools),
		StartedAt:  time.Now().UTC(),
	}
	if p := PurposeFromContext(ctx); p != "" {
		stat.Purpose = p
	}
	if ctx != nil {
		if budget, ok := ctx.Value(prefillBudgetCtxKey{}).(prefillBudgetContext); ok {
			stat.PrefillBudget = budget.Tokens
			stat.PrefillBudgetSource = budget.Source
		}
	}
	// The wrapper's own sink plus any per-request sinks attached via
	// WithCallSink (e.g. the web GUI's per-session usage recorder).
	ctxSinks := callSinksFromContext(ctx)
	emit := func() {
		input := stat.TokensIn
		if input <= 0 {
			input = stat.Request.System + stat.Request.User + stat.Request.Assistant + stat.Request.Tool + stat.Request.Other
		}
		stat.PrefillEvaluated = input - stat.TokensCached
		if stat.PrefillEvaluated < 0 {
			stat.PrefillEvaluated = 0
		}
		if stat.TTFT > 0 {
			stat.PrefillTokensPerSecond = float64(stat.PrefillEvaluated) / stat.TTFT.Seconds()
		}
		m.sink(stat)
		for _, s := range ctxSinks {
			s(stat)
		}
	}
	start := time.Now()
	// Background calls are serialized process-wide (see
	// backgroundGate): only one helper inference at a time, and
	// canceling ctx (user sent a new prompt) abandons the wait.
	// They are also PREEMPTABLE: the call runs under a derived
	// cancelable context registered in bgCalls, and any foreground
	// call cancels it — mid-stream or still waiting on the gate.
	release := func() {}
	cleanup := func() {}
	finishForeground := func() {}
	if stat.Background {
		bgCtx, bgCancel := context.WithCancel(ctx)
		ctx = bgCtx
		id, waitErr := registerBackgroundWhenIdle(ctx, bgCancel)
		if waitErr != nil {
			bgCancel()
			stat.Duration = time.Since(start)
			stat.Failed = true
			stat.Canceled = true
			emit()
			return nil, waitErr
		}
		cleanup = func() {
			unregisterBackgroundCall(id)
			bgCancel()
		}
		var err error
		release, err = acquireBackgroundGate(ctx)
		if err != nil {
			cleanup()
			stat.Duration = time.Since(start)
			stat.Failed = true
			stat.Canceled = true
			emit()
			return nil, err
		}
	} else {
		// Keep the foreground marker for the WHOLE stream. This is
		// what prevents a helper scheduled a millisecond later from
		// entering the backend while the user response is generating.
		finishForeground = beginForeground()
	}
	in, err := m.inner.Complete(ctx, msgs, tools)
	if err != nil {
		release()
		cleanup()
		finishForeground()
		stat.Duration = time.Since(start)
		stat.Failed = true
		stat.Canceled = ctx != nil && ctx.Err() != nil
		emit()
		return nil, err
	}
	out := make(chan Delta, meteredDeltaBuffer)
	go func() {
		// LIFO defers: the gate is released and the preemption
		// registration dropped LAST — after the stat is recorded and
		// close(out) unblocks the consumer.
		defer cleanup()
		defer release()
		defer finishForeground()
		defer close(out)
		defer func() {
			stat.Duration = time.Since(start)
			if ctx != nil && ctx.Err() != nil {
				stat.Canceled = true
			}
			emit()
		}()
		gotFirst := false
		for d := range in {
			if !gotFirst && d.Notice == "" {
				stat.TTFT = time.Since(start)
				gotFirst = true
			}
			if d.Usage != nil {
				stat.TokensIn = d.Usage.Input
				stat.TokensOut = d.Usage.Output
				stat.TokensCached = d.Usage.CachedInput
				stat.TokensReasoning = d.Usage.Reasoning
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
