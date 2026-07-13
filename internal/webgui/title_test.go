package webgui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/storage/session"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// The title job only fires after the idle delay — never immediately,
// so it cannot race the first answer for the backend.
func TestTitleScheduler_NotImmediate(t *testing.T) {
	var ran atomic.Int32
	s := newTitleScheduler(50*time.Millisecond, func(context.Context, string, string) bool {
		ran.Add(1)
		return true
	})
	s.Schedule("sess", "prompt")
	if ran.Load() != 0 {
		t.Fatal("title job ran synchronously with Schedule")
	}
	if !waitFor(t, 2*time.Second, func() bool { return ran.Load() == 1 }) {
		t.Fatalf("title job did not fire after the idle delay (ran=%d)", ran.Load())
	}
}

// A new request for the session cancels the pending timer: no
// inference happens while the user is active.
func TestTitleScheduler_CancelStopsPendingJob(t *testing.T) {
	var ran atomic.Int32
	s := newTitleScheduler(30*time.Millisecond, func(context.Context, string, string) bool {
		ran.Add(1)
		return true
	})
	s.Schedule("sess", "prompt")
	s.Cancel("sess")
	time.Sleep(100 * time.Millisecond)
	if ran.Load() != 0 {
		t.Fatalf("canceled title job still ran %d time(s)", ran.Load())
	}
}

// Cancel aborts an in-flight job through its context, and a failed
// (preempted) attempt re-arms — bounded by titleMaxAttempts, after
// which the local title simply stays.
func TestTitleScheduler_CancelAbortsInFlightAndRetriesAreBounded(t *testing.T) {
	started := make(chan struct{}, titleMaxAttempts+1)
	var canceled atomic.Int32
	var runs atomic.Int32
	s := newTitleScheduler(10*time.Millisecond, func(ctx context.Context, _, _ string) bool {
		runs.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		canceled.Add(1)
		return false // preempted: the LLM title never landed
	})
	s.Schedule("sess", "prompt")
	<-started
	s.Cancel("sess")
	if !waitFor(t, 2*time.Second, func() bool { return canceled.Load() >= 1 }) {
		t.Fatal("in-flight title job was not canceled")
	}
	// The remaining retries fire, fail (ctx canceled after Cancel is a
	// one-shot; later attempts block until we cancel them again), and
	// eventually the scheduler gives up for good.
	for i := 0; i < titleMaxAttempts; i++ {
		select {
		case <-started:
			s.Cancel("sess")
		case <-time.After(2 * time.Second):
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := runs.Load(); got > titleMaxAttempts {
		t.Fatalf("title job ran %d times, want <= %d", got, titleMaxAttempts)
	}
}

// A canceled LLM title never leaves the session unnamed: the
// deterministic local title set at session creation stays in place.
func TestRunSessionTitleLLM_CanceledKeepsLocalTitle(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	prompt := "please rename all the widgets in the dashboard"
	local := summarizeHistoryMessage(prompt, 80)
	sess, err := store.Create(dir, eng.ModelName(), local)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok := eng.runSessionTitleLLM(ctx, sess.ID, prompt); ok {
		t.Fatal("canceled title call reported success")
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != local {
		t.Fatalf("title after canceled LLM call = %q, want local %q", got.Title, local)
	}
}

// With a live (echo) provider the deferred job replaces the local
// title, and a manual rename is never overwritten.
func TestRunSessionTitleLLM_SetsTitleAndRespectsRename(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := session.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	prompt := "summarize the quarterly report for me"
	local := summarizeHistoryMessage(prompt, 80)
	sess, err := store.Create(dir, eng.ModelName(), local)
	if err != nil {
		t.Fatal(err)
	}
	if ok := eng.runSessionTitleLLM(context.Background(), sess.ID, prompt); !ok {
		t.Fatal("echo-backed title call failed")
	}
	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title == local || got.Title == "" {
		t.Fatalf("LLM title did not land: %q", got.Title)
	}

	// Manual rename wins over a later regeneration.
	if err := store.SetTitle(sess.ID, "my own name"); err != nil {
		t.Fatal(err)
	}
	_ = eng.runSessionTitleLLM(context.Background(), sess.ID, prompt)
	got, _ = store.Get(sess.ID)
	if got.Title != "my own name" {
		t.Fatalf("regeneration overwrote a manual rename: %q", got.Title)
	}
}
