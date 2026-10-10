package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"supercli/internal/llm"
)

func receiptSession(t *testing.T, s *Store) Session {
	t.Helper()
	sess, err := s.Create(t.TempDir(), "fixture", "receipt fixture")
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func assertUserReceipt(t *testing.T, s *Store, sid string, receipt MessageReceipt, want bool) {
	t.Helper()
	current, err := s.IsCurrentUserReceipt(context.Background(), sid, receipt)
	if err != nil || current != want {
		t.Fatalf("receipt %+v current=%v, want %v, error=%v", receipt, current, want, err)
	}
}

func TestAppendMessageWithReceiptReturnsExactCommittedRow(t *testing.T) {
	s := openTestStore(t)
	first, second := receiptSession(t, s), receiptSession(t, s)
	ctx := context.Background()
	for _, test := range []struct {
		sid     string
		role    string
		content string
		seq     int
	}{
		{first.ID, "user", "first prompt", 1},
		{second.ID, "user", "other prompt", 1},
		{first.ID, "assistant", "first answer", 2},
	} {
		receipt, err := s.AppendMessageWithReceipt(ctx, test.sid, Encoded{Seq: 999, Role: test.role, Content: test.content})
		if err != nil || receipt.Seq != test.seq || receipt.ID <= 0 {
			t.Fatalf("append receipt=%+v, want seq=%d and positive ID, error=%v", receipt, test.seq, err)
		}
		var sid, role, content string
		var seq int
		err = s.db.QueryRowContext(ctx, `SELECT session_id, seq, role, content FROM messages WHERE id = ?`, receipt.ID).Scan(&sid, &seq, &role, &content)
		if err != nil || sid != test.sid || seq != receipt.Seq || role != test.role || content != test.content {
			t.Fatalf("receipt identified another row: sid=%q seq=%d role=%q content=%q, error=%v", sid, seq, role, content, err)
		}
		assertUserReceipt(t, s, test.sid, receipt, test.role == "user")
		assertUserReceipt(t, s, test.sid, MessageReceipt{Seq: receipt.Seq + 1, ID: receipt.ID}, false)
		if test.sid == first.ID {
			assertUserReceipt(t, s, second.ID, receipt, false)
		}
	}
	seq, err := s.AppendMessageWithSeq(ctx, first.ID, Encoded{Role: "user", Content: "sequence wrapper"})
	if err != nil || seq != 3 {
		t.Fatalf("sequence wrapper changed: seq=%d, error=%v", seq, err)
	}
	if err := s.AppendMessage(ctx, first.ID, Encoded{Role: "assistant", Content: "error-only wrapper"}); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Get(first.ID)
	if err != nil || meta.MessageCount != 4 {
		t.Fatalf("append metadata changed: count=%d, error=%v", meta.MessageCount, err)
	}
}

func TestUserMessageReceiptRejectsReusedSequenceAcrossStores(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ctx := context.Background()
	oldWriter := NewWriter(s, sess.ID)
	if err := oldWriter.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "discarded prompt"}); err != nil {
		t.Fatal(err)
	}
	old := oldWriter.FirstUserReceipt()
	assertUserReceipt(t, other, sess.ID, old, true)
	if _, err := other.TruncateFrom(ctx, sess.ID, old.Seq); err != nil {
		t.Fatal(err)
	}
	assertUserReceipt(t, s, sess.ID, old, false)
	newWriter := NewWriter(other, sess.ID)
	if newWriter.FirstUserReceipt() != (MessageReceipt{}) {
		t.Fatal("fresh invocation adopted a historical receipt")
	}
	if err := newWriter.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "replacement prompt"}); err != nil {
		t.Fatal(err)
	}
	replacement := newWriter.FirstUserReceipt()
	if replacement.Seq != old.Seq || replacement.ID <= old.ID {
		t.Fatalf("fixture did not reuse Seq with a new AUTOINCREMENT ID: old=%+v new=%+v", old, replacement)
	}
	assertUserReceipt(t, s, sess.ID, old, false)
	assertUserReceipt(t, s, sess.ID, replacement, true)
	// The old owner's late user-role notification must not repair its receipt
	// to the current sequence or adopt the replacement invocation's prompt.
	if err := oldWriter.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "late notification"}); err != nil {
		t.Fatal(err)
	}
	if oldWriter.FirstUserReceipt() != old || oldWriter.FirstUserSeq() != old.Seq {
		t.Fatal("old Writer relinked after a sequence was reused")
	}
	copy := newWriter.FirstUserReceipt()
	copy.Seq++
	copy.ID++
	if newWriter.FirstUserReceipt() != replacement {
		t.Fatal("mutating a returned value changed the Writer's immutable receipt")
	}
	assertUserReceipt(t, other, sess.ID, old, false)
}

func TestUserMessageReceiptRejectsDeletedAndRecreatedSession(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	ctx := context.Background()
	old, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "original session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	// Restore the same logical SID explicitly in the synthetic database. The
	// physical user ID must still distinguish the deleted session's owner.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, cwd, title, model, created_at, updated_at) VALUES(?, ?, '', 'fixture', 1, 1)`, sess.ID, sess.Cwd); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "new session"})
	if err != nil || replacement.Seq != old.Seq || replacement.ID <= old.ID {
		t.Fatalf("SID/Seq recreation fixture failed: old=%+v new=%+v, error=%v", old, replacement, err)
	}
	assertUserReceipt(t, s, sess.ID, old, false)
	assertUserReceipt(t, s, sess.ID, replacement, true)
}

