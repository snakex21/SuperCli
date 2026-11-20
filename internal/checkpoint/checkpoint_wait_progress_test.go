package checkpoint

import (
	"context"
	"errors"
	"testing"
	"time"

	tools "supercli/internal/tools/core"
)

// 1.0.38: parallel workers of one coordinator turn share the turn's
// checkpoint lock. A file tool waiting for it reports that as live progress
// instead of looking like the tool itself hangs ("Creating file running 3m").
func TestToolWaitingForCheckpointLockReportsProgress(t *testing.T) {
	turn := &Turn{}
	turn.mu.Lock() // another agent's snapshot holds the lock
	got := make(chan string, 4)
	ctx, cancel := context.WithCancel(tools.WithToolProgress(context.Background(), func(p tools.ToolProgress) { got <- p.Message }))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := turn.ensureBeforeForToolNotice(ctx, "create_file", []byte(`{"path":"a.zig","content":"x"}`))
		done <- err
	}()
	select {
	case msg := <-got:
		if msg != CheckpointWaitProgress {
			t.Fatalf("progress = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no progress while waiting for the checkpoint lock")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	turn.mu.Unlock()
}
