// Deferred LLM session titles. A fresh web session gets its title in
// two steps: a deterministic local one (first words of the first
// prompt) synchronously at session creation — free, instant, never
// competes with the answer — and, for long prompts, a topic title only AFTER the
// first answer finished streaming and the session has been quiet for
// a grace period. The old behaviour fired the title inference the
// moment the request arrived, racing the user's actual answer for
// the same (often single-slot local) backend.
package webgui

import (
	"context"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
)

// titleIdleDelay is how long a session must stay quiet after its
// first answer before the background title inference may run. A
// constant on purpose — not a config knob (mirrors the CLI's
// memoryIdleDelay).
const titleIdleDelay = 15 * time.Second

const sessionTitleMaxRunes = 80

// Short prompts fit as complete local labels in any language; keep them
// without an extra background request to shorten them.
func needsSessionTitle(prompt string) bool {
	return runeLen(collapseWhitespace(stripMarkdownNoise(prompt))) > sessionTitleMaxRunes
}

// titleMaxAttempts bounds retries when the title call was preempted
// by foreground work or storage failures; afterwards the local title simply
// stays.
const titleMaxAttempts = 3

type titleJob struct {
	timer       *time.Timer
	cancel      context.CancelFunc
	attempts    int
	timerGen    uint64 // invalidates callbacks already dispatched by AfterFunc
	runGen      uint64 // identifies the cancel/cleanup owner
	scheduleGen uint64 // preserves an explicitly rearmed job after old completion
}

// titleScheduler defers one title job per session. New requests for
// the session cancel pending and in-flight work (Cancel); the job is
// re-armed after the next stream for a fresh session completes.
type titleScheduler struct {
	mu     sync.Mutex
	delay  time.Duration
	jobs   map[string]*titleJob
	closed bool
	// run performs title generation and reports whether the job is complete.
	run func(ctx context.Context, sessionID, prompt string) bool
}

func newTitleScheduler(delay time.Duration, run func(ctx context.Context, sessionID, prompt string) bool) *titleScheduler {
	return &titleScheduler{delay: delay, jobs: make(map[string]*titleJob), run: run}
}

// armLocked preserves the existing delay and one timer per pending title.
func (s *titleScheduler) armLocked(sessionID, prompt string, job *titleJob) {
	job.timerGen++
	timerGen := job.timerGen
	job.timer = time.AfterFunc(s.delay, func() { s.fire(sessionID, prompt, job, timerGen) })
}

// Schedule (re)arms the idle timer for sessionID's title. Call it
// after the session's first stream has fully completed — never
// before, so the inference cannot race the user's answer.
func (s *titleScheduler) Schedule(sessionID, prompt string) {
	if sessionID == "" || strings.TrimSpace(prompt) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	job := s.jobs[sessionID]
	if job == nil {
		job = &titleJob{}
		s.jobs[sessionID] = job
	}
	if job.attempts >= titleMaxAttempts {
		return
	}
	if job.timer != nil {
		job.timer.Stop()
	}
	job.scheduleGen++
	s.armLocked(sessionID, prompt, job)
}

// Cancel stops the pending timer and aborts any in-flight title call
// for sessionID. Call it the moment a new request for that session
// arrives — the session cannot be idle if the user is typing into it.
func (s *titleScheduler) Cancel(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[sessionID]
	if job == nil {
		return
	}
	job.timerGen++
	if job.timer != nil {
		job.timer.Stop()
		job.timer = nil
	}
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
}

// Close stops every pending timer and in-flight background title call. It is
// invoked after the HTTP server has drained, before Engine closes SQLite.
func (s *titleScheduler) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for id, job := range s.jobs {
		job.timerGen++
		if job.timer != nil {
			job.timer.Stop()
		}
		if job.cancel != nil {
			job.cancel()
		}
		delete(s.jobs, id)
	}
}

func (s *titleScheduler) fire(sessionID, prompt string, expected *titleJob, timerGen uint64) {
	s.mu.Lock()
	job := s.jobs[sessionID]
	// Stop cannot retract an expired callback; job identity also prevents ABA
	// when a completed/deleted session job is replaced under the same key.
	if s.closed || job == nil || job != expected || job.timerGen != timerGen || job.timer == nil {
		s.mu.Unlock()
		return
	}
	job.timer = nil
	job.attempts++
	job.runGen++
	runGen, scheduleGen := job.runGen, job.scheduleGen
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	s.mu.Unlock()
	defer cancel()

	ok := s.run(ctx, sessionID, prompt)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.jobs[sessionID] != job || job.runGen != runGen {
		return
	}
	job.cancel = nil
	// A newer explicit Schedule owns its timer. Cancel alone still preserves
	// the existing failed/preempted attempt retry after the same idle delay.
	if job.scheduleGen != scheduleGen {
		return
	}
	if ok {
		delete(s.jobs, sessionID)
	} else if job.attempts < titleMaxAttempts {
		s.armLocked(sessionID, prompt, job)
	}
}

// runSessionTitleLLM asks the active (metered) provider for a
// topic title and stores it — only if the deterministic local
// title is still current (a manual rename always wins). The call is
// marked background+title by the summarizer, so it queues on the
// background gate and is preempted the moment any foreground call
// starts. Returns true when the job is complete, including an adequate local
// title or a terminal provider fallback.
func (e *Engine) runSessionTitleLLM(ctx context.Context, sessionID, prompt string) bool {
	ctx = llm.WithOpenCodeSession(ctx, sessionID)
	if ctx.Err() != nil {
		return false
	}
	initialTitle := summarizeHistoryMessage(prompt, sessionTitleMaxRunes)
	store, err := e.sessionStore()
	if err != nil {
		return false
	}
	current, err := store.Get(sessionID)
	if err != nil {
		return false
	}
	if current.Title != initialTitle || !needsSessionTitle(prompt) {
		return true // Manual/short names already suffice; no inference or retry.
	}
	e.mu.RLock()
	prov := e.prov
	e.mu.RUnlock()
	// prov is the factory-built metered provider; the per-session
	// usage recorder rides the context instead of a second wrapper.
	if sink := e.usageCallSink(store, sessionID); sink != nil {
		ctx = llm.WithCallSink(ctx, sink)
	}
	// The title is written after the answer finished, so it belongs to no
	// turn; count it with the rest of the out-of-turn model work.
	ctx = e.countOffTurnCalls(ctx)
	title := summarizeHistoryMessageWithProvider(ctx, prompt, sessionTitleMaxRunes, prov)
	if ctx.Err() != nil {
		return false // Foreground preemption may be retried after the idle window.
	}
	if title == "" || strings.HasPrefix(title, "<") || title == initialTitle {
		// A completed request with the same/fallback label is terminal.
		// Retrying an already adequate name only spends more model work.
		return true
	}
	_, err = store.SetTitleIfCurrent(sessionID, initialTitle, title)
	return err == nil
}
