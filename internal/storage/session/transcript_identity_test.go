package session

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestTranscriptIdentityAndExactTruncateRejectReusedSequence(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	other, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	ctx := context.Background()
	old, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, sess.ID, Encoded{Role: "assistant", Content: "old answer"}); err != nil {
		t.Fatal(err)
	}
	page, more, err := s.ReadTranscriptMessagesBefore(ctx, sess.ID, 0, 1)
	if err != nil || !more || len(page) != 1 || page[0].Role != "assistant" || page[0].MessageID <= old.ID {
		t.Fatalf("identity page=%+v more=%v err=%v", page, more, err)
	}
	retained, err := s.ReadTranscriptMessages(ctx, sess.ID)
	if err != nil || len(retained) != 2 || retained[0].MessageID != old.ID {
		t.Fatalf("retained transcript=%+v err=%v", retained, err)
	}
	if n, err := other.TruncateFromExact(ctx, sess.ID, old); err != nil || n != 2 {
		t.Fatalf("initial truncate=%d err=%v", n, err)
	}
	replacement, err := other.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "replacement"})
	if err != nil || replacement.Seq != old.Seq || replacement.ID <= old.ID {
		t.Fatalf("replacement=%+v old=%+v err=%v", replacement, old, err)
	}
	if retained[0].MessageID != old.ID || retained[0].Content != "old" {
		t.Fatal("read result relinked payload to a later row")
	}
	current, err := s.ReadUserReceiptAt(ctx, sess.ID, old.Seq)
	if err != nil || current != replacement {
		t.Fatalf("metadata receipt=%+v err=%v", current, err)
	}
	if _, err := s.ReadUserReceiptAt(ctx, sess.ID, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing receipt=%v", err)
	}
	if err := s.SaveContextProjection(ctx, sess.ID, []llm.Message{{Role: llm.RoleUser, Content: "projected"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMessageAttachments(ctx, sess.ID, replacement.Seq, []string{"synthetic.png"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []MessageReceipt{old, {Seq: replacement.Seq, ID: 0}, {Seq: replacement.Seq + 1, ID: replacement.ID}} {
		if n, err := s.TruncateFromExact(ctx, sess.ID, invalid); n != 0 || !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("stale truncate=%d err=%v", n, err)
		}
	}
	after, err := other.ReadMessages(ctx, sess.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale request changed replacement transcript: err=%v", err)
	}
	attachments, err := other.ReadMessageAttachmentsRange(ctx, sess.ID, 0, 0)
	if err != nil || len(attachments[replacement.Seq]) != 1 {
		t.Fatalf("stale request changed attachments: %+v err=%v", attachments, err)
	}
	var projections int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_context_projections WHERE session_id = ?`, sess.ID).Scan(&projections); err != nil || projections != 1 {
		t.Fatalf("stale request changed projection: %d err=%v", projections, err)
	}
}

func TestExactTruncateRollsBackFailureAndRejectsAssistantReceipt(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	ctx := context.Background()
	user, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "user", Content: "kept"})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := s.AppendMessageWithReceipt(ctx, sess.ID, Encoded{Role: "assistant", Content: "kept answer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadUserReceiptAt(ctx, sess.ID, assistant.Seq); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("assistant read as user receipt: %v", err)
	}
	if n, err := s.TruncateFromExact(ctx, sess.ID, assistant); n != 0 || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("assistant truncated=%d err=%v", n, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_exact_truncate BEFORE DELETE ON messages BEGIN SELECT RAISE(ABORT, 'synthetic truncate failure'); END`); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, attempt := range []context.Context{canceled, ctx} {
		if n, err := s.TruncateFromExact(attempt, sess.ID, user); err == nil || n != 0 {
			t.Fatalf("failed truncate=%d err=%v", n, err)
		}
		rows, err := s.ReadMessages(ctx, sess.ID)
		meta, metaErr := s.Get(sess.ID)
		if err != nil || metaErr != nil || len(rows) != 2 || meta.MessageCount != 2 {
			t.Fatalf("failed truncate changed rows/count: rows=%d count=%d err=%v/%v", len(rows), meta.MessageCount, err, metaErr)
		}
	}
}

func TestTranscriptIdentityPaginationMatchesExistingPayloadContract(t *testing.T) {
	s := openTestStore(t)
	sess := receiptSession(t, s)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		if err := s.AppendMessage(ctx, sess.ID, Encoded{Role: role, Content: "synthetic"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range []struct{ before, limit int }{{0, 2}, {5, 2}, {3, 2}, {1, 2}, {0, 0}, {0, 1000}} {
		old, oldMore, err := s.ReadMessagesBefore(ctx, sess.ID, page.before, page.limit)
		if err != nil {
			t.Fatal(err)
		}
		identified, more, err := s.ReadTranscriptMessagesBefore(ctx, sess.ID, page.before, page.limit)
		if err != nil || more != oldMore || len(identified) != len(old) {
			t.Fatalf("page=%+v count=%d/%d more=%v/%v err=%v", page, len(identified), len(old), more, oldMore, err)
		}
		for i, row := range identified {
			if row.MessageID <= 0 || row.Encoded != old[i] {
				t.Fatalf("page payload/identity diverged: row=%+v expected=%+v", row, old[i])
			}
		}
	}
}

func BenchmarkTranscriptIdentityPage(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	sess, err := s.Create(b.TempDir(), "fixture", "identity page")
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 120; i++ {
		if err := s.AppendMessage(ctx, sess.ID, Encoded{Role: "user", Content: strings.Repeat("synthetic text ", 128)}); err != nil {
			b.Fatal(err)
		}
	}
	for _, name := range []string{"payload_only", "payload_and_identity"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if name == "payload_only" {
					if rows, more, err := s.ReadMessagesBefore(ctx, sess.ID, 0, 100); err != nil || !more || len(rows) != 100 {
						b.Fatalf("payload page rows=%d more=%v err=%v", len(rows), more, err)
					}
				} else if rows, more, err := s.ReadTranscriptMessagesBefore(ctx, sess.ID, 0, 100); err != nil || !more || len(rows) != 100 {
					b.Fatalf("identity page rows=%d more=%v err=%v", len(rows), more, err)
				}
			}
		})
	}
}
