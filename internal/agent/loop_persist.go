package agent

import (
	"context"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// persist calls the writer if one is configured. A failed write
// must not abort the run — but it is no longer swallowed silently:
// persistAppend (persist_health.go) keeps the first error sticky,
// counts failures, buffers the message for in-order retry on the
// next append, and surfaces a one-shot warning to the user.
func (l *Loop) persist(ctx context.Context, msg llm.Message) {
	if l.writer == nil {
		return
	}
	// session_persist accumulates across the step's AppendMessage
	// calls. It OVERLAPS other phases (persists happen inside the
	// step, some from worker goroutines), so statsEndStep keeps it
	// out of the next_turn_prepare remainder math.
	t := time.Now()
	l.persistAppend(ctx, msg.DormantImages())
	l.recordPhase(stats.PhaseSessionPersist, time.Since(t))
}

func (l *Loop) persistProjection(ctx context.Context) {
	w, ok := l.writer.(contextProjectionWriter)
	if !ok {
		return
	}
	h := &l.persistHealth
	h.mu.Lock()
	// Saving now would pair a projection snapshot with a transcript boundary
	// that is missing buffered messages. Delay and rebuild after recovery.
	if h.outage || len(h.pending) > 0 {
		h.projectionDirty = true
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	visible := l.resolvedToolProviderView(l.VisibleMessages())
	for i := range visible {
		visible[i] = visible[i].DormantImages()
	}
	// Base/system-prefix messages are rebuilt from current config when a
	// loop is resumed. Persist only the conversation body, otherwise Web
	// GUI would prepend a fresh system prompt to a stale duplicate.
	lead := 0
	for lead < len(visible) && visible[lead].Role == llm.RoleSystem {
		lead++
	}
	if err := w.SaveContextProjection(ctx, visible[lead:]); err != nil {
		h.mu.Lock()
		h.projectionDirty = true
		h.projectionOutage = true
		warn := h.noteFailureLocked("context_projection", err)
		h.mu.Unlock()
		l.persistNotify(warn)
		return
	}
	h.mu.Lock()
	recovered := h.projectionOutage
	h.projectionDirty = false
	h.projectionOutage = false
	if recovered && !h.outage {
		h.warned = false
	}
	h.mu.Unlock()
	if recovered {
		l.persistNotify("session context projection persistence recovered")
	}
}

// retryDirtyProjection must run on the loop goroutine: it rebuilds the latest
// visible context after append recovery. Persisting an old saved slice with a
// new MAX(seq) boundary could otherwise skip newer messages on resume.
func (l *Loop) retryDirtyProjection(ctx context.Context) {
	h := &l.persistHealth
	h.mu.Lock()
	ready := h.projectionDirty && !h.outage && len(h.pending) == 0
	h.mu.Unlock()
	if ready {
		l.persistProjection(ctx)
	}
}
