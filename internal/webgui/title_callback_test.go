package webgui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

type titleCallbackTestProvider struct {
	t        *testing.T
	calls    atomic.Int32
	mu       sync.Mutex
	requests []string
}

func (p *titleCallbackTestProvider) Name() string { return "owned-title-audit" }
func (p *titleCallbackTestProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	if !llm.IsBackground(ctx) || llm.PurposeFromContext(ctx) != llm.PurposeTitle {
		p.t.Error("helper lost background/title purpose")
	}
	if len(messages) != 2 || len(tools) != 0 {
		p.t.Errorf("helper request shape messages=%d tools=%d", len(messages), len(tools))
	}
	p.mu.Lock()
	p.requests = append(p.requests, messages[1].Content)
	p.mu.Unlock()
	out := make(chan llm.Delta, 1)
	out <- llm.Delta{Content: "Owned audit title"}
	close(out)
	return out, nil
}

func newTitleCallbackTestScheduler(t *testing.T, after func(context.Context, int) bool) (*titleScheduler, *atomic.Int32, *titleCallbackTestProvider) {
	t.Helper()
	helper := new(atomic.Int32)
	provider := &titleCallbackTestProvider{t: t}
	s := newTitleScheduler(time.Hour, func(ctx context.Context, _, prompt string) bool {
		n := int(helper.Add(1))
		if got := summarizeHistoryMessageWithProvider(ctx, prompt, 80, provider); got != "Owned audit title" {
			t.Errorf("title = %q", got)
		}
		if after != nil {
			return after(ctx, n)
		}
		return true
	})
	t.Cleanup(s.Close)
	return s, helper, provider
}

func checkTitleCallbackCounts(t *testing.T, helper *atomic.Int32, p *titleCallbackTestProvider, want int32) {
	t.Helper()
	t.Logf("synthetic helper callbacks=%d Complete calls=%d expected=%d", helper.Load(), p.calls.Load(), want)
	if helper.Load() != want || p.calls.Load() != want {
		t.Errorf("callbacks/Complete = %d/%d, want %d/%d", helper.Load(), p.calls.Load(), want, want)
	}
}

func TestTitleCallback_CancelAfterDispatch(t *testing.T) {
	s, helper, p := newTitleCallbackTestScheduler(t, nil)
	s.Schedule("owned", "Only a synthetic topic")
	// A dispatched AfterFunc closure may run after Stop. Freeze that closure,
	// cancel user activity, then deliver it, without timers/sleeps/polling.
	queued := captureTitleCallback(s, "owned", "Only a synthetic topic")
	s.Cancel("owned")
	queued()
	checkTitleCallbackCounts(t, helper, p, 0)
}

func TestTitleCallback_RescheduleAfterDispatch(t *testing.T) {
	s, helper, p := newTitleCallbackTestScheduler(t, nil)
	s.Schedule("owned", "Original synthetic topic")
	old := captureTitleCallback(s, "owned", "Original synthetic topic")
	s.Schedule("owned", "Latest synthetic topic")
	latest := captureTitleCallback(s, "owned", "Latest synthetic topic")
	old()
	checkTitleCallbackCounts(t, helper, p, 0)
	latest()
	checkTitleCallbackCounts(t, helper, p, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 1 || !strings.Contains(p.requests[0], "Latest synthetic topic") {
		t.Error("replacement timer lost the latest supplied topic")
	}
}

func TestTitleCallback_ReplacementJobIdentity(t *testing.T) {
	s, helper, p := newTitleCallbackTestScheduler(t, nil)
	s.Schedule("owned", "First synthetic topic")
	old := captureTitleCallback(s, "owned", "First synthetic topic")
	old() // valid completion deletes the original job
	s.Schedule("owned", "Replacement synthetic topic")
	latest := captureTitleCallback(s, "owned", "Replacement synthetic topic")
	old() // a retained old dispatch must not consume a new job with the same ID
	checkTitleCallbackCounts(t, helper, p, 1)
	latest()
	checkTitleCallbackCounts(t, helper, p, 2)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) != 2 || !strings.Contains(p.requests[1], "Replacement synthetic topic") {
		t.Error("completed job consumed a replacement job")
	}
}

