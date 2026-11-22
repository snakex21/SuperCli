package webgui

import (
	"sync"
	"testing"
	"time"
)

// sessionEventRecorder collects one conversation's events from both places
// they can appear since delegated work runs in the background (1.0.39): the
// chat run's own stream, and the sync hub's background_event frames that
// carry worker progress, reports and questions after the run has ended.
type sessionEventRecorder struct {
	eng *Engine

	mu     sync.Mutex
	sid    string
	events []wireEvent
	stops  []func()
}

func newSessionEventRecorder(t *testing.T, eng *Engine) *sessionEventRecorder {
	r := &sessionEventRecorder{eng: eng}
	t.Cleanup(func() {
		r.mu.Lock()
		stops := r.stops
		r.stops = nil
		r.mu.Unlock()
		for _, stop := range stops {
			stop()
		}
	})
	return r
}

// emit is the runStream callback. The first "session" event subscribes to
// that conversation's background frames, before any worker can start.
func (r *sessionEventRecorder) emit(ev wireEvent) {
	r.add(ev)
	if ev.Type == "session" && ev.SessionID != "" {
		r.watch(ev.SessionID)
	}
}

// emitWith returns a callback that records and then calls fn.
func (r *sessionEventRecorder) emitWith(fn func(wireEvent)) func(wireEvent) {
	return func(ev wireEvent) {
		r.emit(ev)
		if fn != nil {
			fn(ev)
		}
	}
}

func (r *sessionEventRecorder) watch(sid string) {
	r.mu.Lock()
	if r.sid == sid {
		r.mu.Unlock()
		return
	}
	r.sid = sid
	r.mu.Unlock()
	sub, _, stop := r.eng.syncHub.subscribe(sid, "test-recorder")
	r.mu.Lock()
	r.stops = append(r.stops, stop)
	r.mu.Unlock()
	go func() {
		for frame := range sub.events {
			if frame.Type == "background_event" && frame.Event != nil {
				r.add(*frame.Event)
			}
		}
	}()
}

func (r *sessionEventRecorder) add(ev wireEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *sessionEventRecorder) snapshot() []wireEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wireEvent(nil), r.events...)
}

func (r *sessionEventRecorder) session() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sid
}

// waitFor polls until ok accepts the recorded events, or fails the test.
func (r *sessionEventRecorder) waitFor(t *testing.T, within time.Duration, what string, ok func([]wireEvent) bool) []wireEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		events := r.snapshot()
		if ok(events) {
			return events
		}
		if time.Now().After(deadline) {
			var kinds []string
			for _, ev := range events {
				kinds = append(kinds, ev.Type+":"+ev.ID+":"+ev.Status+":"+ev.Name)
			}
			t.Fatalf("timed out waiting for %s; %d events recorded: %v", what, len(events), kinds)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
