package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"supercli/internal/llm"
)

// flakyWriter fails the first failAppends AppendMessage attempts,
// then succeeds and records messages in arrival order. UpdateUsage
// fails while failUsage is true.
type flakyWriter struct {
	mu             sync.Mutex
	failAppends    int
	failUsage      bool
	failProjection bool
	attempts       int
	messages       []llm.Message
	projections    [][]llm.Message
}

func (w *flakyWriter) AppendMessage(ctx context.Context, msg llm.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts++
	if w.attempts <= w.failAppends {
		return errors.New("disk full")
	}
	w.messages = append(w.messages, msg)
	return nil
}

func (w *flakyWriter) UpdateUsage(in, out int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failUsage {
		return errors.New("database is locked")
	}
	return nil
}

func (w *flakyWriter) SaveContextProjection(_ context.Context, msgs []llm.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failProjection {
		return errors.New("projection database is locked")
	}
	w.projections = append(w.projections, append([]llm.Message(nil), msgs...))
	return nil
}

func drainNotices(ch chan Event) []NoticeEvent {
	var out []NoticeEvent
	for {
		select {
		case ev := <-ch:
			if n, ok := ev.(NoticeEvent); ok {
				out = append(out, n)
			}
		default:
			return out
		}
	}
}

func newPersistTestLoop(t *testing.T, w SessionWriter) (*Loop, chan Event) {
	t.Helper()
	l, err := NewLoop(LoopConfig{
		Provider: makeScriptedProvider("ok"),
		Registry: emptyRegistry(),
		Writer:   w,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	sink := make(chan Event, 32)
	l.SetExternalSink(sink)
	return l, sink
}

func msgU(s string) llm.Message { return llm.Message{Role: llm.RoleUser, Content: s} }

// First failure warns exactly once; later failures only bump the
// counter and keep the FIRST error sticky.
func TestPersist_FirstFailureWarnsOnceAndSticks(t *testing.T) {
	w := &flakyWriter{failAppends: 1 << 30} // never recovers
	l, sink := newPersistTestLoop(t, w)

	l.persist(context.Background(), msgU("a"))
	l.persist(context.Background(), msgU("b"))
	l.persist(context.Background(), msgU("c"))

	notices := drainNotices(sink)
	if len(notices) != 1 {
		t.Fatalf("notices = %d, want exactly 1 (first failure only)", len(notices))
	}
	ps := l.PersistStatus()
	if ps.Failures != 3 {
		t.Errorf("Failures = %d, want 3", ps.Failures)
	}
	if ps.FirstOp != "append" || ps.FirstErr == "" || ps.FirstAt.IsZero() {
		t.Errorf("first error not sticky: %+v", ps)
	}
	if ps.LastWriteOK {
		t.Errorf("LastWriteOK = true, want false during outage")
	}
	if ps.Pending != 3 {
		t.Errorf("Pending = %d, want 3 (all failed appends buffered)", ps.Pending)
	}
}

// A successful write after an outage flushes the retry buffer in
// the original order (no hole in the on-disk history) and emits
// one recovery notice. A NEW outage afterwards warns again.
func TestPersist_RecoveryFlushesBufferInOrder(t *testing.T) {
	w := &flakyWriter{failAppends: 2}
	l, sink := newPersistTestLoop(t, w)

	l.persist(context.Background(), msgU("m1")) // attempt 1: fail
	l.persist(context.Background(), msgU("m2")) // attempt 2 (retry m1): fail
	l.persist(context.Background(), msgU("m3")) // retries m1, m2, then m3: ok

	got := make([]string, 0, 3)
	w.mu.Lock()
	for _, m := range w.messages {
		got = append(got, m.Content)
	}
	w.mu.Unlock()
	if len(got) != 3 || got[0] != "m1" || got[1] != "m2" || got[2] != "m3" {
		t.Fatalf("store order = %v, want [m1 m2 m3]", got)
	}

	notices := drainNotices(sink)
	if len(notices) != 2 {
		t.Fatalf("notices = %d, want 2 (one warning + one recovery)", len(notices))
	}

	ps := l.PersistStatus()
	if !ps.LastWriteOK || ps.Pending != 0 {
		t.Errorf("after recovery: LastWriteOK=%v Pending=%d, want true/0", ps.LastWriteOK, ps.Pending)
	}
	if ps.Failures != 2 {
		t.Errorf("Failures = %d, want 2 (sticky counter survives recovery)", ps.Failures)
	}

	// A new outage after recovery warns again (once).
	w.mu.Lock()
	w.failAppends = w.attempts + (1 << 30)
	w.mu.Unlock()
	l.persist(context.Background(), msgU("m4"))
	l.persist(context.Background(), msgU("m5"))
	if n := len(drainNotices(sink)); n != 1 {
		t.Errorf("notices after new outage = %d, want 1", n)
	}
}

// The retry buffer is bounded: overflow drops the oldest entries
// and counts them as lost.
func TestPersist_BufferOverflowDropsOldest(t *testing.T) {
	w := &flakyWriter{failAppends: 1 << 30}
	l, _ := newPersistTestLoop(t, w)
	for i := 0; i < persistPendingMax+5; i++ {
		l.persist(context.Background(), msgU("x"))
	}
	ps := l.PersistStatus()
	if ps.Pending != persistPendingMax {
		t.Errorf("Pending = %d, want cap %d", ps.Pending, persistPendingMax)
	}
	if ps.Dropped != 5 {
		t.Errorf("Dropped = %d, want 5", ps.Dropped)
	}
}

// A failing writer must never interrupt inference: the run still
// streams the assistant answer end to end.
func TestRun_FailingWriter_DoesNotAbortInference(t *testing.T) {
	w := &flakyWriter{failAppends: 1 << 30}
	l, _ := newPersistTestLoop(t, w)

	events, err := l.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var text string
	for ev := range events {
		switch e := ev.(type) {
		case MessageEvent:
			text = e.Text
		case ErrorEvent:
			t.Fatalf("unexpected ErrorEvent: %v", e.Err)
		}
	}
	if text != "ok" {
		t.Errorf("assistant text = %q, want %q", text, "ok")
	}
	if ps := l.PersistStatus(); ps.Failures == 0 {
		t.Errorf("Failures = 0, want > 0 (writes did fail)")
	}
}

// UpdateUsage failures feed the sticky tracker with their own op
// label but do not close/open an append outage.
func TestPersist_UsageFailureSticky(t *testing.T) {
	w := &flakyWriter{failUsage: true}
	l, sink := newPersistTestLoop(t, w)

	events, err := l.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range events {
	}
	ps := l.PersistStatus()
	if ps.Failures != 1 {
		t.Fatalf("Failures = %d, want 1", ps.Failures)
	}
	if ps.FirstOp != "update_usage" {
		t.Errorf("FirstOp = %q, want update_usage", ps.FirstOp)
	}
	if !ps.LastWriteOK {
		t.Errorf("LastWriteOK = false, want true (appends never failed)")
	}
	if n := len(drainNotices(sink)); n != 1 {
		t.Errorf("notices = %d, want 1", n)
	}
}

func TestProjectionDirtyWaitsForAppendRecoveryAndRebuildsCurrentView(t *testing.T) {
	w := &flakyWriter{failAppends: 1 << 30}
	l, _ := newPersistTestLoop(t, w)
	m1, m2 := msgU("first"), msgU("second")
	l.Messages = append(l.Messages, m1)
	l.persist(context.Background(), m1) // opens append outage
	l.persistProjection(context.Background())
	if ps := l.PersistStatus(); !ps.ProjectionDirty || ps.LastWriteOK {
		t.Fatalf("during outage status = %+v, want dirty/not-ok", ps)
	}
	w.mu.Lock()
	if len(w.projections) != 0 {
		t.Fatal("projection was written against an incomplete transcript")
	}
	w.failAppends = w.attempts // next retry succeeds
	w.mu.Unlock()
	l.Messages = append(l.Messages, m2)
	l.persist(context.Background(), m2) // flushes first + second
	if ps := l.PersistStatus(); !ps.ProjectionDirty || ps.LastWriteOK {
		t.Fatalf("before loop retry status = %+v, want dirty/not-ok", ps)
	}
	l.retryDirtyProjection(context.Background())
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.projections) != 1 || len(w.projections[0]) != 2 || w.projections[0][1].Content != "second" {
		t.Fatalf("projection = %+v, want rebuilt [first second]", w.projections)
	}
	if ps := l.PersistStatus(); ps.ProjectionDirty || !ps.LastWriteOK {
		t.Fatalf("after retry status = %+v, want clean/ok", ps)
	}
}

func TestProjectionWriteFailureRetriesAndReportsRecovery(t *testing.T) {
	w := &flakyWriter{failProjection: true}
	l, sink := newPersistTestLoop(t, w)
	l.Messages = append(l.Messages, msgU("visible"))
	l.persistProjection(context.Background())
	if ps := l.PersistStatus(); !ps.ProjectionDirty || ps.LastWriteOK || ps.LastOp != "context_projection" {
		t.Fatalf("failed projection status = %+v", ps)
	}
	w.mu.Lock()
	w.failProjection = false
	w.mu.Unlock()
	l.retryDirtyProjection(context.Background())
	if ps := l.PersistStatus(); ps.ProjectionDirty || !ps.LastWriteOK {
		t.Fatalf("recovered projection status = %+v", ps)
	}
	notices := drainNotices(sink)
	if len(notices) != 2 {
		t.Fatalf("notices = %d, want warning + recovery", len(notices))
	}
}
