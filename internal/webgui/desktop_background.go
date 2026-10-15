package webgui

import (
	"sync"
	"time"
)

// nativeWindowOptions configures the native desktop window.
type nativeWindowOptions struct {
	URL      string
	AppName  string
	DataDir  string
	IconPath string
	// HasActiveWork decides whether closing asks before stopping work.
	HasActiveWork func() bool
	// NeedsAttention brings a hidden window back, e.g. for ask_user.
	NeedsAttention func() bool
	// ShowEvent is the named event a second launch signals to reveal this
	// window instead of reporting that the application is already running.
	ShowEvent string
}

// backgroundRunner keeps the application alive after its window is closed
// while work is still running. The window is only hidden, so the page and
// its streams keep running unchanged; the process quits once work has been
// idle for idleGrace, and returns to view when the work needs the user.
type backgroundRunner struct {
	mu       sync.Mutex
	hidden   bool
	quitting bool
	gen      int

	hasActiveWork  func() bool
	needsAttention func() bool
	hide, show     func()
	quit           func()
	interval       time.Duration
	idleGrace      time.Duration
}

func (b *backgroundRunner) enter() {
	b.mu.Lock()
	if b.hidden || b.quitting {
		b.mu.Unlock()
		return
	}
	b.hidden = true
	b.gen++
	gen := b.gen
	b.mu.Unlock()
	b.hide()
	go b.watch(gen)
}

// leave reveals the window; it is also used when the window is already
// visible and a second launch only asks to bring it forward.
func (b *backgroundRunner) leave() {
	b.mu.Lock()
	if b.quitting {
		b.mu.Unlock()
		return
	}
	b.hidden = false
	b.gen++
	b.mu.Unlock()
	b.show()
}

func (b *backgroundRunner) current(gen int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hidden && !b.quitting && b.gen == gen
}

func (b *backgroundRunner) watch(gen int) {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	var idleSince time.Time
	for range ticker.C {
		if !b.current(gen) {
			return
		}
		if b.needsAttention != nil && b.needsAttention() {
			b.leave()
			return
		}
		// A queued follow-up starts shortly after the previous run ends, so
		// one idle sample is not enough to decide that the work is finished.
		if b.hasActiveWork != nil && b.hasActiveWork() {
			idleSince = time.Time{}
			continue
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
			continue
		}
		if time.Since(idleSince) < b.idleGrace {
			continue
		}
		b.mu.Lock()
		if b.hidden && !b.quitting && b.gen == gen {
			b.quitting = true
			b.quit()
		}
		b.mu.Unlock()
		return
	}
}
