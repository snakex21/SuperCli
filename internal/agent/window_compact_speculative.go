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

// maybeStartSpeculativeCompact starts at most one background summary. Keep an
// existing job even as history grows or is pruned: valid shorter prefixes remain
// useful, and invalid ones are joined/discarded only when compaction is needed.
// This avoids paying for a new summary on every small history change.
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
	if len(prefix) <= leadingSystemCount(prefix) || !hasFreshCompactablePrefix(prefix, len(prefix)) ||
		l.autoCompactRejected(prefix) {
		return
	}
	tools := l.compactActiveTools()
	l.speculativeMu.Lock()
	job := l.speculative
	if job != nil {
		select {
		case <-job.done:
			if l.speculativeMatches(job, prefix, tools) {
				l.speculativeMu.Unlock()
				return // consume this result; never pay twice for the same prefix
			}
			// A completed obsolete job cannot become valid again by growing
			// history. Retaining it would block all speculation until overflow.
			l.speculative = nil
			job.cancel()
		default:
			// Do not cancel/join running work on each small history change.
			l.speculativeMu.Unlock()
			return
		}
	}
	l.speculativeMu.Unlock()
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

// takeSpeculativeCompact consumes a summary covering prefix[:covered]. The
// remaining messages MUST stay verbatim. Nonblocking callers leave an unfinished
// (or stale) job alone; hard/manual callers join it before any replacement call.
func (l *Loop) takeSpeculativeCompact(ctx context.Context, prefix []llm.Message, wait bool) (summary string, err error, covered int, ok bool) {
	l.speculativeMu.Lock()
	job := l.speculative
	if job == nil {
		l.speculativeMu.Unlock()
		return "", nil, 0, false
	}
	matches := l.speculativeMatches(job, prefix, l.compactActiveTools())
	if !wait {
		select {
		case <-job.done:
			if !matches {
				l.speculative = nil
				l.speculativeMu.Unlock()
				job.cancel()
				return "", nil, 0, false
			}
		default:
			l.speculativeMu.Unlock()
			return "", nil, 0, false
		}
	}
	l.speculative = nil
	l.speculativeMu.Unlock()
	if !matches {
		job.cancel()
		<-job.done
		return "", nil, 0, false
	}
	start := time.Now()
	select {
	case <-job.done:
	case <-ctx.Done():
		job.cancel()
		<-job.done
		l.recordAuxWall(llm.PurposeCompact, time.Since(start))
		return "", ctx.Err(), len(job.prefix), true
	}
	job.cancel()
	l.recordAuxWall(llm.PurposeCompact, time.Since(start))
	// A failed helper must not be paid for twice by an immediate retry.
	return job.summary, job.err, len(job.prefix), true
}

// Readiness is checked only at the ordinary pre-request boundary. Starting
// speculation needs pressure; consuming an existing finished result does not.
func (l *Loop) speculativeCompactReady() bool {
	l.speculativeMu.Lock()
	defer l.speculativeMu.Unlock()
	if l.speculative == nil {
		return false
	}
	select {
	case <-l.speculative.done:
		return true
	default:
		return false
	}
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

// rejectAutoCompact remembers an older prefix whose summary did not reduce it,
// so threshold compaction does not pay for the same prefix on every step.
func (l *Loop) rejectAutoCompact(prefix []llm.Message) {
	l.speculativeMu.Lock()
	l.rejectedCompact = cloneCompactPrefix(prefix)
	l.rejectedCompactEpoch = l.conversationEpoch.Load()
	l.speculativeMu.Unlock()
}

func (l *Loop) autoCompactRejected(prefix []llm.Message) bool {
	l.speculativeMu.Lock()
	defer l.speculativeMu.Unlock()
	return l.rejectedCompact != nil && l.rejectedCompactEpoch == l.conversationEpoch.Load() &&
		sameCompactPrefix(l.rejectedCompact, prefix)
}

func (l *Loop) compactActiveTools() []string {
	if l.registry == nil {
		return nil
	}
	return l.registry.ActiveNames()
}

func (l *Loop) speculativeMatches(job *speculativeCompact, prefix []llm.Message, tools []string) bool {
	return job.epoch == l.conversationEpoch.Load() && job.provider == l.provider &&
		slices.Equal(job.tools, tools) && len(job.prefix) <= len(prefix) &&
		sameCompactPrefix(job.prefix, prefix[:len(job.prefix)])
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
