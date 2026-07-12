package agent

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"supercli/internal/llm"
)

// persistPendingMax bounds the retry buffer for failed session
// appends. When the store stays broken (disk full, corrupted DB)
// the buffer stops growing at this size: the OLDEST buffered
// message is dropped and counted, so memory stays bounded and the
// most recent history has the best chance of surviving a late
// recovery.
const persistPendingMax = 64

// persistHealth tracks the reliability of session writes. Session
// persistence is best-effort by design (a failed write must never
// abort inference), but failures used to be swallowed silently:
// the user kept chatting and only discovered the missing history
// after a restart. This tracker keeps the FIRST error sticky (for
// /status and the one-shot warning), counts every failure, buffers
// failed appends for in-order retry, and reports recovery.
//
// All fields are guarded by mu; persist() is called from the run
// goroutine AND from worker goroutines (task notifications), and
// holding mu across the store write serializes appends so the
// retry queue drains in the original message order.
type persistHealth struct {
	mu sync.Mutex

	// failures counts every failed write attempt (append or
	// usage update), across the whole loop lifetime.
	failures int
	// first* freeze the very first failure: op ("append" or
	// "update_usage"), error text and time. Sticky — never
	// overwritten by later failures.
	firstOp  string
	firstErr string
	firstAt  time.Time
	// last* track the most recent failure.
	lastOp  string
	lastErr string
	lastAt  time.Time
	// outage is true while the most recent APPEND attempt
	// failed (usage-update failures do not open or close an
	// outage: buffered messages are what matters for history).
	outage bool
	// warned guards the one-shot UI warning per outage. Reset
	// on recovery so a NEW outage warns again.
	warned bool
	// pending holds messages whose append failed, in order.
	// They are retried before the next append so a late
	// recovery leaves no hole in the on-disk history.
	pending []llm.Message
	// dropped counts messages evicted from a full pending
	// buffer — real, unrecoverable history loss.
	dropped int
	// projectionDirty means a context projection could not be saved while
	// transcript appends were missing (or its own write failed). It stores no
	// stale snapshot: the run goroutine rebuilds the latest visible context
	// after append recovery so the projection boundary cannot skip messages.
	projectionDirty  bool
	projectionOutage bool
}

// PersistStatus is the /status- and doctor-facing snapshot of
// session-write health.
type PersistStatus struct {
	// Failures is the total number of failed write attempts.
	Failures int
	// FirstOp/FirstErr/FirstAt describe the first failure.
	FirstOp  string
	FirstErr string
	FirstAt  time.Time
	// LastOp/LastErr/LastAt describe the most recent failure.
	LastOp  string
	LastErr string
	LastAt  time.Time
	// LastWriteOK is false while the session store is in an
	// outage (the most recent append attempt failed).
	LastWriteOK bool
	// Pending is the number of messages buffered for retry.
	Pending int
	// Dropped is the number of messages lost to buffer overflow.
	Dropped int
	// ProjectionDirty reports that the durable model-context projection is
	// waiting for a safe retry with the current conversation view.
	ProjectionDirty bool
}

// PersistStatus returns a snapshot of session-write health. Safe
// from any goroutine.
func (l *Loop) PersistStatus() PersistStatus {
	h := &l.persistHealth
	h.mu.Lock()
	defer h.mu.Unlock()
	return PersistStatus{
		Failures:        h.failures,
		FirstOp:         h.firstOp,
		FirstErr:        h.firstErr,
		FirstAt:         h.firstAt,
		LastOp:          h.lastOp,
		LastErr:         h.lastErr,
		LastAt:          h.lastAt,
		LastWriteOK:     !h.outage && !h.projectionDirty && !h.projectionOutage,
		Pending:         len(h.pending),
		Dropped:         h.dropped,
		ProjectionDirty: h.projectionDirty,
	}
}

// noteFailureLocked records one failed write attempt. Returns the
// one-shot warning text when this failure should be surfaced to
// the user (first failure of an outage), "" otherwise. Caller
// holds h.mu.
func (h *persistHealth) noteFailureLocked(op string, err error) string {
	h.failures++
	now := time.Now()
	if h.firstErr == "" {
		h.firstOp, h.firstErr, h.firstAt = op, err.Error(), now
	}
	h.lastOp, h.lastErr, h.lastAt = op, err.Error(), now
	if h.warned {
		return ""
	}
	h.warned = true
	return fmt.Sprintf(
		"session persistence failed (%s): %v — the conversation continues; writes will be retried",
		op, err)
}

// enqueueLocked buffers a message whose append failed, evicting
// the oldest entry when the buffer is full. Caller holds h.mu.
func (h *persistHealth) enqueueLocked(msg llm.Message) {
	if len(h.pending) >= persistPendingMax {
		h.pending = h.pending[1:]
		h.dropped++
	}
	h.pending = append(h.pending, msg)
}

// persistAppend writes msg via the session writer, first flushing
// any messages buffered from earlier failed appends (in order, so
// a recovered store ends up with a hole-free history). On failure
// the message joins the retry buffer and the failure is recorded;
// inference is never interrupted. Caller guarantees l.writer != nil.
func (l *Loop) persistAppend(ctx context.Context, msg llm.Message) {
	h := &l.persistHealth
	h.mu.Lock()

	// Retry earlier failures first to preserve on-disk order.
	for len(h.pending) > 0 {
		if err := l.writer.AppendMessage(ctx, h.pending[0]); err != nil {
			// Still broken: the current message queues up
			// behind the backlog.
			h.outage = true
			warn := h.noteFailureLocked("append", err)
			h.enqueueLocked(msg)
			h.mu.Unlock()
			l.persistNotify(warn)
			return
		}
		h.pending = h.pending[1:]
	}

	if err := l.writer.AppendMessage(ctx, msg); err != nil {
		h.outage = true
		warn := h.noteFailureLocked("append", err)
		h.enqueueLocked(msg)
		h.mu.Unlock()
		l.persistNotify(warn)
		return
	}

	// Success. If we were in an outage, report recovery once.
	var recovered string
	if h.outage {
		h.outage = false
		h.warned = false
		recovered = fmt.Sprintf(
			"session persistence recovered after %d failed write(s)", h.failures)
		if h.dropped > 0 {
			recovered += fmt.Sprintf("; %d message(s) were lost to buffer overflow", h.dropped)
		}
	}
	h.mu.Unlock()
	l.persistNotify(recovered)
}

// persistUsageFailure records a failed UpdateUsage call. Usage is
// an additive counter, not history — there is nothing to buffer —
// so it only feeds the sticky error, the counter and the one-shot
// warning.
func (l *Loop) persistUsageFailure(err error) {
	h := &l.persistHealth
	h.mu.Lock()
	warn := h.noteFailureLocked("update_usage", err)
	h.mu.Unlock()
	l.persistNotify(warn)
}

// persistNotify surfaces a persistence warning/recovery line to
// the user without interrupting the run: via the external event
// sink when one is attached (TUI, webgui), otherwise one line on
// stderr (batch mode).
func (l *Loop) persistNotify(text string) {
	if text == "" {
		return
	}
	if !l.Emit(NoticeEvent{Text: text}) {
		fmt.Fprintf(os.Stderr, "[notice] %s\n", text)
	}
}
