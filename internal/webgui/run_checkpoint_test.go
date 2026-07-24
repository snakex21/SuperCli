package webgui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
)

type writingProvider struct{ calls int }

func (p *writingProvider) Name() string { return "writer" }
func (p *writingProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	out := make(chan llm.Delta, 3)
	if p.calls == 1 {
		out <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{ID: "write-1", Name: "write_file", Arguments: `{"path":"agent.txt","content":"made by agent"}`}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Role: llm.RoleAssistant, Content: "done"}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

type writingThenFailProvider struct{ calls int }

func (p *writingThenFailProvider) Name() string { return "writer-fail" }
func (p *writingThenFailProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	if p.calls > 1 {
		return nil, errors.New("provider failed after write")
	}
	out := make(chan llm.Delta, 2)
	out <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{ID: "write-fail-1", Name: "write_file", Arguments: `{"path":"before-error.txt","content":"kept"}`}}
	out <- llm.Delta{FinishReason: "tool_calls"}
	close(out)
	return out, nil
}

func TestWebTurnCheckpointUndoAndHistoryEvent(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.mu.Lock()
	eng.prov = &writingProvider{}
	eng.mu.Unlock()
	var checkpointID, sessionID string
	var fileChanges []checkpoint.FileChange
	if err := eng.runStream(context.Background(), "write it", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sessionID = ev.SessionID
		}
		if ev.Type == "done" {
			checkpointID = ev.CheckpointID
			fileChanges = ev.FileChanges
		}
	}); err != nil {
		t.Fatal(err)
	}
	if checkpointID == "" {
		t.Fatal("done event missing checkpoint id")
	}
	if len(fileChanges) != 1 || fileChanges[0] != (checkpoint.FileChange{Path: "agent.txt", Kind: "created"}) {
		t.Fatalf("done event file changes = %+v", fileChanges)
	}
	manager, err := eng.checkpointManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if record := manager.Latest(sessionID); record == nil || record.UserSeq != 1 {
		t.Fatalf("checkpoint is not tied to initiating user message: %+v", record)
	}
	path := filepath.Join(dir, "agent.txt")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(eng, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/checkpoint", strings.NewReader(`{"id":"`+checkpointID+`","action":"undo"}`))
	req.Host = "127.0.0.1:8080"
	req.RemoteAddr = "127.0.0.1:43210"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created file remains: %v", err)
	}
	msgs, err := eng.transcript(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range msgs {
		if msg.Role == string(llm.RoleSystem) && strings.Contains(msg.Content, "User undid changes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("undo event missing from transcript: %+v", msgs)
	}
	lesson := httptest.NewRecorder()
	lessonReq := httptest.NewRequest(http.MethodPost, "/api/checkpoint/lesson", strings.NewReader(`{"session_id":"`+sessionID+`","checkpoint_id":"`+checkpointID+`","reason":"changed the wrong file","scope":"session"}`))
	lessonReq.Host = "127.0.0.1:8080"
	lessonReq.RemoteAddr = "127.0.0.1:43210"
	srv.Handler().ServeHTTP(lesson, lessonReq)
	if lesson.Code != http.StatusOK {
		t.Fatalf("lesson status=%d body=%s", lesson.Code, lesson.Body.String())
	}
	msgs, err = eng.transcript(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[len(msgs)-1].Content, "changed the wrong file") {
		t.Fatalf("lesson missing from transcript: %+v", msgs[len(msgs)-1])
	}
}

func TestWebTurnReportsFileChangesWhenProviderFailsAfterWrite(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.mu.Lock()
	eng.prov = &writingThenFailProvider{}
	eng.mu.Unlock()
	var sessionID string
	var failure wireEvent
	if err := eng.runStream(context.Background(), "write then fail", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sessionID = ev.SessionID
		}
		if ev.Type == "error" {
			failure = ev
		}
	}); err != nil {
		t.Fatal(err)
	}
	if failure.CheckpointID == "" || len(failure.FileChanges) != 1 {
		t.Fatalf("failure event did not retain file changes: %+v", failure)
	}
	if got := failure.FileChanges[0]; got.Path != "before-error.txt" || got.Kind != "created" {
		t.Fatalf("failure file change = %+v", got)
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	turns, err := store.ReadTurnSummaries(context.Background(), sessionID)
	if err != nil || len(turns) != 1 || len(turns[0].FileChanges) != 1 {
		t.Fatalf("persisted failed-turn changes = %+v, err=%v", turns, err)
	}
}
