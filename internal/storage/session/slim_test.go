package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

func bigActivity(n int) json.RawMessage {
	calls := make([]map[string]any, n)
	for i := range calls {
		calls[i] = map[string]any{"tool": "read_lines", "args": strings.Repeat("a", 200), "output": strings.Repeat("line of output\n", 40)}
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "id": "worker-3", "run": 1, "calls": calls})
	return raw
}

func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// New notification rows keep a large worker snapshot by handle; every reader
// gets the original origin back, and the cache never evicts it.
func TestWorkerSnapshotIsStoredByHandle(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sess, _ := s.Create("/w", "m", "t")
	w := NewWriter(s, sess.ID)
	activity := bigActivity(40)
	msg := llm.Message{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: "<task-notification>report</task-notification>",
		Origin: &llm.ContextOrigin{Source: llm.SourceWorkerNotification, WorkerActivity: activity}}
	if err := w.AppendMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = s.db.QueryRow(`SELECT origin_json FROM messages WHERE session_id = ?`, sess.ID).Scan(&stored)
	if len(stored) > 512 || !strings.Contains(stored, WorkerActivityHandlePrefix) {
		t.Fatalf("snapshot still inline: %d bytes", len(stored))
	}
	want, _ := FromMessage(msg)
	rows, _ := s.ReadMessages(ctx, sess.ID)
	page, _, _ := s.ReadMessagesBefore(ctx, sess.ID, 0, 10)
	one, _ := s.ReadMessageAt(ctx, sess.ID, 1)
	tr, _ := s.ReadTranscriptMessageAt(ctx, sess.ID, 1)
	for name, got := range map[string]string{"all": rows[0].OriginJSON, "page": page[0].OriginJSON, "at": one.OriginJSON, "transcript": tr.OriginJSON} {
		if !sameJSON(t, got, want.OriginJSON) {
			t.Fatalf("%s reader did not restore the snapshot", name)
		}
	}
	for i := 0; i < maxSavedOutputs+20; i++ {
		if err := w.SaveToolOutput(ctx, fmt.Sprintf("out_%08x", i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ = s.ReadMessages(ctx, sess.ID)
	if !sameJSON(t, rows[0].OriginJSON, want.OriginJSON) {
		t.Fatal("the cache evicted a worker snapshot")
	}
}

// The one-time migration: backup first, inline snapshots moved, readers see
// the same transcript, finished old workers pruned safely, idempotent.
func TestSlimMigratesAndPrunesSafely(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sess, _ := s.Create("/w", "m", "t")
	origin, _ := json.Marshal(map[string]any{"source": "worker_notification", "worker_activity": json.RawMessage(bigActivity(30))})
	for i := 0; i < 5; i++ {
		// Rows written before this change: inserted inline, bypassing the writer.
		if _, err := s.db.Exec(`INSERT INTO messages(session_id, seq, role, content, origin_json, created_at) VALUES(?,?,?,?,?,?)`,
			sess.ID, i+1, "user", fmt.Sprintf("report %d", i), string(origin), time.Now().UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	history := func(n int) []llm.Message {
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "BRIEF: build the night cycle"}}
		for i := 0; i < n; i++ {
			msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("step %d", i)})
		}
		return append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "FINAL REPORT: done"})
	}
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	for _, w := range []struct {
		id, status string
		at         time.Time
	}{{"worker-1", "done", old}, {"worker-2", "done", now}, {"worker-3", "failed", old}, {"worker-4", "interrupted", old}, {"worker-5", "running", old}} {
		if err := s.SaveWorker(ctx, WorkerRow{SessionID: sess.ID, ID: w.id, Agent: "code", Description: "d", Status: w.status, Messages: history(100), UpdatedAt: w.at}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.ReadMessages(ctx, sess.ID)
	rep, err := s.Slim(ctx, SlimPolicy{WorkerMaxAge: 14 * 24 * time.Hour, WorkerKeepLast: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backup == "" || rep.ActivityRows != 5 || rep.PrunedWorkers != 1 || !rep.MigrationApplied {
		t.Fatalf("report = %+v", rep)
	}
	if st, err := os.Stat(rep.Backup); err != nil || st.Size() == 0 || filepath.Dir(rep.Backup) != dir {
		t.Fatalf("no backup next to the database: %v", err)
	}
	after, _ := s.ReadMessages(ctx, sess.ID)
	for i := range before {
		if !sameJSON(t, before[i].OriginJSON, after[i].OriginJSON) || before[i].Content != after[i].Content {
			t.Fatalf("row %d changed for readers", i)
		}
	}
	for _, id := range []string{"worker-2", "worker-3", "worker-4", "worker-5"} {
		rec, ok, _ := s.LoadWorker(ctx, sess.ID, id)
		if !ok || len(rec.Messages) != 102 {
			t.Fatalf("%s was pruned (%d messages); only old finished workers may be", id, len(rec.Messages))
		}
	}
	rec, _, _ := s.LoadWorker(ctx, sess.ID, "worker-1")
	if len(rec.Messages) != 12 || rec.Messages[0].Content != "BRIEF: build the night cycle" ||
		!strings.Contains(rec.Messages[1].Content, "pruned") || rec.Messages[len(rec.Messages)-1].Content != "FINAL REPORT: done" {
		t.Fatalf("pruned worker = %d messages: first %q", len(rec.Messages), rec.Messages[0].Content)
	}
	again, err := s.Slim(ctx, SlimPolicy{WorkerMaxAge: 14 * 24 * time.Hour, WorkerKeepLast: 10}, now)
	if err != nil || again.Backup != "" || again.ActivityRows != 0 || again.PrunedWorkers != 0 {
		t.Fatalf("second pass did work again: %+v %v", again, err)
	}
}
