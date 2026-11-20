package webgui

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 1.0.37: a user turn (e.g. a queued message) that finds an automatic
// wake-up turn holding the conversation waits for it instead of failing
// with "busy" after 3 s; two user turns keep the bounded wait.
func TestUserTurnWaitsForAutomaticWakeTurn(t *testing.T) {
	e := &Engine{}
	wakeCtx := context.WithValue(context.Background(), wakeRunKey{}, true)
	release, err := e.claimSessionRun(wakeCtx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		rel, err := e.claimSessionRun(context.Background(), "s1", 50*time.Millisecond)
		if rel != nil {
			rel()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("user turn gave up while the automatic turn ran: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err := <-got; err != nil {
		t.Fatalf("user turn after the automatic turn: %v", err)
	}

	userRelease, err := e.claimSessionRun(context.Background(), "s2", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer userRelease()
	if _, err := e.claimSessionRun(context.Background(), "s2", 50*time.Millisecond); !errors.Is(err, errSessionBusy) {
		t.Fatalf("second user turn = %v, want busy after the bounded wait", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wakeRelease, _ := e.claimSessionRun(wakeCtx, "s3", 0)
	defer wakeRelease()
	if _, err := e.claimSessionRun(ctx, "s3", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait = %v", err)
	}
}
