package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

func TestLegacySummaryIsNotAUserCardAndDoesNotBreakPaging(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo", "legacy summary")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	summary := agent.WrapCompactSummary("Goal: complete the existing task.")
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "original question"},
		{Role: llm.RoleAssistant, Content: "original answer"},
		{Role: llm.RoleUser, Content: summary},
	}
	for _, msg := range messages {
		if err := writer.AppendMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.SaveContextProjection(ctx, []llm.Message{messages[2]}); err != nil {
		t.Fatal(err)
	}
	last, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Messages) != 0 || !last.HasMore || last.BeforeSeq != 3 {
		t.Fatalf("summary-only page cannot load older history: %+v", last)
	}
	older, err := srv.eng.transcriptPage(ctx, sess.ID, last.BeforeSeq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if older.HasMore || older.BeforeSeq != 1 || len(older.Messages) != 2 || older.Messages[0].Content != "original question" || older.Messages[1].Content != "original answer" {
		t.Fatalf("original transcript lost: %+v", older)
	}
	full, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil || len(full) != 2 {
		t.Fatalf("full transcript=%+v err=%v", full, err)
	}
	model, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil || len(model) != 1 || model[0].Content != summary {
		t.Fatalf("UI filtering changed model context: %+v err=%v", model, err)
	}
	// A question quoting the beginning of the generated text remains a user card.
	quoted := llm.Message{Role: llm.RoleUser, Content: strings.Split(summary, "Goal:")[0] + "Why is this in my conversation?"}
	if err := writer.AppendMessage(ctx, quoted); err != nil {
		t.Fatal(err)
	}
	full, err = srv.eng.transcript(ctx, sess.ID)
	if err != nil || len(full) != 3 || full[2].Role != "user" || full[2].Content != quoted.Content {
		t.Fatalf("real user message hidden: %+v err=%v", full, err)
	}
}

func TestRewindRejectsInternalSummaryWithoutChangingHistory(t *testing.T) {
	srv := newTestServer(t, false)
	source, store := createRewindSession(t, srv, "legacy summary rewind")
	ctx := context.Background()
	summary := llm.Message{Role: llm.RoleUser, Content: agent.WrapCompactSummary("Goal: keep the existing work.")}
	writer := session.NewWriter(store, source.ID)
	if err := writer.AppendMessage(ctx, summary); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveContextProjection(ctx, []llm.Message{summary}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadMessages(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("{\"session_id\":%q,\"selected_seq\":5}", source.ID)
	rec := httptest.NewRecorder()
	srv.handleSessionRewind(rec, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("summary accepted as user rewind: %d %s", rec.Code, rec.Body.String())
	}
	after, err := store.ReadMessages(ctx, source.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected rewind changed transcript: %+v err=%v", after, err)
	}
	projected, err := store.ReadModelContext(ctx, source.ID)
	if err != nil || len(projected) != 1 || projected[0].Content != summary.Content {
		t.Fatalf("rejected rewind changed context: %+v err=%v", projected, err)
	}
}
