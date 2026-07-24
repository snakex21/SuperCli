// Idle scheduling for background memory work. Foreground beats
// background: the incremental memory summary (an extra model call)
// used to fire immediately after every agent turn, racing the
// user's next question for the same local backend (worse TTFT,
// KV-prefix churn). The idleScheduler defers that work until the
// user has been quiet for a fixed grace period, and a new prompt
// cancels whatever background call is mid-flight — nothing is
// lost, the uncovered conversation tail is simply retried at the
// next idle window.
package app

import (
	"context"
	"sync"
	"time"
)

// memoryIdleDelay is how long the user must be idle before the
// background memory work (incremental summary, startup raw-log
// summarization) is allowed to touch the model. A constant on
// purpose — not a config knob.
const memoryIdleDelay = 15 * time.Second

// idleScheduler runs a single job function after a quiet period.
//
//	Schedule() — (re)arms the idle timer (call after each turn).
//	Activity() — user is active: stop the timer and cancel any
//	             in-flight job. Pending work stays pending; the
//	             next Schedule() re-arms.
//	Close()    — permanent Activity(); used on the exit path so
//	             the finalizer never races an in-flight job.
//
// The job receives a context that is canceled by Activity/Close;
// it must treat cancellation as "retry later" (the memory saver
// already does — a failed summary leaves the fragment uncovered).
type idleScheduler struct {
	mu     sync.Mutex
	delay  time.Duration
	job    func(ctx context.Context)
	timer  *time.Timer
	cancel context.CancelFunc // non-nil while a job is in flight
	gen    int                // job generation; guards cancel ownership
	closed bool
}

func newIdleScheduler(delay time.Duration, job func(ctx context.Context)) *idleScheduler {
	return &idleScheduler{delay: delay, job: job}
}

// Schedule (re)arms the idle timer. Coalescing is free: several
// turns finishing inside one delay window produce ONE job run
// covering all of them.
func (s *idleScheduler) Schedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.delay, s.fire)
}

// Activity marks the user as active: the pending timer is stopped
// and any in-flight job is canceled. Call it the moment a new
// prompt is submitted, BEFORE the foreground model call starts.
func (s *idleScheduler) Activity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// Close cancels everything and refuses future schedules. Used on
// the exit path so finalizeMemorySession never waits behind an
// in-flight background summary.
func (s *idleScheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// fire runs on the timer goroutine when the idle delay elapses.
func (s *idleScheduler) fire() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.gen++
	myGen := s.gen
	s.mu.Unlock()

	s.job(ctx)

	s.mu.Lock()
	// Only clear the cancel if it is still OURS: a canceled job
	// that overlaps the next fire() must not cancel the new job.
	if s.gen == myGen && s.cancel != nil {
		s.cancel = nil
	}
	s.mu.Unlock()
	cancel() // release this job's context resources
}
