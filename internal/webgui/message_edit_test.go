package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postMessageEdit(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/session/message/edit", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.handleSessionMessageEdit(rec, r)
	return rec
}

func TestSessionMessageEditEndpoint(t *testing.T) {
	srv := newTestServer(t, false)
	sess, _ := createRewindSession(t, srv, "edit")
	ctx := context.Background()
	page, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if err != nil || len(page.Messages) != 4 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	answer := page.Messages[1]
	if answer.Role != "assistant" || answer.MessageID == "" {
		t.Fatalf("assistant row has no message id: %+v", answer)
	}
	body := func(id, content string, restore bool) string {
		return fmt.Sprintf(`{"session_id":%q,"seq":%d,"message_id":%q,"content":%q,"restore":%t}`, sess.ID, answer.Seq, id, content, restore)
	}

	if rec := postMessageEdit(t, srv, body(answer.MessageID, "edited", false)); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "assistant_edit") {
		t.Fatalf("disabled setting: %d %s", rec.Code, rec.Body.String())
	}
	if rec := knobsPOST(t, srv, `{"key":"assistant_edit","value":"on"}`); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d", rec.Code)
	}

	release := srv.eng.beginActiveRun()
	rec := postMessageEdit(t, srv, body(answer.MessageID, "edited", false))
	release()
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy: %d %s", rec.Code, rec.Body.String())
	}

	if rec := postMessageEdit(t, srv, body("999999", "edited", false)); rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body.String())
	}
	user := page.Messages[0]
	userBody := fmt.Sprintf(`{"session_id":%q,"seq":%d,"message_id":%q,"content":"x"}`, sess.ID, user.Seq, user.MessageID)
	if rec := postMessageEdit(t, srv, userBody); rec.Code != http.StatusBadRequest {
		t.Fatalf("user row: %d %s", rec.Code, rec.Body.String())
	}

	rec = postMessageEdit(t, srv, body(answer.MessageID, "edited answer", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Edited       bool   `json:"edited"`
		ModelVisible bool   `json:"model_visible"`
		Content      string `json:"content"`
		MessageID    string `json:"message_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Edited || out.ModelVisible || out.Content != "edited answer" || out.MessageID != answer.MessageID {
		t.Fatalf("edit result=%s err=%v", rec.Body.String(), err)
	}
	after, _ := srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if got := after.Messages[1]; got.Content != "edited answer" || !got.Edited || got.EditModelVisible {
		t.Fatalf("transcript does not reflect edit: %+v", got)
	}
	store, _ := srv.eng.sessionStore()
	model, _ := store.ReadModelContext(ctx, sess.ID)
	if model[1].Content != "first answer" {
		t.Fatalf("display-only edit reached the model: %+v", model[1])
	}

	if rec := knobsPOST(t, srv, `{"key":"assistant_edit_model_sees","value":"on"}`); rec.Code != http.StatusOK {
		t.Fatal("enable model sees")
	}
	if rec := postMessageEdit(t, srv, body(answer.MessageID, "model edit", false)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"model_visible":true`) {
		t.Fatalf("model-visible edit: %d %s", rec.Code, rec.Body.String())
	}
	model, _ = store.ReadModelContext(ctx, sess.ID)
	if model[1].TextOnly().Content != "model edit" {
		t.Fatalf("model did not see edit: %+v", model[1])
	}

	if rec := postMessageEdit(t, srv, body(answer.MessageID, "", true)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"first answer"`) {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	after, _ = srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if got := after.Messages[1]; got.Content != "first answer" || got.Edited {
		t.Fatalf("restore transcript: %+v", got)
	}
	model, _ = store.ReadModelContext(ctx, sess.ID)
	if model[1].Content != "first answer" {
		t.Fatalf("restore model: %+v", model[1])
	}
}
