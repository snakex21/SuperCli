package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
)

func TestStoreAppendMessageWithSeqReturnsItsExactInsert(t *testing.T) {
	s := openTestStore(t)
	first, err := s.Create(t.TempDir(), "fixture", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(t.TempDir(), "fixture", "second")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, test := range []struct {
		session string
		role    string
		content string
		seq     int
	}{
		{first.ID, "user", "first prompt", 1},
		{second.ID, "user", "other session prompt", 1},
		{first.ID, "assistant", "first answer", 2},
	} {
		// A caller-provided sequence must never replace the actual assignment.
		seq, err := s.AppendMessageWithSeq(ctx, test.session, Encoded{Seq: 999, Role: test.role, Content: test.content})
		if err != nil || seq != test.seq {
			t.Fatalf("append returned seq=%d, want %d, error=%v", seq, test.seq, err)
		}
		row, err := s.ReadMessageAt(ctx, test.session, seq)
		if err != nil || row.Content != test.content || row.Role != test.role {
			t.Fatalf("returned sequence points at another insert: row=%+v, error=%v", row, err)
		}
	}
	// The original API still appends through the same transaction.
	if err := s.AppendMessage(ctx, first.ID, Encoded{Role: "user", Content: "legacy API"}); err != nil {
		t.Fatal(err)
	}
	row, err := s.ReadMessageAt(ctx, first.ID, 3)
	if err != nil || row.Content != "legacy API" {
		t.Fatalf("legacy append changed: row=%+v, error=%v", row, err)
	}
	meta, err := s.Get(first.ID)
	if err != nil || meta.MessageCount != 3 {
		t.Fatalf("append message count changed: count=%d, error=%v", meta.MessageCount, err)
	}
}

func TestWriterFirstUserSeqBelongsToInvocation(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "fixture", "overlapping invocations")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, row := range []Encoded{
		{Role: "user", Content: "historical prompt"},
		{Role: "assistant", Content: "historical answer"},
	} {
		if err := s.AppendMessage(ctx, sess.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	older := NewWriter(s, sess.ID)
	newer := NewWriter(s, sess.ID)
	if older.FirstUserSeq() != 0 || newer.FirstUserSeq() != 0 {
		t.Fatal("fresh Writer adopted historical user sequence")
	}
	if err := older.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "older invocation prompt"}); err != nil {
		t.Fatal(err)
	}
	if err := newer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "forced newer submit"}); err != nil {
		t.Fatal(err)
	}
	// The older invocation finishes after the new prompt and receives a task
	// notification encoded as a user message. Its checkpoint still belongs to
	// its own first prompt, never the session's latest user message.
	if err := older.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "older invocation answer"}); err != nil {
		t.Fatal(err)
	}
	if err := older.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "later task notification"}); err != nil {
		t.Fatal(err)
	}
	if older.FirstUserSeq() != 3 || newer.FirstUserSeq() != 4 {
		t.Fatalf("invocation sequences changed: older=%d, newer=%d", older.FirstUserSeq(), newer.FirstUserSeq())
	}
	for _, test := range []struct {
		writer  *Writer
		content string
	}{{older, "older invocation prompt"}, {newer, "forced newer submit"}} {
		row, err := s.ReadMessageAt(ctx, sess.ID, test.writer.FirstUserSeq())
		if err != nil || row.Role != "user" || row.Content != test.content {
			t.Fatalf("Writer sequence does not identify its own prompt: row=%+v, error=%v", row, err)
		}
	}
	latest, err := s.LatestMessageSeq(ctx, sess.ID, "user")
	if err != nil || latest != 6 || latest == older.FirstUserSeq() {
		t.Fatalf("fixture did not distinguish latest user from invocation prompt: latest=%d, error=%v", latest, err)
	}
}

