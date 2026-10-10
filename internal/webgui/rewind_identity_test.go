package webgui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestWebRewindStaleCardCannotDeleteRecreatedSessionOrAutolinkCheckpoint(t *testing.T) {
	srv := newTestServer(t, false)
	sess, store := createRewindSession(t, srv, "original")
	ctx := context.Background()
	page, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if err != nil || len(page.Messages) != 4 {
		t.Fatalf("initial transcript=%+v err=%v", page, err)
	}
	selected := page.Messages[2]
	messageID, err := strconv.ParseInt(selected.MessageID, 10, 64)
	if err != nil || messageID <= 0 || selected.Seq != 3 {
		t.Fatalf("selected card has no immutable ID: %+v err=%v", selected, err)
	}
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip("git unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(srv.eng.Home(), "identity.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn(sess.ID, "original")
	t.Cleanup(func() { _, _ = turn.Complete(context.Background()) })
	turn.SetUserMessageReceipt(selected.Seq, messageID)
	spec := turn.Wrap(tools.NewWriteFile(srv.eng.Home()).Spec())
	result, err := spec.Fn(ctx, json.RawMessage(`{"path":"identity.txt","content":"after"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("write err=%v/%v", err, result.Err)
	}
	record, err := turn.Complete(ctx)
	if err != nil || record == nil {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if err := srv.eng.deleteSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSession(sess.ID, sess.Cwd, "replacement"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []llm.Message{{Role: llm.RoleUser, Content: "replacement first"}, {Role: llm.RoleAssistant, Content: "replacement answer"}, {Role: llm.RoleUser, Content: selected.Content}} {
		if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	current, err := srv.eng.transcriptPage(ctx, sess.ID, 0, 20)
	if err != nil || len(current.Messages) != 3 || current.Messages[2].MessageID == selected.MessageID {
		t.Fatalf("replacement transcript=%+v err=%v", current, err)
	}
	preview, err := manager.PreviewFromContext(ctx, sess.ID, selected.Seq)
	if err != nil || len(preview.Records) != 0 {
		t.Fatalf("deleted physical user row auto-linked: %+v err=%v", preview, err)
	}
	for _, files := range []bool{false, true} {
		rec := httptest.NewRecorder()
		body := fmt.Sprintf(`{"session_id":%q,"selected_seq":%d,"selected_message_id":%q,"rewind_files":%t}`, sess.ID, selected.Seq, selected.MessageID, files)
		srv.handleSessionRewind(rec, httptest.NewRequest(http.MethodPost, "/api/session/rewind", strings.NewReader(body)))
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale POST files=%v status=%d body=%s", files, rec.Code, rec.Body.String())
		}
	}
	get := httptest.NewRecorder()
	srv.handleCheckpointRewind(get, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/checkpoint/rewind?session=%s&from_seq=3&message_id=%s", sess.ID, selected.MessageID), nil))
	if get.Code != http.StatusConflict {
		t.Fatalf("stale preview status=%d body=%s", get.Code, get.Body.String())
	}
	if rows, err := store.ReadMessages(ctx, sess.ID); err != nil || len(rows) != 3 {
		t.Fatalf("stale request deleted replacement: rows=%d err=%v", len(rows), err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "after" {
		t.Fatalf("stale request changed file: %q err=%v", got, err)
	}
	// The original checkpoint remains usable as explicitly identified recovery.
	if _, err := manager.Undo(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "before" {
		t.Fatalf("exact recovery lost Before: %q err=%v", got, err)
	}
}

func TestClearCheckpointManagersPreservesPendingAndUncachedStores(t *testing.T) {
	srv := newTestServer(t, false)
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip("git unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(srv.eng.DataDir(), "checkpoints")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, "uncached-recovery-proof")
	if err := os.WriteFile(unknown, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn := manager.NewTurn("synthetic-pending", "pending")
	var borrow *checkpoint.InvocationBorrow
	t.Cleanup(func() {
		borrow.Close()
		_, _ = turn.Complete(context.Background())
	})
	spec := turn.Wrap(tools.Tool{Name: "task", Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		var err error
		borrow, err = checkpoint.BorrowInvocation(ctx, true)
		return tools.Result{Err: err}, nil
	}})
	result, err := spec.Fn(context.Background(), json.RawMessage(`{}`))
	if err != nil || result.Err != nil || borrow == nil {
		t.Fatalf("accepted borrow err=%v/%v", err, result.Err)
	}
	if err := srv.eng.clearCheckpointManagers(); !errors.Is(err, checkpoint.ErrActiveTurn) {
		t.Fatalf("pending manager cleared: %v", err)
	}
	if cached, err := srv.eng.checkpointManager(srv.eng.Home()); err != nil || cached != manager {
		t.Fatalf("errored manager dropped: cached=%p manager=%p err=%v", cached, manager, err)
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "preserve" {
		t.Fatalf("uncached recovery removed: %q err=%v", got, err)
	}
	borrow.Close()
	if _, err := turn.Complete(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := srv.eng.clearCheckpointManagers(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(unknown); err != nil || string(got) != "preserve" {
		t.Fatalf("successful cached clear removed unknown store: %q err=%v", got, err)
	}
}

func TestWebFailedPromptAppendPublishesNoReceiptOrAutomaticCheckpoint(t *testing.T) {
	srv := newTestServer(t, false)
	sess, store := createRewindSession(t, srv, "failed current prompt")
	ctx := context.Background()
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip("git unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(srv.eng.DataDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TRIGGER reject_gui_prompt BEFORE INSERT ON messages WHEN NEW.role='user' BEGIN SELECT RAISE(ABORT, 'synthetic current append failure'); END`); err != nil {
		t.Fatal(err)
	}
	srv.eng.mu.Lock()
	srv.eng.prov = &writingProvider{}
	srv.eng.mu.Unlock()
	activities := 0
	if err := srv.eng.runStream(ctx, "rejected current prompt", sess.ID, "", func(ev wireEvent) {
		if ev.Type == "session_activity" {
			activities++
			if ev.UserSeq != 0 || ev.UserMessageID != "" {
				t.Errorf("failed current prompt adopted a historical receipt: %+v", ev)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if activities != 1 {
		t.Fatalf("activities=%d, want 1", activities)
	}
	record := manager.Latest(sess.ID)
	if record == nil || record.UserSeq != 0 || record.UserMessageID != 0 {
		t.Fatalf("unpersisted prompt attached to old history: %+v", record)
	}
	if receipt, err := store.ReadUserReceiptAt(ctx, sess.ID, 3); err != nil || receipt.ID <= 0 {
		t.Fatalf("existing prompt should remain: %+v err=%v", receipt, err)
	}
	if preview, err := manager.PreviewFromContext(ctx, sess.ID, 1); err != nil || len(preview.Records) != 0 {
		t.Fatalf("unbound recovery auto-linked: %+v err=%v", preview, err)
	}
}

func TestDataClearSessionsRetainsExplicitCheckpointRecovery(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.mu.Lock()
	srv.eng.prov = &writingProvider{}
	srv.eng.mu.Unlock()
	ctx := context.Background()
	var sid string
	if err := srv.eng.runStream(ctx, "write synthetic file", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := srv.eng.checkpointManager(srv.eng.Home())
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip("git unavailable")
	}
	if err != nil {
		t.Fatal(err)
	}
	record := manager.Latest(sid)
	if record == nil {
		t.Fatal("synthetic write produced no checkpoint")
	}
	clear := httptest.NewRecorder()
	srv.handleDataClear(clear, httptest.NewRequest(http.MethodPost, "/api/data/clear", strings.NewReader(`{"action":"sessions"}`)))
	if clear.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", clear.Code, clear.Body.String())
	}
	if retained := manager.Latest(sid); retained == nil || retained.ID != record.ID {
		t.Fatalf("history delete erased explicit recovery: %+v", retained)
	}
	if preview, err := manager.PreviewFromContext(ctx, sid, 1); err != nil || len(preview.Records) != 0 {
		t.Fatalf("deleted chat retained automatic links: %+v err=%v", preview, err)
	}
	if _, err := manager.Undo(ctx, record.ID); err != nil {
		t.Fatalf("explicit recovery after clear=%v", err)
	}
	if _, err := os.Stat(filepath.Join(srv.eng.Home(), "agent.txt")); !os.IsNotExist(err) {
		t.Fatalf("explicit recovery did not undo created file: %v", err)
	}
}
