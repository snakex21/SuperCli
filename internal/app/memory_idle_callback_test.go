package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// Expired callbacks can enter fire after Activity or a replacement Schedule.
// Invoke that callback directly so the ordering is deterministic without sleeps.
func TestIdleSchedulerInvalidatesExpiredCallbacks(t *testing.T) {
	for _, action := range []string{"activity", "reschedule", "close"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			provider := llm.Metered(memoryPreemptionProvider{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
				calls.Add(1)
				stream := make(chan llm.Delta, 1)
				stream <- llm.Delta{Content: "NOTHING"}
				close(stream)
				return stream, nil
			}}, "test", "main", nil)
			s := newIdleScheduler(time.Hour, func(ctx context.Context) {
				_, _ = providerSummarizer(provider)(ctx, "Summarize the completed task.")
			})
			defer s.Close()
			s.Schedule()
			expiredGeneration := s.timerGen
			s.timer.Stop() // Stand in for a timer whose callback has been queued.
			switch action {
			case "activity":
				s.Activity()
			case "reschedule":
				s.Schedule()
			case "close":
				s.Close()
			}
			replacement := s.timer
			s.fire(expiredGeneration)
			if got := calls.Load(); got != 0 {
				t.Fatalf("expired callback started %d provider calls after %s", got, action)
			}
			if s.timer != replacement {
				t.Fatal("expired callback changed the replacement timer")
			}
		})
	}
}

func TestIdleSchedulerCurrentCallbackKeepsContextLifecycle(t *testing.T) {
	var calls int
	var jobContext context.Context
	s := newIdleScheduler(time.Hour, func(ctx context.Context) {
		calls++
		jobContext = ctx
		if ctx.Err() != nil {
			t.Fatal("current callback started with a canceled context")
		}
	})
	defer s.Close()
	s.Schedule()
	oldGeneration := s.timerGen
	s.Schedule()
	currentGeneration := s.timerGen
	s.fire(oldGeneration)
	if calls != 0 {
		t.Fatal("replaced callback started a job")
	}
	s.timer.Stop()
	s.fire(currentGeneration)
	if calls != 1 || jobContext == nil || jobContext.Err() != context.Canceled {
		t.Fatalf("current callback lifecycle: calls=%d context=%v", calls, jobContext)
	}
	if s.timer != nil || s.cancel != nil {
		t.Fatal("completed callback retained timer or cancellation ownership")
	}
}

func TestIdleSchedulerLateJobCannotClearReplacementCancel(t *testing.T) {
	firstStarted := make(chan context.Context, 1)
	secondStarted := make(chan context.Context, 1)
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	firstFinished := make(chan struct{})
	secondFinished := make(chan struct{})
	var calls atomic.Int32
	s := newIdleScheduler(time.Hour, func(ctx context.Context) {
		switch calls.Add(1) {
		case 1:
			firstStarted <- ctx
			<-releaseFirst
		case 2:
			secondStarted <- ctx
			<-releaseSecond
		}
	})
	defer s.Close()
	secondReleased := false
	defer func() {
		if !secondReleased {
			close(releaseSecond)
		}
	}()
	firstReleased := false
	defer func() {
		if !firstReleased {
			close(releaseFirst)
		}
	}()
	waitContext := func(ch <-chan context.Context) context.Context {
		t.Helper()
		select {
		case ctx := <-ch:
			return ctx
		case <-time.After(2 * time.Second):
			t.Fatal("job did not start")
			return nil
		}
	}
	waitDone := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("completion notification missing")
		}
	}
	s.Schedule()
	firstGeneration := s.timerGen
	s.timer.Stop()
	go func() { s.fire(firstGeneration); close(firstFinished) }()
	firstContext := waitContext(firstStarted)
	s.Activity()
	if firstContext.Err() != context.Canceled {
		t.Fatal("activity did not cancel the first job")
	}
	s.Schedule()
	secondGeneration := s.timerGen
	s.timer.Stop()
	go func() { s.fire(secondGeneration); close(secondFinished) }()
	secondContext := waitContext(secondStarted)
	// A stale callback must neither start another job nor steal its cancel.
	s.fire(firstGeneration)
	if got := calls.Load(); got != 2 {
		t.Fatalf("stale callback changed job count to %d", got)
	}
	close(releaseFirst)
	firstReleased = true
	waitDone(firstFinished)
	if secondContext.Err() != nil {
		t.Fatal("late first-job completion canceled the second job")
	}
	s.Activity()
	if secondContext.Err() != context.Canceled {
		t.Fatal("late first-job completion cleared the second cancellation owner")
	}
	close(releaseSecond)
	secondReleased = true
	waitDone(secondFinished)
}
