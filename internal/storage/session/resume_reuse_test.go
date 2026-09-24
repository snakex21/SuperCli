package session

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestResumeReusesTranscriptWithProjectionTail(t *testing.T) {
	ctx := context.Background()
	store, _ := OpenStore(t.TempDir())
	defer store.Close()
	sess, _ := store.Create(t.TempDir(), "model", "test")
	w := NewWriter(store, sess.ID)
	w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "old"})
	w.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "old answer"})
	summary := []llm.Message{{Role: llm.RoleUser, Content: "saved compact summary"}}
	if err := store.SaveContextProjection(ctx, sess.ID, summary); err != nil {
		t.Fatal(err)
	}
	w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "new tail"})
	rows, _ := store.ReadMessages(ctx, sess.ID)
	var messages []llm.Message
	var seqs []int
	for _, row := range rows {
		m, _ := row.ToMessage()
		messages = append(messages, m)
		seqs = append(seqs, row.Seq)
	}
	expected, _ := store.ReadModelContext(ctx, sess.ID)
	actual, err := store.ModelContextFromTranscript(ctx, sess.ID, messages, seqs)
	if err != nil || !reflect.DeepEqual(expected, actual) {
		t.Fatalf("projection/tail changed: %v %#v", err, actual)
	}
	if len(messages) != 3 || messages[0].Content != "old" {
		t.Fatal("UI transcript modified")
	}
	store.db.Exec("UPDATE session_context_projections SET messages_json='broken' WHERE session_id=?", sess.ID)
	actual, err = store.ModelContextFromTranscript(ctx, sess.ID, messages, seqs)
	if err != nil || !reflect.DeepEqual(messages, actual) {
		t.Fatal("corrupt projection lost transcript")
	}
}

func BenchmarkResumeTranscriptReuse(b *testing.B) {
	ctx := context.Background()
	store, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	sess, _ := store.Create(b.TempDir(), "model", "benchmark")
	w := NewWriter(store, sess.ID)
	for i := 0; i < 400; i++ {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		w.AppendMessage(ctx, llm.Message{Role: role, Content: strings.Repeat("code context ", 160)})
	}
	for _, reuse := range []bool{false, true} {
		name := "double_read"
		if reuse {
			name = "reuse_decoded"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rows, err := store.ReadMessages(ctx, sess.ID)
				if err != nil {
					b.Fatal(err)
				}
				msgs := make([]llm.Message, 0, len(rows))
				seqs := make([]int, 0, len(rows))
				for _, row := range rows {
					m, err := row.ToMessage()
					if err != nil {
						b.Fatal(err)
					}
					msgs = append(msgs, m)
					seqs = append(seqs, row.Seq)
				}
				if reuse {
					_, err = store.ModelContextFromTranscript(ctx, sess.ID, msgs, seqs)
				} else {
					_, err = store.ReadModelContext(ctx, sess.ID)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestLegacyUsageRemainsVisibleWhenDetailedCallsBegin(t *testing.T) {
	ctx := context.Background()
	s, _ := OpenStore(t.TempDir())
	defer s.Close()
	sess, _ := s.Create(t.TempDir(), "old", "legacy")
	if err := s.UpdateUsage(sess.ID, 1000, 100); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.PreserveLegacyUsage(ctx, sess.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendUsage(ctx, UsageRecord{SessionID: sess.ID, Model: "new", Input: 200, Output: 20}); err != nil {
		t.Fatal(err)
	}
	if err := s.PreserveLegacyUsage(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadUsage(ctx, sess.ID)
	if err != nil || len(rows) != 2 || rows[0].Source != "legacy" || rows[0].Input+rows[1].Input != 1200 {
		t.Fatalf("legacy totals lost or duplicated: %v %#v", err, rows)
	}
}