func TestWriterFirstUserSeqConcurrentWritersRemainIndependent(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "fixture", "concurrent writers")
	if err != nil {
		t.Fatal(err)
	}
	writers := []*Writer{NewWriter(s, sess.ID), NewWriter(s, sess.ID)}
	start := make(chan struct{})
	finished := make(chan error, len(writers))
	for i, writer := range writers {
		go func(index int, w *Writer) {
			<-start
			finished <- w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt-%d", index)})
		}(i, writer)
	}
	close(start)
	for range writers {
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if writers[0].FirstUserSeq() == writers[1].FirstUserSeq() {
		t.Fatal("independent Writer invocations adopted the same insert")
	}
	for i, writer := range writers {
		seq := writer.FirstUserSeq()
		row, err := s.ReadMessageAt(context.Background(), sess.ID, seq)
		if seq < 1 || seq > 2 || err != nil || row.Content != fmt.Sprintf("prompt-%d", i) {
			t.Fatalf("Writer %d claimed another insert: seq=%d, row=%+v, error=%v", i, seq, row, err)
		}
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "later notification"}); err != nil {
			t.Fatal(err)
		}
		if writer.FirstUserSeq() != seq {
			t.Fatalf("Writer %d replaced its first sequence", i)
		}
	}
}

func TestWriterFirstUserSeqConcurrentSameWriterKeepsEarliestInsert(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "fixture", "same writer")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	if err := w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleAssistant, Content: "assistant does not establish user sequence"}); err != nil {
		t.Fatal(err)
	}
	if w.FirstUserSeq() != 0 {
		t.Fatal("assistant message established FirstUserSeq")
	}
	const count = 12
	start := make(chan struct{})
	finished := make(chan error, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_ = w.FirstUserSeq() // Concurrent reads must be safe under -race.
			err := w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("user-%d", index)})
			_ = w.FirstUserSeq()
			finished <- err
		}(i)
	}
	close(start)
	workers.Wait()
	close(finished)
	for err := range finished {
		if err != nil {
			t.Error(err)
		}
	}
	if w.FirstUserSeq() != 2 {
		t.Fatalf("concurrent user insert published a later sequence: %d", w.FirstUserSeq())
	}
	rows, err := s.ReadMessages(context.Background(), sess.ID)
	if err != nil || len(rows) != count+1 || rows[1].Role != "user" {
		t.Fatalf("concurrent messages lost: count=%d, error=%v", len(rows), err)
	}
}

func TestWriterFirstUserSeqIsPublishedOnlyForSuccessfulUserAppend(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "fixture", "failed insert")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	if err := w.AppendMessage(context.Background(), llm.Message{}); err == nil {
		t.Fatal("invalid message was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error lost: %v", err)
	}
	// The UPDATE succeeds, then the exact INSERT fails. The transaction must
	// roll back message_count and FirstUserSeq must remain unpublished.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_sequence_fixture BEFORE INSERT ON messages
		WHEN NEW.content='rejected' BEGIN SELECT RAISE(ABORT, 'synthetic insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "rejected"}); err == nil || !strings.Contains(err.Error(), "append: insert:") {
		t.Fatalf("insert error changed: %v", err)
	}
	seq, err := s.AppendMessageWithSeq(context.Background(), sess.ID, Encoded{Role: "user", Content: "rejected"})
	if err == nil || seq != 0 {
		t.Fatalf("failed insert published sequence %d: %v", seq, err)
	}
	if w.FirstUserSeq() != 0 {
		t.Fatal("failed user append established FirstUserSeq")
	}
	meta, err := s.Get(sess.ID)
	if err != nil || meta.MessageCount != 0 {
		t.Fatalf("failed insert did not roll back metadata: count=%d, error=%v", meta.MessageCount, err)
	}
	if err := w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "first successful prompt"}); err != nil {
		t.Fatal(err)
	}
	if w.FirstUserSeq() != 1 {
		t.Fatalf("failed writes consumed the first sequence: %d", w.FirstUserSeq())
	}
	if err := w.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "rejected"}); err == nil {
		t.Fatal("late rejected notification was accepted")
	}
	if w.FirstUserSeq() != 1 {
		t.Fatal("late failed notification changed FirstUserSeq")
	}
}
