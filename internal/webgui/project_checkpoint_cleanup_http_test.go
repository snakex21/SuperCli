package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func projectCleanupFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	srv := newTestServer(t, false)
	home := t.TempDir()
	if err := srv.eng.projectAction("add", home, "Cleanup fixture", ""); err != nil {
		t.Fatal(err)
	}
	seedProjectCleanupCheckpoint(t, srv.eng, home)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	chat, err := store.Create(home, "echo", "Preserved conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.NewWriter(store, chat.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "Preserve this conversation"}); err != nil {
		t.Fatal(err)
	}
	projectStore, err := memory.OpenProjectStore(srv.eng.DataDir(), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := projectStore.Put(memory.Entry{ID: "cleanup-memory", Scope: memory.ScopeFact, Content: "Preserved project memory"}); err != nil {
		t.Fatal(err)
	}
	if err := projectStore.Close(); err != nil {
		t.Fatal(err)
	}
	return srv, home, chat.ID
}

func seedProjectCleanupCheckpoint(t *testing.T, eng *Engine, home string) {
	t.Helper()
	manager, err := eng.checkpointManager(home)
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("cleanup-fixture", "synthetic checkpoint")
	wrapped := turn.Wrap(tools.Tool{Name: "write_file", Description: "fixture", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "written"}, os.WriteFile(filepath.Join(home, "keep.txt"), []byte("Current project file"), 0600)
	}})
	result, err := wrapped.Fn(context.Background(), json.RawMessage(`{"path":"keep.txt"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("fixture write: result=%+v err=%v", result, err)
	}
	if record, err := turn.Complete(context.Background()); err != nil || record == nil {
		t.Fatalf("fixture checkpoint: record=%+v err=%v", record, err)
	}
}

func projectCleanupRequest(t *testing.T, srv *Server, ctx context.Context, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(string(raw))).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.handleProjects(rec, req)
	return rec
}

func assertProjectCleanupPreservedData(t *testing.T, srv *Server, home, chatID string) {
	t.Helper()
	if content, err := os.ReadFile(filepath.Join(home, "keep.txt")); err != nil || string(content) != "Current project file" {
		t.Fatalf("workspace file changed: %q %v", content, err)
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	chat, err := store.Get(chatID)
	if err != nil || !sameSessionWorkspace(chat.Cwd, home) {
		t.Fatalf("conversation removed or reassigned: %+v %v", chat, err)
	}
	rows, err := store.ReadTranscriptMessages(context.Background(), chatID)
	if err != nil || len(rows) != 1 || rows[0].Content != "Preserve this conversation" {
		t.Fatalf("conversation changed: %+v %v", rows, err)
	}
	projectStore, err := memory.OpenStore(filepath.Join(srv.eng.DataDir(), "projects", memory.ProjectKey(home)))
	if err != nil {
		t.Fatal(err)
	}
	defer projectStore.Close()
	entry, err := projectStore.Get("cleanup-memory")
	if err != nil || entry.Content != "Preserved project memory" {
		t.Fatalf("project memory changed: %+v %v", entry, err)
	}
}

func TestProjectCheckpointHTTPPreviewClearPreservesProjectAndOtherData(t *testing.T) {
	srv, home, chatID := projectCleanupFixture(t)
	other := t.TempDir()
	if err := srv.eng.projectAction("add", other, "Other fixture", ""); err != nil {
		t.Fatal(err)
	}
	seedProjectCleanupCheckpoint(t, srv.eng, other)
	beforeOther, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), other, srv.eng.DataDir())
	if err != nil || beforeOther.Bytes <= 0 {
		t.Fatalf("other checkpoint fixture: %+v %v", beforeOther, err)
	}
	before := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "checkpoint_preview", "target": home})
	var preview checkpoint.WorkspaceCleanupPreview
	if err := json.Unmarshal(before.Body.Bytes(), &preview); err != nil || before.Code != http.StatusOK || preview.Bytes <= 0 || preview.Files <= 0 || preview.Stores != 1 || !sameSessionWorkspace(preview.Workspace, home) {
		t.Fatalf("preview status=%d body=%s err=%v", before.Code, before.Body.String(), err)
	}
	cleared := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "clear_checkpoints", "target": home})
	var receipt checkpoint.WorkspaceCleanupPreview
	if err := json.Unmarshal(cleared.Body.Bytes(), &receipt); err != nil || cleared.Code != http.StatusOK || receipt.Bytes != preview.Bytes || receipt.Stores != preview.Stores {
		t.Fatalf("clear status=%d body=%s err=%v", cleared.Code, cleared.Body.String(), err)
	}
	if _, ok := memory.LoadWorkspace(srv.eng.DataDir()).Get(home); !ok || srv.eng.Home() != other {
		t.Fatal("manual cleanup removed registration or changed active home")
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir()); err != nil || after.Bytes != 0 || after.Stores != 0 {
		t.Fatalf("checkpoints remain: %+v %v", after, err)
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), other, srv.eng.DataDir()); err != nil || after.Bytes != beforeOther.Bytes {
		t.Fatalf("unrelated checkpoint changed: %+v %v", after, err)
	}
	for key := range srv.eng.checkpoints {
		if sameSessionWorkspace(key, home) {
			t.Fatal("cleared project retained stale manager")
		}
	}
	assertProjectCleanupPreservedData(t, srv, home, chatID)
}

func TestProjectCheckpointHTTPRemovalDefaultsToKeepAndRequiresExplicitRemember(t *testing.T) {
	srv, home, chatID := projectCleanupFixture(t)
	removed := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "remove", "target": home})
	if removed.Code != http.StatusOK {
		t.Fatalf("default remove status=%d body=%s", removed.Code, removed.Body.String())
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir()); err != nil || after.Bytes <= 0 {
		t.Fatalf("default removal discarded checkpoints: %+v %v", after, err)
	}
	assertProjectCleanupPreservedData(t, srv, home, chatID)
	if err := srv.eng.projectAction("add", home, "Cleanup fixture", ""); err != nil {
		t.Fatal(err)
	}
	invalid := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "remove", "target": home, "remember_cleanup": true})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("implicit remember accepted: status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if _, ok := memory.LoadWorkspace(srv.eng.DataDir()).Get(home); !ok {
		t.Fatal("invalid remember removed project")
	}
	explicit := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "remove", "target": home, "delete_checkpoints": true, "remember_cleanup": true})
	if explicit.Code != http.StatusOK {
		t.Fatalf("explicit cleanup status=%d body=%s", explicit.Code, explicit.Body.String())
	}
	if enabled, err := memory.LoadProjectCheckpointCleanup(srv.eng.DataDir()); err != nil || !enabled {
		t.Fatalf("explicit preference not remembered: %t %v", enabled, err)
	}
	assertProjectCleanupPreservedData(t, srv, home, chatID)
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir()); err != nil || after.Bytes != 0 {
		t.Fatalf("explicit cleanup did not clear checkpoints: %+v %v", after, err)
	}
	get := httptest.NewRecorder()
	srv.handleProjects(get, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	var response struct {
		Delete bool `json:"delete_checkpoints_on_remove"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &response); err != nil || get.Code != http.StatusOK || !response.Delete {
		t.Fatalf("GET preference: status=%d body=%s err=%v", get.Code, get.Body.String(), err)
	}
}

