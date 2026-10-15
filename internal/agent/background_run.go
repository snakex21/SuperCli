package agent

import (
	"context"
	"sync"
)

// DelegatedWaitEvent marks host-side waiting, after the parent provider has
// finished its response. Model inactivity timers must pause only for this
// interval; caller cancellation and the workers' own limits remain active.
type DelegatedWaitEvent struct{ Waiting bool }

func (DelegatedWaitEvent) event() {}

// A run owns only the workers it started. A cancelled run's workers may finish
// and persist their reports, but cannot wake a later conversation or run.
type backgroundRunKey struct{}
type backgroundRun struct {
	mu        sync.Mutex
	active    int
	published uint64
	accepted  uint64
	changed   chan struct{}
}

func backgroundRunFrom(ctx context.Context) *backgroundRun {
	run, _ := ctx.Value(backgroundRunKey{}).(*backgroundRun)
	return run
}

func (r *backgroundRun) start() func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.changed == nil {
		r.changed = make(chan struct{})
	}
	r.active++
	r.mu.Unlock()
	return sync.OnceFunc(func() {
		r.mu.Lock()
		r.active--
		close(r.changed)
		r.changed = make(chan struct{})
		r.mu.Unlock()
	})
}

func (r *backgroundRun) revision() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.accepted
}

// Count reports only after the conversation owner has appended them to the
// provider history. Worker completion alone is not evidence of unseen context:
// its report may already have been used by the current model request.
func (r *backgroundRun) acceptReport() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.accepted++
	r.mu.Unlock()
}

func (r *backgroundRun) publishReport() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.published++
	r.mu.Unlock()
}

// One event-driven wait coalesces the outstanding reports. It never queries a
// worker, consumes a model call while waiting or races conversation mutation.
func (r *backgroundRun) wait(ctx context.Context, observed uint64, waiting func()) (bool, error) {
	if r == nil {
		return false, nil
	}
	announced := false
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		r.mu.Lock()
		active, published, changed := r.active, r.published, r.changed
		r.mu.Unlock()
		if active == 0 {
			return published > observed, nil
		}
		if !announced && waiting != nil {
			waiting()
			announced = true
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-changed:
		}
	}
}
