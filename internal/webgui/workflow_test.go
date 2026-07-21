package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func createRewindSession(t *testing.T, srv *Server, title string) (session.Session, *session.Store) {
	t.Helper()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.Create(srv.eng.Home(), "echo", title)
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, source.ID)
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "first answer"},
		{Role: llm.RoleUser, Content: "return here"},
		{Role: llm.RoleAssistant, Content: "discard this answer"},
	} {
		if err := writer.AppendMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	return source, store
}

func postSessionRewind(t *testing.T, srv *Server, sessionID string, seq int, files bool, reason string) sessionRewindView {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":%q,"selected_seq":%d,"rewind_files":%t,"reason":%q}`, sessionID, seq, files, reason)
	recorder := httptest.NewRecorder()
	srv.handleSessionRewind(recorder, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("rewind status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response sessionRewindView
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandleSessionRewindEditsCurrentConversationWithoutBranch(t *testing.T) {
	srv := newTestServer(t, false)
	source, store := createRewindSession(t, srv, "rewind in place")

	response := postSessionRewind(t, srv, source.ID, 3, false, "try a safer approach")
	if !response.OK || response.Removed != 2 || response.FilesRewound {
		t.Fatalf("response = %+v", response)
	}
	page, err := srv.eng.transcriptPage(context.Background(), source.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 || page.Messages[1].Content != "first answer" || page.Messages[2].Role != string(llm.RoleSystem) {
		t.Fatalf("rewound transcript = %+v", page.Messages)
	}
	if !strings.Contains(page.Messages[2].Content, "source of truth") || !strings.Contains(page.Messages[2].Content, "try a safer approach") {
		t.Fatalf("rewind marker = %q", page.Messages[2].Content)
	}
	meta, err := store.Get(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MessageCount != 3 || meta.ParentID != "" {
		t.Fatalf("session metadata = %+v", meta)
	}
}

func TestHandleSessionRewindKeepsFilesAndDetachesOldCheckpoints(t *testing.T) {
	srv := newTestServer(t, false)
	source, _ := createRewindSession(t, srv, "keep current files")
	path := filepath.Join(srv.eng.Home(), "app.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if err != nil {
		t.Skip(err)
	}
	turn := manager.NewTurn(source.ID, "return here")
	turn.SetUserSeq(3)
	spec := turn.Wrap(tools.NewWriteFile(srv.eng.Home()).Spec())
	result, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"app.txt","content":"after"}`))
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
		t.Fatalf("checkpoint record=%+v err=%v", record, completeErr)
	}

	postSessionRewind(t, srv, source.ID, 3, false, "")
	if got, _ := os.ReadFile(path); string(got) != "after" {
		t.Fatalf("current file state was changed: %q", got)
	}
	if preview := manager.PreviewFrom(source.ID, 3); len(preview.Records) != 0 {
		t.Fatalf("discarded checkpoint still attached: %+v", preview)
	}
}

func TestHandleSessionRewindConversationAndFiles(t *testing.T) {
	srv := newTestServer(t, false)
	source, _ := createRewindSession(t, srv, "rewind files")
	path := filepath.Join(srv.eng.Home(), "app.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if err != nil {
		t.Skip(err)
	}
	turn := manager.NewTurn(source.ID, "return here")
	turn.SetUserSeq(3)
	spec := turn.Wrap(tools.NewWriteFile(srv.eng.Home()).Spec())
	result, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"app.txt","content":"after"}`))
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
		t.Fatalf("checkpoint record=%+v err=%v", record, completeErr)
	}

	response := postSessionRewind(t, srv, source.ID, 3, true, "wrong implementation")
	if !response.FilesRewound || len(response.Files) != 1 || response.Files[0] != "app.txt" {
		t.Fatalf("response = %+v", response)
	}
	if got, _ := os.ReadFile(path); string(got) != "before" {
		t.Fatalf("rewound file = %q", got)
	}
	if preview := manager.PreviewFrom(source.ID, 3); len(preview.Records) != 0 {
		t.Fatalf("rewound checkpoint was not discarded: %+v", preview)
	}
	page, err := srv.eng.transcriptPage(context.Background(), source.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 || !strings.Contains(page.Messages[2].Content, "file changes were also restored") {
		t.Fatalf("rewound transcript = %+v", page.Messages)
	}
}

func TestHandleSessionRewindRejectsNonUserSequence(t *testing.T) {
	srv := newTestServer(t, false)
	source, _ := createRewindSession(t, srv, "invalid rewind")
	body := fmt.Sprintf(`{"session_id":%q,"selected_seq":2}`, source.ID)
	recorder := httptest.NewRecorder()
	srv.handleSessionRewind(recorder, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
