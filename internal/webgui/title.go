// Deferred LLM session titles. A fresh web session gets its title in
// two steps: a deterministic local one (first words of the first
// prompt) synchronously at session creation — free, instant, never
// competes with the answer — and a nicer LLM summary only AFTER the
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
	"supercli/internal/storage/session"
)

// titleIdleDelay is how long a session must stay quiet after its
// first answer before the background title inference may run. A
// constant on purpose — not a config knob (mirrors the CLI's
// memoryIdleDelay).
const titleIdleDelay = 15 * time.Second

// titleMaxAttempts bounds retries when the title call was preempted
// by foreground work or failed; afterwards the local title simply
// stays.
const titleMaxAttempts = 3

type titleJob struct {
	timer    *time.Timer
	cancel   context.CancelFunc // non-nil while the LLM call is in flight
	attempts int
}

// titleScheduler defers one title job per session. New requests for
// the session cancel pending and in-flight work (Cancel); the job is
// re-armed after the next stream for a fresh session completes.
type titleScheduler struct {
	mu    sync.Mutex
	delay time.Duration
	jobs  map[string]*titleJob
	// run performs the actual title generation; it reports success so
	// the scheduler knows whether to retry. Swappable in tests.
	run func(ctx context.Context, sessionID, prompt string) bool
}

func newTitleScheduler(delay time.Duration, run func(ctx context.Context, sessionID, prompt string) bool) *titleScheduler {
	return &titleScheduler{delay: delay, jobs: make(map[string]*titleJob), run: run}
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
	job := s.jobs[sessionID]
	if job == nil {
		job = &titleJob{}
		s.jobs[sessionID] = job
	}
	if job.attempts >= titleMaxAttempts {
		return // the local title stays; stop burning inference on it
	}
	if job.timer != nil {
		job.timer.Stop()
	}
	job.timer = time.AfterFunc(s.delay, func() { s.fire(sessionID, prompt) })
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
	if job.timer != nil {
		job.timer.Stop()
		job.timer = nil
	}
	if job.cancel != nil {
		job.cancel()
		job.cancel = nil
	}
}

func (s *titleScheduler) fire(sessionID, prompt string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	job := s.jobs[sessionID]
	if job == nil {
		s.mu.Unlock()
		cancel()
		return
	}
	job.attempts++
	job.cancel = cancel
	s.mu.Unlock()

	ok := s.run(ctx, sessionID, prompt)

	s.mu.Lock()
	if job.cancel != nil {
		job.cancel = nil
	}
	retry := !ok && job.attempts < titleMaxAttempts
	if ok {
		delete(s.jobs, sessionID)
	} else if retry && job.timer != nil {
		job.timer.Stop()
	}
	if retry {
		// Preempted by foreground work (or the model failed): the
		// local title is still in place; try again after another
		// quiet window.
		job.timer = time.AfterFunc(s.delay, func() { s.fire(sessionID, prompt) })
	}
	s.mu.Unlock()
	cancel()
}

// runSessionTitleLLM asks the active (metered) provider for a
// PR-style title and stores it — only if the deterministic local
// title is still current (a manual rename always wins). The call is
// marked background+title by the summarizer, so it queues on the
// background gate and is preempted the moment any foreground call
// starts. Returns true when the LLM title actually landed.
func (e *Engine) runSessionTitleLLM(ctx context.Context, sessionID, prompt string) bool {
	initialTitle := summarizeHistoryMessage(prompt, 80)
	store, err := session.OpenStore(e.dataDir)
	if err != nil {
		return false
	}
	defer store.Close()
	e.mu.RLock()
	prov := e.prov
	e.mu.RUnlock()
	// prov is the factory-built metered provider; the per-session
	// usage recorder rides the context instead of a second wrapper.
	if sink := e.usageCallSink(store, sessionID); sink != nil {
		ctx = llm.WithCallSink(ctx, sink)
	}
	title := summarizeHistoryMessageWithProvider(ctx, prompt, 80, prov)
	if title == "" || strings.HasPrefix(title, "<") || title == initialTitle {
		// Canceled/failed calls fall back to the local title — the
		// session is never left unnamed, and the scheduler may retry.
		return false
	}
	_, err = store.SetTitleIfCurrent(sessionID, initialTitle, title)
	return err == nil
}
