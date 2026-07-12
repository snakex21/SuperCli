package session

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

func TestContextProjectionKeepsTranscriptAndAppendsTail(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "m", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"old secret", "visible now"} {
		enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: text})
		if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
			t.Fatal(err)
		}
	}
	projection := []llm.Message{{Role: llm.RoleUser, Content: "[earlier context cleared — 1 message(s) compacted]"}, {Role: llm.RoleUser, Content: "visible now"}}
	if err := s.SaveContextProjection(context.Background(), sess.ID, projection); err != nil {
		t.Fatal(err)
	}
	enc, _ := FromMessage(llm.Message{Role: llm.RoleAssistant, Content: "new tail"})
	if err := s.AppendMessage(context.Background(), sess.ID, enc); err != nil {
		t.Fatal(err)
	}

	full, _ := s.ReadMessages(context.Background(), sess.ID)
	if len(full) != 3 || full[0].Content != "old secret" {
		t.Fatalf("full transcript changed: %+v", full)
	}
	model, err := s.ReadModelContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(model) != 3 || model[0].Content == "old secret" || model[2].Content != "new tail" {
		t.Fatalf("model context = %+v", model)
	}
}

func TestContextProjectionCorruptFallsBackToTranscript(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create(t.TempDir(), "m", "")
	enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: "survives"})
	_ = s.AppendMessage(context.Background(), sess.ID, enc)
	_, err := s.db.Exec(`INSERT INTO session_context_projections(session_id, through_seq, messages_json, updated_at) VALUES(?,?,?,?)`, sess.ID, 1, []byte("{"), 1)
	if err != nil {
		t.Fatal(err)
	}
	model, err := s.ReadModelContext(context.Background(), sess.ID)
	if err != nil || len(model) != 1 || model[0].Content != "survives" {
		t.Fatalf("fallback = %+v, %v", model, err)
	}
}

func TestContextProjectionPreservesCompactedOrder(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create(t.TempDir(), "m", "")
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "old"},
		{Role: llm.RoleAssistant, Content: "old answer"},
		{Role: llm.RoleUser, Content: "recent"},
		// Compaction summaries are appended to the transcript but moved
		// before the recent tail in the model projection.
		{Role: llm.RoleUser, Content: "summary"},
	} {
		enc, _ := FromMessage(m)
		_ = s.AppendMessage(context.Background(), sess.ID, enc)
	}
	if err := s.SaveContextProjection(context.Background(), sess.ID, []llm.Message{
		{Role: llm.RoleUser, Content: "summary"},
		{Role: llm.RoleUser, Content: "recent"},
	}); err != nil {
		t.Fatal(err)
	}
	enc, _ := FromMessage(llm.Message{Role: llm.RoleAssistant, Content: "continued"})
	_ = s.AppendMessage(context.Background(), sess.ID, enc)
	got, err := s.ReadModelContext(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "summary" || got[1].Content != "recent" || got[2].Content != "continued" {
		t.Fatalf("compacted context = %+v", got)
	}
}