func TestReceiptIsUnpublishedAfterValidationInsertAndCommitFailure(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TRIGGER reject_receipt_insert BEFORE INSERT ON messages WHEN NEW.content='insert-rejected' BEGIN SELECT RAISE(ABORT, 'synthetic insert failure'); END`,
		`CREATE TABLE receipt_commit_parent(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE receipt_commit_guard(parent_id INTEGER REFERENCES receipt_commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER reject_receipt_commit AFTER INSERT ON messages WHEN NEW.content='commit-rejected' BEGIN INSERT INTO receipt_commit_guard(parent_id) VALUES(99); END`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, test := range []struct {
		name    string
		ctx     context.Context
		message Encoded
	}{
		{"validation", ctx, Encoded{}},
		{"canceled", canceled, Encoded{Role: "user", Content: "canceled"}},
		{"insert", ctx, Encoded{Role: "user", Content: "insert-rejected"}},
		{"commit", ctx, Encoded{Role: "user", Content: "commit-rejected"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			receipt, err := s.AppendMessageWithReceipt(test.ctx, sess.ID, test.message)
			if err == nil || receipt != (MessageReceipt{}) {
				t.Fatalf("failed append published receipt=%+v, error=%v", receipt, err)
			}
			if test.name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
		})
	}
	w := NewWriter(s, sess.ID)
	for _, content := range []string{"insert-rejected", "commit-rejected"} {
		if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: content}); err == nil {
			t.Fatalf("Writer accepted %s fixture", content)
		}
		if w.FirstUserReceipt() != (MessageReceipt{}) || w.FirstUserSeq() != 0 {
			t.Fatal("failed Writer append published an identity")
		}
	}
	meta, err := s.Get(sess.ID)
	if err != nil || meta.MessageCount != 0 {
		t.Fatalf("failed append did not roll back message count: count=%d, error=%v", meta.MessageCount, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, sess.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed commit left messages: count=%d, error=%v", count, err)
	}
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "committed prompt"}); err != nil {
		t.Fatal(err)
	}
	committed := w.FirstUserReceipt()
	if committed.Seq != 1 || committed.ID <= 0 {
		t.Fatalf("failed appends consumed the first sequence: %+v", committed)
	}
	assertUserReceipt(t, s, sess.ID, committed, true)
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "commit-rejected"}); err == nil || w.FirstUserReceipt() != committed {
		t.Fatalf("later failed append changed the receipt: got=%+v, error=%v", w.FirstUserReceipt(), err)
	}
}

func TestWriterConcurrentReceiptIsAnImmutablePair(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	w := NewWriter(s, sess.ID)
	ctx := context.Background()
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "assistant has no user receipt"}); err != nil {
		t.Fatal(err)
	}
	if w.FirstUserReceipt() != (MessageReceipt{}) {
		t.Fatal("assistant published the user receipt")
	}
	const count = 12
	start := make(chan struct{})
	finished := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(index int) {
			<-start
			before := w.FirstUserReceipt()
			if (before.Seq == 0) != (before.ID == 0) {
				finished <- fmt.Errorf("partially published receipt before append: %+v", before)
				return
			}
			err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt-%d", index)})
			after := w.FirstUserReceipt()
			if err == nil && (after.Seq != 2 || after.ID <= 0) {
				err = fmt.Errorf("wrong first user receipt after append: %+v", after)
			}
			finished <- err
		}(i)
	}
	close(start)
	for i := 0; i < count; i++ {
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	receipt := w.FirstUserReceipt()
	assertUserReceipt(t, s, sess.ID, receipt, true)
	if w.FirstUserSeq() != receipt.Seq {
		t.Fatal("legacy sequence wrapper differs from the atomic receipt")
	}
	newer := NewWriter(s, sess.ID)
	if err := newer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "another invocation"}); err != nil {
		t.Fatal(err)
	}
	if newer.FirstUserReceipt().ID == receipt.ID || newer.FirstUserReceipt().Seq != count+2 {
		t.Fatal("another invocation adopted the shared Writer's receipt")
	}
	assertUserReceipt(t, s, sess.ID, newer.FirstUserReceipt(), true)
}

func TestCurrentUserReceiptDistinguishesInvalidArgumentsAndSQLError(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	ctx := context.Background()
	receipt, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "fixture prompt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []MessageReceipt{{}, {Seq: receipt.Seq}, {ID: receipt.ID}, {Seq: -1, ID: receipt.ID}} {
		if current, err := s.IsCurrentUserReceipt(ctx, sess.ID, invalid); current || err == nil {
			t.Fatalf("invalid receipt became a missing message: current=%v error=%v", current, err)
		}
	}
	if current, err := s.IsCurrentUserReceipt(ctx, " ", receipt); current || err == nil {
		t.Fatal("blank session was treated as a deleted message")
	}
	var nilStore *Store
	if current, err := nilStore.IsCurrentUserReceipt(ctx, sess.ID, receipt); current || err == nil {
		t.Fatal("nil Store was treated as a deleted message")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if current, err := s.IsCurrentUserReceipt(canceled, sess.ID, receipt); current || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled identity query became deletion: current=%v error=%v", current, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if current, err := s.IsCurrentUserReceipt(ctx, sess.ID, receipt); current || err == nil {
		t.Fatalf("SQL error became deletion: current=%v error=%v", current, err)
	}
}
