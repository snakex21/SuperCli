package agent

import (
	"context"
	"reflect"
	"slices"
	"time"

	"supercli/internal/llm"
)

// speculativeCompactFrac starts summarizing the completed-turn prefix in the
// background once the estimate reaches this share of the compaction threshold.
const speculativeCompactFrac = 0.8

// speculativeCompact is one background summary of a frozen prefix copy.
// Fields other than summary/err are immutable; those are read after done.
type speculativeCompact struct {
	epoch    uint64
	provider llm.Provider
	prefix   []llm.Message
	tools    []string
	cancel   context.CancelFunc
	done     chan struct{}
	summary  string
	err      error
}

// Background summaries share the active provider with the main request.
// Local single-slot backends (taskParallel=false) would only thrash the KV
// cache, so speculation follows the same concurrency policy as task batches.
func (l *Loop) speculativeCompactEnabled() bool {
	return l.summarizer != nil && l.taskParallel
}

// maybeStartSpeculativeCompact runs below the threshold. It starts at most one
// background summary per prefix and only when the synchronous path would
// summarize the same prefix (same split and freshness gates).
func (l *Loop) maybeStartSpeculativeCompact(ctx context.Context) {
	if !l.speculativeCompactEnabled() || ctx.Err() != nil {
		return
	}
	history := l.compactionHistory()
	all := history.messages
	split := history.autoCompactSplit()
	if split <= leadingSystemCount(all) {
		return
	}
	prefix := all[:split]
	if len(prefix) <= leadingSystemCount(prefix) || !hasFreshCompactablePrefix(prefix, len(prefix)) {
		return
	}
	tools := l.compactActiveTools()
	l.speculativeMu.Lock()
	job := l.speculative
	l.speculativeMu.Unlock()
	if job != nil {
		if l.speculativeMatches(job, prefix, tools) {
			return // running or finished (even failed): never repeat for this prefix
		}
		l.stopSpeculativeCompact() // stale: discard before starting a new one
	}
	jobCtx, cancel := context.WithCancel(ctx)
	job = &speculativeCompact{
		epoch: l.conversationEpoch.Load(), provider: l.provider,
		prefix: cloneCompactPrefix(prefix), tools: tools,
		cancel: cancel, done: make(chan struct{}),
	}
	var scope compactToolScope
	if l.registry != nil {
		frozen := job.tools
		scope.activeTools = func() []string { return frozen }
	}
	summarize := l.summarizer
	l.speculativeMu.Lock()
	l.speculative = job
	l.speculativeMu.Unlock()
	go func() {
		defer close(job.done)
		callCtx := context.WithValue(llm.WithPurpose(jobCtx, llm.PurposeCompact), compactToolScopeKey{}, scope)
		job.summary, job.err = summarize(callCtx, job.provider, job.prefix)
	}()
}

// takeSpeculativeCompact consumes a background summary for prefix. ok=false
// means none matched (stale or failed); the caller runs the synchronous path.
// A matching job still in flight is awaited instead of starting a new call.
func (l *Loop) takeSpeculativeCompact(ctx context.Context, prefix []llm.Message) (summary string, err error, ok bool) {
	l.speculativeMu.Lock()
	job := l.speculative
	l.speculative = nil
	l.speculativeMu.Unlock()
	if job == nil {
		return "", nil, false
	}
	if !l.speculativeMatches(job, prefix, l.compactActiveTools()) {
		job.cancel()
		<-job.done
		return "", nil, false
	}
	start := time.Now()
	select {
	case <-job.done:
	case <-ctx.Done():
		job.cancel()
		<-job.done
		l.recordAuxWall(llm.PurposeCompact, time.Since(start))
		return "", ctx.Err(), true
	}
	job.cancel()
	l.recordAuxWall(llm.PurposeCompact, time.Since(start))
	if job.err != nil {
		return "", nil, false
	}
	return job.summary, nil, true
}

// stopSpeculativeCompact cancels and joins any background summary, so no two
// summaries ever run at once and none outlives its Run.
func (l *Loop) stopSpeculativeCompact() {
	l.speculativeMu.Lock()
	job := l.speculative
	l.speculative = nil
	l.speculativeMu.Unlock()
	if job != nil {
		job.cancel()
		<-job.done
	}
}

func (l *Loop) compactActiveTools() []string {
	if l.registry == nil {
		return nil
	}
	return l.registry.ActiveNames()
}

func (l *Loop) speculativeMatches(job *speculativeCompact, prefix []llm.Message, tools []string) bool {
	return job.epoch == l.conversationEpoch.Load() && job.provider == l.provider &&
		slices.Equal(job.tools, tools) && sameCompactPrefix(job.prefix, prefix)
}

// cloneCompactPrefix copies the slices a later in-place edit could share.
func cloneCompactPrefix(prefix []llm.Message) []llm.Message {
	out := make([]llm.Message, len(prefix))
	for i, m := range prefix {
		m.Parts = slices.Clone(m.Parts)
		m.ToolCalls = slices.Clone(m.ToolCalls)
		out[i] = m
	}
	return out
}

func sameCompactPrefix(a, b []llm.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Role != y.Role || x.Content != y.Content || x.Name != y.Name || x.ToolCallID != y.ToolCallID ||
			!reflect.DeepEqual(x.Parts, y.Parts) || !slices.Equal(x.ToolCalls, y.ToolCalls) {
			return false
		}
	}
	return true
}
