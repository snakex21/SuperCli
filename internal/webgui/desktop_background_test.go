package webgui

import (
	"sync/atomic"
	"testing"
	"time"
)

type fakeBackground struct {
	active, attention   atomic.Bool
	hides, shows, quits atomic.Int32
}

func (f *fakeBackground) runner() *backgroundRunner {
	return &backgroundRunner{
		hasActiveWork:  f.active.Load,
		needsAttention: f.attention.Load,
		hide:           func() { f.hides.Add(1) },
		show:           func() { f.shows.Add(1) },
		quit:           func() { f.quits.Add(1) },
		interval:       2 * time.Millisecond,
		idleGrace:      20 * time.Millisecond,
	}
}

func waitBackground(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBackgroundQuitsOnlyAfterWorkStaysIdle(t *testing.T) {
	f := &fakeBackground{}
	f.active.Store(true)
	b := f.runner()
	b.enter()
	b.enter() // a second close while hidden must not start another watcher
	time.Sleep(60 * time.Millisecond)
	if f.hides.Load() != 1 || f.quits.Load() != 0 {
		t.Fatalf("hides=%d quits=%d while work is active", f.hides.Load(), f.quits.Load())
	}
	f.active.Store(false)
	waitBackground(t, "quit", func() bool { return f.quits.Load() == 1 })
	b.leave()
	if f.shows.Load() != 0 {
		t.Fatal("window was shown after quitting began")
	}
}

func TestBackgroundShowsWindowWhenWorkNeedsUser(t *testing.T) {
	f := &fakeBackground{}
	f.active.Store(true)
	b := f.runner()
	b.enter()
	f.attention.Store(true)
	waitBackground(t, "show", func() bool { return f.shows.Load() == 1 })
	f.active.Store(false)
	time.Sleep(60 * time.Millisecond)
	if f.quits.Load() != 0 {
		t.Fatal("visible window quit after its work finished")
	}
}

func TestBackgroundLeaveCancelsPendingQuit(t *testing.T) {
	f := &fakeBackground{}
	b := f.runner()
	b.enter()
	b.leave()
	time.Sleep(60 * time.Millisecond)
	if f.quits.Load() != 0 || f.shows.Load() != 1 {
		t.Fatalf("quits=%d shows=%d after returning to the window", f.quits.Load(), f.shows.Load())
	}
	// Closing again after returning starts a fresh background period.
	b.enter()
	waitBackground(t, "quit", func() bool { return f.quits.Load() == 1 })
}
