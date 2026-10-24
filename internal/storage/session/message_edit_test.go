package session

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"supercli/internal/llm"
)

func editFixture(t *testing.T, msgs ...llm.Message) (*Store, string) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sess, err := s.Create(t.TempDir(), "m", "edit")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	for _, m := range msgs {
		if err := w.AppendMessage(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	return s, sess.ID
}

func editReceipt(t *testing.T, s *Store, id string, seq int) MessageReceipt {
	t.Helper()
	m, err := s.ReadTranscriptMessageAt(context.Background(), id, seq)
	if err != nil {
		t.Fatal(err)
	}
	return MessageReceipt{Seq: seq, ID: m.MessageID}
}

func modelTexts(t *testing.T, s *Store, id string) []string {
	t.Helper()
	msgs, err := s.ReadModelContext(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		out = append(out, m.TextOnly().Content)
	}
	return out
}

func rawRow(t *testing.T, s *Store, id string, seq int) Encoded {
	t.Helper()
	m, err := s.ReadMessageAt(context.Background(), id, seq)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var reasoningAnswer = llm.Message{Role: llm.RoleAssistant, Origin: &llm.ContextOrigin{Source: llm.SourceModel}, Parts: []llm.ContentPart{
	{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "model", Scope: "scope", Data: []byte(`{"reasoning_content":"private"}`)}},
	{Type: llm.PartTypeText, Text: "original answer"},
}, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"a"}`}}}

func TestEditDisplayOnlyKeepsModelContextAndRestoresExactly(t *testing.T) {
	ctx := context.Background()
	s, id := editFixture(t, llm.Message{Role: llm.RoleUser, Content: "q"}, reasoningAnswer)
	before := rawRow(t, s, id, 2)
	wantModel := modelTexts(t, s, id)
	r := editReceipt(t, s, id, 2)
	res, err := s.EditAssistantMessage(ctx, id, r, "edited answer", false)
	if err != nil || !res.Edited || res.ModelVisible || res.Content != "edited answer" {
		t.Fatalf("edit res=%+v err=%v", res, err)
	}
	if got := modelTexts(t, s, id); !equalStrings(got, wantModel) {
		t.Fatalf("display-only edit changed model context: %v", got)
	}
	edits, err := s.ReadMessageEdits(ctx, id, 1, 2)
	if err != nil || len(edits) != 1 {
		t.Fatalf("edits=%v err=%v", edits, err)
	}
	e := edits[r.ID]
	if e.OriginalPartsJSON != before.PartsJSON || e.OriginalContent != before.Content {
		t.Fatalf("original not kept exactly: %+v", e)
	}
	edited, _ := Encoded{Role: "assistant", PartsJSON: e.EditedPartsJSON, Content: e.EditedContent, ToolCallsJSON: before.ToolCallsJSON}.ToMessage()
	if len(edited.Parts) != 2 || edited.Parts[0].Type != llm.PartTypeReasoning || edited.Parts[1].Text != "edited answer" {
		t.Fatalf("edit must keep reasoning parts: %+v", edited.Parts)
	}
	if _, err := s.RestoreAssistantMessage(ctx, id, r); err != nil {
		t.Fatal(err)
	}
	if after := rawRow(t, s, id, 2); after != before {
		t.Fatalf("restore not lossless:\n%+v\n%+v", before, after)
	}
	if edits, _ := s.ReadMessageEdits(ctx, id, 1, 2); len(edits) != 0 {
		t.Fatalf("restore kept edit: %v", edits)
	}
}

func TestEditModelVisibleTailAndRestore(t *testing.T) {
	ctx := context.Background()
	s, id := editFixture(t, llm.Message{Role: llm.RoleUser, Content: "q"}, reasoningAnswer)
	before := rawRow(t, s, id, 2)
	r := editReceipt(t, s, id, 2)
	res, err := s.EditAssistantMessage(ctx, id, r, "model sees this", true)
	if err != nil || !res.ModelVisible || res.Summarized {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := modelTexts(t, s, id); !equalStrings(got, []string{"q", "model sees this"}) {
		t.Fatalf("model context=%v", got)
	}
	msgs, _ := s.ReadModelContext(ctx, id)
	if len(msgs[1].ToolCalls) != 1 || msgs[1].Parts[0].Type != llm.PartTypeReasoning {
		t.Fatalf("tool calls/reasoning lost: %+v", msgs[1])
	}
	// Switching an existing edit to display-only puts the original back for the model.
	if _, err := s.EditAssistantMessage(ctx, id, r, "display only now", false); err != nil {
		t.Fatal(err)
	}
	if got := modelTexts(t, s, id); !equalStrings(got, []string{"q", "original answer"}) {
		t.Fatalf("display-only switch: %v", got)
	}
	if _, err := s.EditAssistantMessage(ctx, id, r, "visible again", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreAssistantMessage(ctx, id, r); err != nil {
		t.Fatal(err)
	}
	if after := rawRow(t, s, id, 2); after != before {
		t.Fatalf("restore not lossless:\n%+v\n%+v", before, after)
	}
}

func TestEditModelVisibleInsideProjectionAndSummarized(t *testing.T) {
	ctx := context.Background()
	s, id := editFixture(t,
		llm.Message{Role: llm.RoleUser, Content: "q1"},
		llm.Message{Role: llm.RoleAssistant, Content: "same"},
		llm.Message{Role: llm.RoleUser, Content: "q2"},
		llm.Message{Role: llm.RoleAssistant, Content: "same"},
		llm.Message{Role: llm.RoleUser, Content: "q3"},
		llm.Message{Role: llm.RoleAssistant, Content: "dropped"},
	)
	// The projection keeps both "same" replies but summarized "dropped" away.
	if err := s.SaveContextProjection(ctx, id, []llm.Message{
		{Role: llm.RoleUser, Content: "q1"}, {Role: llm.RoleAssistant, Content: "same"},
		{Role: llm.RoleUser, Content: "q2"}, {Role: llm.RoleAssistant, Content: "same"},
		{Role: llm.RoleUser, Content: "summary of q3"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := NewWriter(s, id).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "tail"}); err != nil {
		t.Fatal(err)
	}
	r2 := editReceipt(t, s, id, 2)
	res, err := s.EditAssistantMessage(ctx, id, r2, "first edited", true)
	if err != nil || !res.ModelVisible {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := []string{"q1", "first edited", "q2", "same", "summary of q3", "tail"}
	if got := modelTexts(t, s, id); !equalStrings(got, want) {
		t.Fatalf("projection edit hit wrong message: %v", got)
	}
	r6 := editReceipt(t, s, id, 6)
	res, err = s.EditAssistantMessage(ctx, id, r6, "too late", true)
	if err != nil || res.ModelVisible || !res.Summarized || !res.Edited {
		t.Fatalf("summarized res=%+v err=%v", res, err)
	}
	if got := modelTexts(t, s, id); !equalStrings(got, want) {
		t.Fatalf("summarized edit changed context: %v", got)
	}
	if rawRow(t, s, id, 6).Content != "dropped" {
		t.Fatal("summarized message row must keep the original")
	}
	if _, err := s.RestoreAssistantMessage(ctx, id, r2); err != nil {
		t.Fatal(err)
	}
	if got := modelTexts(t, s, id); !equalStrings(got, []string{"q1", "same", "q2", "same", "summary of q3", "tail"}) {
		t.Fatalf("projection restore: %v", got)
	}
}

func TestEditRejectsNonAssistantAndStaleAndRewindRemoves(t *testing.T) {
	ctx := context.Background()
	s, id := editFixture(t, llm.Message{Role: llm.RoleUser, Content: "q"}, llm.Message{Role: llm.RoleAssistant, Content: "a"})
	if _, err := s.EditAssistantMessage(ctx, id, editReceipt(t, s, id, 1), "x", false); !errors.Is(err, ErrNotAssistantMessage) {
		t.Fatalf("user edit err=%v", err)
	}
	r := editReceipt(t, s, id, 2)
	if _, err := s.EditAssistantMessage(ctx, id, MessageReceipt{Seq: 2, ID: r.ID + 100}, "x", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale err=%v", err)
	}
	if _, err := s.EditAssistantMessage(ctx, id, r, "edited", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TruncateFrom(ctx, id, 2); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM message_edits`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rewind kept %d edits err=%v", n, err)
	}
	if err := NewWriter(s, id).AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EditAssistantMessage(ctx, id, r, "x", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old receipt after rewind err=%v", err)
	}
}
