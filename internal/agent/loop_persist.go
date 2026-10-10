package agent

import (
	"context"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// persist calls the writer if one is configured. A failed write
// must not abort the run — but it is no longer swallowed silently:
// persistAppend (persist_health.go) keeps the first error sticky,
// counts failures, buffers the message for in-order retry on the
// next append, and surfaces a one-shot warning to the user.
// The result reports whether this exact message committed during this call;
// recovering an older buffered message alone does not make it true.
func (l *Loop) persist(ctx context.Context, msg llm.Message) bool {
	if l.writer == nil {
		return false
	}
	// session_persist accumulates across the step's AppendMessage
	// calls. It OVERLAPS other phases (persists happen inside the
	// step, some from worker goroutines), so statsEndStep keeps it
	// out of the next_turn_prepare remainder math.
	t := time.Now()
	written := l.persistAppend(ctx, msg.DormantImages())
	l.recordPhase(stats.PhaseSessionPersist, time.Since(t))
	return written
}

func (l *Loop) persistProjection(ctx context.Context) {
	l.persistProjectionView(ctx, nil)
}

// persistCompletedProjection reuses the view used to decide whether saving is
// needed. It is local to this completion boundary, never cached across turns.
func (l *Loop) persistCompletedProjection(ctx context.Context) {
	if _, ok := l.writer.(contextProjectionWriter); !ok {
		return
	}
	visible := l.VisibleMessages()
	projected := l.resolvedToolProviderView(visible)
	if len(projected) < len(visible) {
		l.persistProjectionView(ctx, projected)
	}
}

// A prepared view belongs to the current loop-goroutine boundary only. Dirty
// projection retries always rebuild from the latest history, including any new
// instructions or recovered appends, rather than retaining this temporary view.
func (l *Loop) persistProjectionView(ctx context.Context, visible []llm.Message) {
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

	if visible == nil {
		visible = l.resolvedToolProviderView(l.VisibleMessages())
	}
	copied := false
	for i, msg := range visible {
		if !msg.HasImage() {
			continue
		}
		if !copied {
			visible = append([]llm.Message(nil), visible...)
			copied = true
		}
		visible[i] = msg.DormantImages()
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

// retainInterruptedReply keeps text already delivered to the UI when a stream
// fails or is canceled. Tool calls from that incomplete response were never
// dispatched, so they must not create dangling tool-call history. Run's bounded
// shutdown flush writes this queued message before releasing the turn fence.
func (l *Loop) retainInterruptedReply(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	assistant := llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: text}}}
	_, plain := captureThinkingFromMessage(assistant)
	l.Messages = append(l.Messages, plain)
	if l.writer == nil {
		return
	}
	h := &l.persistHealth
	h.mu.Lock()
	h.enqueueLocked(assistant.DormantImages(), l.writer)
	h.mu.Unlock()
}