func TestTitleCallback_CloseAndNormalCompletion(t *testing.T) {
	t.Run("close drops queued callback", func(t *testing.T) {
		s, helper, p := newTitleCallbackTestScheduler(t, nil)
		s.Schedule("owned", "Synthetic topic")
		queued := captureTitleCallback(s, "owned", "Synthetic topic")
		s.Close()
		queued()
		s.Schedule("owned", "Ignored after close")
		checkTitleCallbackCounts(t, helper, p, 0)
	})
	t.Run("valid callback completes once", func(t *testing.T) {
		s, helper, p := newTitleCallbackTestScheduler(t, nil)
		s.Schedule("owned", "Synthetic topic")
		captureTitleCallback(s, "owned", "Synthetic topic")()
		checkTitleCallbackCounts(t, helper, p, 1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.jobs["owned"] != nil {
			t.Error("successful title job not removed")
		}
	})
}

func TestTitleCallback_PreemptedRunKeepsBoundedRetry(t *testing.T) {
	started := make(chan context.Context, 1)
	s, helper, p := newTitleCallbackTestScheduler(t, func(ctx context.Context, n int) bool {
		if n == 1 {
			started <- ctx
			<-ctx.Done()
			return false
		}
		return true
	})
	s.Schedule("owned", "Synthetic topic")
	first := captureTitleCallback(s, "owned", "Synthetic topic")
	done := make(chan struct{})
	go func() { first(); close(done) }()
	<-started
	s.Cancel("owned")
	<-done
	s.mu.Lock()
	job := s.jobs["owned"]
	hasRetry := job != nil && job.timer != nil && job.attempts == 1
	s.mu.Unlock()
	if !hasRetry {
		t.Fatal("preempted active run lost existing idle retry")
	}
	captureTitleCallback(s, "owned", "Synthetic topic")()
	checkTitleCallbackCounts(t, helper, p, 2)
}

func TestTitleCallback_AttemptLimitAndBlankSchedule(t *testing.T) {
	s, helper, p := newTitleCallbackTestScheduler(t, func(context.Context, int) bool { return false })
	s.Schedule("", "Synthetic topic")
	s.Schedule("owned", "  ")
	if len(s.jobs) != 0 {
		t.Error("blank Schedule created a job")
	}
	s.Schedule("owned", "Synthetic topic")
	for i := 0; i < titleMaxAttempts; i++ {
		captureTitleCallback(s, "owned", "Synthetic topic")()
	}
	checkTitleCallbackCounts(t, helper, p, titleMaxAttempts)
	s.mu.Lock()
	before := s.jobs["owned"].timer
	s.mu.Unlock()
	s.Schedule("owned", "Ignored above the attempt limit")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs["owned"].attempts != titleMaxAttempts || s.jobs["owned"].timer != before {
		t.Error("exhausted title attempt limit was rearmed")
	}
}

func TestTitleCallback_LateCleanupDoesNotOwnNewRun(t *testing.T) {
	for _, oldSuccess := range []bool{false, true} {
		t.Run(map[bool]string{false: "old failure", true: "old success"}[oldSuccess], func(t *testing.T) {
			started := make(chan context.Context, 2)
			releaseOld, releaseNew := make(chan struct{}), make(chan struct{})
			s, helper, p := newTitleCallbackTestScheduler(t, func(ctx context.Context, n int) bool {
				started <- ctx
				if n == 1 {
					<-releaseOld
					return oldSuccess
				}
				<-releaseNew
				return false
			})
			s.Schedule("owned", "Old synthetic topic")
			first := captureTitleCallback(s, "owned", "Old synthetic topic")
			doneOld := make(chan struct{})
			go func() { first(); close(doneOld) }()
			oldCtx := <-started
			s.Cancel("owned")
			if oldCtx.Err() == nil {
				t.Error("Cancel did not abort old run")
			}
			s.Schedule("owned", "New synthetic topic")
			second := captureTitleCallback(s, "owned", "New synthetic topic")
			doneNew := make(chan struct{})
			go func() { second(); close(doneNew) }()
			newCtx := <-started
			close(releaseOld)
			<-doneOld
			if newCtx.Err() != nil {
				t.Error("old cleanup canceled the newer context")
			}
			s.Cancel("owned")
			if newCtx.Err() == nil {
				t.Error("old cleanup detached the newer cancel owner")
			}
			close(releaseNew)
			<-doneNew
			checkTitleCallbackCounts(t, helper, p, 2)
		})
	}
}

func TestTitleCallback_LateCompletionPreservesExplicitTimer(t *testing.T) {
	for _, oldSuccess := range []bool{false, true} {
		t.Run(map[bool]string{false: "old failure", true: "old success"}[oldSuccess], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			s, helper, p := newTitleCallbackTestScheduler(t, func(_ context.Context, n int) bool {
				if n == 1 {
					close(started)
					<-release
					return oldSuccess
				}
				return true
			})
			s.Schedule("owned", "Old synthetic topic")
			first := captureTitleCallback(s, "owned", "Old synthetic topic")
			done := make(chan struct{})
			go func() { first(); close(done) }()
			<-started
			s.Schedule("owned", "Latest synthetic topic")
			latest := captureTitleCallback(s, "owned", "Latest synthetic topic")
			s.mu.Lock()
			job, timer := s.jobs["owned"], s.jobs["owned"].timer
			s.mu.Unlock()
			close(release)
			<-done
			s.mu.Lock()
			retained := s.jobs["owned"] == job && job.timer == timer
			s.mu.Unlock()
			if !retained {
				t.Error("old completion deleted or replaced the newer explicit timer")
			}
			latest()
			checkTitleCallbackCounts(t, helper, p, 2)
		})
	}
}

// Freeze the exact job/token installed by armLocked. Stop only the owned
// synthetic long-delay timer on delivery to model an already expired callback.
func captureTitleCallback(s *titleScheduler, sessionID, prompt string) func() {
	s.mu.Lock()
	job := s.jobs[sessionID]
	timerGen, timer := job.timerGen, job.timer
	s.mu.Unlock()
	return func() {
		if timer != nil {
			timer.Stop()
		}
		s.fire(sessionID, prompt, job, timerGen)
	}
}