func TestProjectCheckpointHTTPStoredPolicyCanBeOverriddenOrUsed(t *testing.T) {
	for _, explicitKeep := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_keep=%t", explicitKeep), func(t *testing.T) {
			srv, home, chatID := projectCleanupFixture(t)
			if err := memory.SaveProjectCheckpointCleanup(srv.eng.DataDir(), true); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"action": "remove", "target": home}
			if explicitKeep {
				body["delete_checkpoints"] = false
			}
			rec := projectCleanupRequest(t, srv, context.Background(), body)
			if rec.Code != http.StatusOK {
				t.Fatalf("remove status=%d body=%s", rec.Code, rec.Body.String())
			}
			after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir())
			if err != nil || (after.Bytes > 0) != explicitKeep {
				t.Fatalf("preference not applied or override ignored: %+v %v", after, err)
			}
			if enabled, err := memory.LoadProjectCheckpointCleanup(srv.eng.DataDir()); err != nil || !enabled {
				t.Fatal("one-time override changed stored policy")
			}
			assertProjectCleanupPreservedData(t, srv, home, chatID)
		})
	}
}

func TestProjectCheckpointHTTPRejectsBusyCanceledAndUnregisteredTargets(t *testing.T) {
	srv, home, _ := projectCleanupFixture(t)
	before, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	finish := srv.eng.beginActiveRun()
	for _, action := range []string{"remove", "clear_checkpoints"} {
		body := map[string]any{"action": action, "target": home}
		if action == "remove" {
			body["delete_checkpoints"] = true
		}
		rec := projectCleanupRequest(t, srv, context.Background(), body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("busy %s status=%d body=%s", action, rec.Code, rec.Body.String())
		}
	}
	finish()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := projectCleanupRequest(t, srv, ctx, map[string]any{"action": "remove", "target": home, "delete_checkpoints": true, "remember_cleanup": true})
	if canceled.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled cleanup status=%d body=%s", canceled.Code, canceled.Body.String())
	}
	for _, action := range []string{"checkpoint_preview", "clear_checkpoints", "remove"} {
		rec := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": action, "target": t.TempDir()})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unregistered %s accepted: status=%d body=%s", action, rec.Code, rec.Body.String())
		}
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir()); err != nil || after.Bytes != before.Bytes {
		t.Fatalf("rejected request changed checkpoints: %+v %v", after, err)
	}
	if _, ok := memory.LoadWorkspace(srv.eng.DataDir()).Get(home); !ok {
		t.Fatal("rejected request removed registration")
	}
	if enabled, err := memory.LoadProjectCheckpointCleanup(srv.eng.DataDir()); err != nil || enabled {
		t.Fatal("rejected request changed preference")
	}
}

