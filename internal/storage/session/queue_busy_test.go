package session

import (
	"context"
	"testing"
	"time"
)

// A message queued while another connection writes (a turn starting) must
// wait for the writer, not fail with SQLITE_BUSY / SQLITE_BUSY_SNAPSHOT
// ("database is locked") as a deferred read-then-write transaction did.
func TestEnqueueTaskWaitsForConcurrentWriter(t *testing.T) {
	ctx := context.Background()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cwd := t.TempDir()
	if _, err := s.EnqueueTaskWithAttachments(ctx, cwd, "", "first", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		holder, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := holder.ExecContext(ctx, `INSERT INTO prompt_queue(id,cwd,prompt,position,created_at,attachments_json) VALUES(?,?,?,?,?,?)`, newID(), cwd, "writer", 100+i, time.Now().UnixNano(), "[]"); err != nil {
			_ = holder.Rollback()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := s.EnqueueTaskWithAttachments(ctx, cwd, "", "queued while a turn writes", nil)
			done <- err
		}()
		time.Sleep(150 * time.Millisecond) // the enqueue is now waiting on the writer
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("round %d: enqueue during a concurrent write: %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("round %d: enqueue did not finish", i)
		}
	}
	tasks, err := s.ListQueuedTasks(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 11 {
		t.Fatalf("queued tasks = %d, want 11", len(tasks))
	}
}
