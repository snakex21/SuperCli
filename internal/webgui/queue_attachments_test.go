package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/storage/session"
)

func queueAttachmentRequest(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleTasks(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func listQueuedOverHTTP(t *testing.T, srv *Server) []session.QueuedTask {
	t.Helper()
	rec := queueAttachmentRequest(t, srv, http.MethodGet, "/api/tasks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out []session.QueuedTask
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func stageQueueAttachment(t *testing.T, srv *Server, name string) string {
	t.Helper()
	dir := filepath.Join(srv.eng.Home(), ".supercli", "attachments", "upload-"+strings.TrimSuffix(name, filepath.Ext(name)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nfixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandleTasksPersistsAttachmentsThroughGetAndPatch(t *testing.T) {
	srv := newTestServer(t, false)
	image := stageQueueAttachment(t, srv, "shot.png")
	doc := filepath.Join(srv.eng.Home(), "notes.txt")
	if err := os.WriteFile(doc, []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"prompt": "look at this", "attachments": []string{image, doc, image}})
	rec := queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created session.QueuedTask
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	want := []string{image, doc}
	if !reflect.DeepEqual(created.Attachments, want) {
		t.Fatalf("created attachments=%v", created.Attachments)
	}
	// A durable read (reload, other client) returns the same paths.
	got := listQueuedOverHTTP(t, srv)
	if len(got) != 1 || got[0].ID != created.ID || !reflect.DeepEqual(got[0].Attachments, want) {
		t.Fatalf("GET queue=%+v", got)
	}
	rec = queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", fmt.Sprintf(`{"id":%q,"prompt":"edited"}`, created.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", fmt.Sprintf(`{"id":%q,"position":0}`, created.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", rec.Code, rec.Body.String())
	}
	got = listQueuedOverHTTP(t, srv)
	if len(got) != 1 || got[0].Prompt != "edited" || !reflect.DeepEqual(got[0].Attachments, want) {
		t.Fatalf("PATCH lost attachments: %+v", got)
	}
	// Plain text tasks (TUI, schedules) keep working without attachments.
	if _, err := srv.eng.enqueueTask(context.Background(), "", "plain"); err != nil {
		t.Fatal(err)
	}
	got = listQueuedOverHTTP(t, srv)
	if len(got) != 2 || got[1].Prompt != "plain" || got[1].Attachments != nil {
		t.Fatalf("plain task=%+v", got)
	}
	if !strings.Contains(listRaw(t, srv), `"attachments":[`) {
		t.Fatal("GET JSON does not expose attachments")
	}
}

func listRaw(t *testing.T, srv *Server) string {
	t.Helper()
	return queueAttachmentRequest(t, srv, http.MethodGet, "/api/tasks", "").Body.String()
}

func TestHandleTasksAttachmentOnlyAndLimits(t *testing.T) {
	srv := newTestServer(t, false)
	image := stageQueueAttachment(t, srv, "only.png")
	rec := queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks", fmt.Sprintf(`{"prompt":"","attachments":[%q]}`, image))
	if rec.Code != http.StatusOK {
		t.Fatalf("attachment-only status=%d body=%s", rec.Code, rec.Body.String())
	}
	missing := filepath.Join(srv.eng.Home(), "missing.png")
	rec = queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks", fmt.Sprintf(`{"prompt":"x","attachments":[%q]}`, missing))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing attachment status=%d", rec.Code)
	}
	many := make([]string, maxChatAttachments+1)
	for i := range many {
		many[i] = image
	}
	payload, _ := json.Marshal(map[string]any{"prompt": "x", "attachments": many})
	rec = queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks", string(payload))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too many attachments status=%d", rec.Code)
	}
	if got := listQueuedOverHTTP(t, srv); len(got) != 1 {
		t.Fatalf("rejected tasks were stored: %+v", got)
	}
}

func TestHandleTasksDeleteCleansOnlyUnusedStagedAttachments(t *testing.T) {
	srv := newTestServer(t, false)
	ctx := context.Background()
	removed := stageQueueAttachment(t, srv, "removed.png")
	shared := stageQueueAttachment(t, srv, "shared.png")
	dispatched := stageQueueAttachment(t, srv, "dispatched.png")
	userFile := filepath.Join(srv.eng.Home(), "user.png")
	if err := os.WriteFile(userFile, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := srv.eng.enqueueTaskWithAttachments(ctx, "", "first", []string{removed, shared, userFile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.eng.enqueueTaskWithAttachments(ctx, "", "second", []string{shared}); err != nil {
		t.Fatal(err)
	}
	third, err := srv.eng.enqueueTaskWithAttachments(ctx, "", "third", []string{dispatched})
	if err != nil {
		t.Fatal(err)
	}
	if rec := queueAttachmentRequest(t, srv, http.MethodDelete, "/api/tasks?id="+first.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(removed); !os.IsNotExist(err) {
		t.Fatalf("unused staged attachment left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(removed)); !os.IsNotExist(err) {
		t.Fatalf("empty staging directory left behind: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("attachment still used by another task was removed: %v", err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("user file outside the staging directory was removed: %v", err)
	}
	// An accepted dispatch retires the row but /api/chat now owns the file.
	if rec := queueAttachmentRequest(t, srv, http.MethodDelete, "/api/tasks?keep_attachments=1&id="+third.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("dispatch DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dispatched); err != nil {
		t.Fatalf("dispatched attachment was removed: %v", err)
	}
	if got := listQueuedOverHTTP(t, srv); len(got) != 1 || got[0].Prompt != "second" || !reflect.DeepEqual(got[0].Attachments, []string{shared}) {
		t.Fatalf("remaining queue=%+v", got)
	}
}
