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

func TestHandleBranches_RewindStopsBeforeSelectedMessageAndKeepsOriginal(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.Create(srv.eng.Home(), "echo", "rewind")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, source.ID)
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "first answer"},
		{Role: llm.RoleUser, Content: "return here"},
		{Role: llm.RoleAssistant, Content: "remove from the new view"},
	} {
		if err := writer.AppendMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}

	postBranch := func(through int) sessionMeta {
		t.Helper()
		body := fmt.Sprintf(`{"session_id":%q,"through_seq":%d}`, source.ID, through)
		rec := httptest.NewRecorder()
		srv.handleBranches(rec, httptest.NewRequest(http.MethodPost, "/api/branches", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("rewind branch status = %d: %s", rec.Code, rec.Body.String())
		}
		var branch sessionMeta
		if err := json.Unmarshal(rec.Body.Bytes(), &branch); err != nil {
			t.Fatal(err)
		}
		return branch
	}

	branch := postBranch(2) // selected user message is seq 3
	page, err := srv.eng.transcriptPage(context.Background(), branch.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 || page.Messages[1].Role != string(llm.RoleAssistant) || page.Messages[1].Content != "first answer" {
		t.Fatalf("rewound transcript = %+v", page.Messages)
	}

	fromStart := postBranch(-1) // selected user message is seq 1
	empty, err := srv.eng.transcriptPage(context.Background(), fromStart.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Messages) != 0 {
		t.Fatalf("first-message rewind is not empty: %+v", empty.Messages)
	}
	original, err := srv.eng.transcriptPage(context.Background(), source.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Messages) != 4 {
		t.Fatalf("original transcript was changed: %+v", original.Messages)
	}
}

func TestHandleBranches_RewindConversationAndFiles(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.Create(srv.eng.Home(), "echo", "rewind files")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, source.ID)
	for _, message := range []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "first answer"},
		{Role: llm.RoleUser, Content: "bad change"},
		{Role: llm.RoleAssistant, Content: "changed it"},
	} {
		if err := writer.AppendMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(srv.eng.Home(), "app.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if err != nil {
		t.Skip(err)
	}
	turn := manager.NewTurn(source.ID, "bad change")
	turn.SetUserSeq(3)
	spec := turn.Wrap(tools.NewWriteFile(srv.eng.Home()).Spec())
	result, _ := spec.Fn(context.Background(), json.RawMessage(`{"path":"app.txt","content":"after"}`))
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if record, completeErr := turn.Complete(context.Background()); completeErr != nil || record == nil {
		t.Fatalf("checkpoint record=%+v err=%v", record, completeErr)
	}
	previewRec := httptest.NewRecorder()
	srv.handleCheckpointRewind(previewRec, httptest.NewRequest(http.MethodGet,
		"/api/checkpoint/rewind?session="+source.ID+"&from_seq=3", nil))
	var preview checkpointRewindView
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", previewRec.Code, previewRec.Body.String())
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Available || preview.Checkpoints != 1 || len(preview.Files) != 1 {
		t.Fatalf("preview=%+v", preview)
	}

	// An older checkpoint must not make an unrelated later message look as if
	// it changed files. The UI only offers file rewind when this preview says so.
	noChangesRec := httptest.NewRecorder()
	srv.handleCheckpointRewind(noChangesRec, httptest.NewRequest(http.MethodGet,
		"/api/checkpoint/rewind?session="+source.ID+"&from_seq=5", nil))
	var noChanges checkpointRewindView
	if noChangesRec.Code != http.StatusOK {
		t.Fatalf("no-changes preview status = %d: %s", noChangesRec.Code, noChangesRec.Body.String())
	}
	if err := json.Unmarshal(noChangesRec.Body.Bytes(), &noChanges); err != nil {
		t.Fatal(err)
	}
	if noChanges.Available || noChanges.Checkpoints != 0 || len(noChanges.Files) != 0 {
		t.Fatalf("no-changes preview=%+v", noChanges)
	}

	body := fmt.Sprintf(`{"session_id":%q,"through_seq":2,"selected_seq":3,"rewind_files":true}`, source.ID)
	rec := httptest.NewRecorder()
	srv.handleBranches(rec, httptest.NewRequest(http.MethodPost, "/api/branches", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rewind status = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != "before" {
		t.Fatalf("rewound file=%q", got)
	}
	var branch sessionMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &branch); err != nil {
		t.Fatal(err)
	}
	page, err := srv.eng.transcriptPage(context.Background(), branch.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 || page.Messages[1].Content != "first answer" {
		t.Fatalf("rewound branch=%+v", page.Messages)
	}
	if branch.FileRewind == nil || branch.FileRewind.SessionID != source.ID || len(branch.FileRewind.CheckpointIDs) != 1 || len(branch.FileRewind.Files) != 1 {
		t.Fatalf("file rewind receipt=%+v", branch.FileRewind)
	}

	// Changing one's mind restores the exact batch produced by this rewind,
	// not an arbitrary latest checkpoint.
	checkpointIDs, err := json.Marshal(branch.FileRewind.CheckpointIDs)
	if err != nil {
		t.Fatal(err)
	}
	redoBody := fmt.Sprintf(`{"session_id":%q,"branch_session_id":%q,"checkpoint_ids":%s}`,
		source.ID, branch.ID, checkpointIDs)
	redoRec := httptest.NewRecorder()
	srv.handleCheckpointRewind(redoRec, httptest.NewRequest(http.MethodPost,
		"/api/checkpoint/rewind", strings.NewReader(redoBody)))
	if redoRec.Code != http.StatusOK {
		t.Fatalf("redo rewind status = %d: %s", redoRec.Code, redoRec.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != "after" {
		t.Fatalf("restored agent file=%q", got)
	}
}
