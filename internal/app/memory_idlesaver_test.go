package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond up to 2s. Keeps the tests fast on the happy
// path without flaking on slow CI clocks.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestIdleScheduler_NotImmediate is the contract of the whole
// change: scheduling background memory work must NOT run it right
// away — only after the idle delay elapses.
func TestIdleScheduler_NotImmediate(t *testing.T) {
	var runs atomic.Int32
	s := newIdleScheduler(60*time.Millisecond, func(ctx context.Context) {
		runs.Add(1)
	})
	s.Schedule()
	time.Sleep(20 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("job ran %d time(s) before the idle delay", got)
	}
	waitFor(t, func() bool { return runs.Load() == 1 }, "job never ran after the idle delay")
}

// TestIdleScheduler_CoalescesSchedules: several turns finishing
// within one idle window produce ONE job run (the memory saver
// then summarizes the whole backlog in a single call).
func TestIdleScheduler_CoalescesSchedules(t *testing.T) {
	var runs atomic.Int32
	s := newIdleScheduler(50*time.Millisecond, func(ctx context.Context) {
		runs.Add(1)
	})
	s.Schedule()
	time.Sleep(20 * time.Millisecond)
	s.Schedule() // second turn ends inside the window → timer resets
	time.Sleep(20 * time.Millisecond)
	s.Schedule()
	waitFor(t, func() bool { return runs.Load() >= 1 }, "job never ran")
	time.Sleep(100 * time.Millisecond) // no extra timer may be pending
	if got := runs.Load(); got != 1 {
		t.Fatalf("job ran %d time(s), want exactly 1 (coalesced)", got)
	}
}

// TestIdleScheduler_ActivityStopsPendingTimer: user activity
// before the timer fires postpones the work entirely.
func TestIdleScheduler_ActivityStopsPendingTimer(t *testing.T) {
	var runs atomic.Int32
	s := newIdleScheduler(40*time.Millisecond, func(ctx context.Context) {
		runs.Add(1)
	})
	s.Schedule()
	time.Sleep(10 * time.Millisecond)
	s.Activity()
	time.Sleep(100 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("job ran %d time(s) despite Activity before the delay", got)
	}
	// The next quiet period runs the (still pending) work.
	s.Schedule()
	waitFor(t, func() bool { return runs.Load() == 1 }, "job never ran after re-schedule")
}

// TestIdleScheduler_ActivityCancelsInFlightJob: a new user prompt
// must cancel the background summary that is already talking to
// the model.
func TestIdleScheduler_ActivityCancelsInFlightJob(t *testing.T) {
	started := make(chan struct{})
	var canceled atomic.Bool
	s := newIdleScheduler(10*time.Millisecond, func(ctx context.Context) {
		close(started)
		select {
		case <-ctx.Done():
			canceled.Store(true)
		case <-time.After(2 * time.Second):
		}
	})
	s.Schedule()
	<-started
	s.Activity()
	waitFor(t, canceled.Load, "in-flight job context was not canceled by Activity")
}

// TestIdleScheduler_CloseCancelsAndRefuses: the exit path cancels
// in-flight work and no future Schedule may fire.
func TestIdleScheduler_CloseCancelsAndRefuses(t *testing.T) {
	var runs atomic.Int32
	s := newIdleScheduler(20*time.Millisecond, func(ctx context.Context) {
		runs.Add(1)
	})
	s.Schedule()
	s.Close()
	s.Schedule() // after Close: must be refused
	time.Sleep(80 * time.Millisecond)
	if got := runs.Load(); got != 0 {
		t.Fatalf("job ran %d time(s) after Close", got)
	}
}