func TestProjectCheckpointHTTPInvalidOptionsAndPreferenceFailBeforeRemoval(t *testing.T) {
	srv, home, _ := projectCleanupFixture(t)
	before, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{
		{"action": "remove", "target": home, "delete_checkpoints": "true"},
		{"action": "remove", "target": home, "remember_cleanup": "true", "delete_checkpoints": true},
		{"action": "clear_checkpoints", "target": home, "delete_checkpoints": false},
		{"action": "checkpoint_preview", "target": home, "remember_cleanup": true},
		{"action": "remove", "target": home, "delete_checkpoints": true, "unrecognized": true},
	} {
		rec := projectCleanupRequest(t, srv, context.Background(), body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid options accepted: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if err := os.WriteFile(filepath.Join(srv.eng.DataDir(), "project-cleanup.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "remove", "target": home, "delete_checkpoints": true})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("invalid preference accepted: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := memory.LoadWorkspace(srv.eng.DataDir()).Get(home); !ok {
		t.Fatal("invalid request or preference removed project")
	}
	if after, err := checkpoint.PreviewWorkspaceCleanup(context.Background(), home, srv.eng.DataDir()); err != nil || after.Bytes != before.Bytes {
		t.Fatalf("invalid request or preference cleared checkpoints: %+v %v", after, err)
	}
}

func TestProjectCheckpointHTTPActiveLeaseKeepsRegistrationAndCache(t *testing.T) {
	srv, home, _ := projectCleanupFixture(t)
	manager, err := srv.eng.checkpointManager(home)
	if err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("pending-cleanup-fixture", "live synthetic mutation")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	wrapped := turn.Wrap(tools.Tool{Name: "write_file", Description: "fixture", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		close(entered)
		<-release
		return tools.Result{Text: "written"}, os.WriteFile(filepath.Join(home, "pending.txt"), []byte("pending"), 0600)
	}})
	done := make(chan error, 1)
	go func() {
		result, err := wrapped.Fn(context.Background(), json.RawMessage(`{"path":"pending.txt"}`))
		if err == nil {
			err = result.Err
		}
		done <- err
	}()
	<-entered
	rec := projectCleanupRequest(t, srv, context.Background(), map[string]any{"action": "remove", "target": home, "delete_checkpoints": true})
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("live checkpoint cleanup status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := memory.LoadWorkspace(srv.eng.DataDir()).Get(home); !ok {
		t.Fatal("live lease failure removed project")
	}
	if current, err := srv.eng.checkpointManager(home); err != nil || current != manager {
		t.Fatal("live lease failure discarded cached manager")
	}
}
