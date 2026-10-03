package session

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestStoreReadMessagesHonorsCancellation(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create("/cwd", "model", "canceled load")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(context.Background(), sess.ID, Encoded{Role: "user", Content: "kept"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := s.ReadMessages(ctx, sess.ID)
	if !errors.Is(err, context.Canceled) || len(rows) != 0 {
		t.Fatalf("canceled read: rows=%d err=%v", len(rows), err)
	}
	rows, err = s.ReadMessages(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 || rows[0].Content != "kept" {
		t.Fatalf("healthy read after cancellation: rows=%+v err=%v", rows, err)
	}
}

func TestStoreReadMessagesCancelsWhileConnectionIsOccupied(t *testing.T) {
	s := openTestStore(t)
	s.db.SetMaxOpenConns(1)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); _, err := s.ReadMessages(ctx, "blocked"); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read err=%v", err)
		}
	case <-time.After(2 * time.Second):
		conn.Close()
		<-done
		t.Fatal("canceled history read kept waiting for the occupied connection")
	}
}

func TestStoreReadMessageAtKeepsExactRowAndSession(t *testing.T) {
	s := openTestStore(t)
	const seq = int(^uint(0) >> 1)
	want := Encoded{SessionID: "first", Seq: seq, Role: "assistant", Content: "zażółć\x00終", PartsJSON: `[{"type":"text","text":"image caption"}]`, ToolCallID: "result-id", ToolCallsJSON: `[{"id":"tool-id","name":"read_image","arguments":{"path":"photo.png"}}]`, Name: "worker 1"}
	if err := s.EnsureSession(want.SessionID, "/cwd", "model"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSession("other", "/cwd", "model"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO messages(session_id,seq,role,content,parts_json,tool_call_id,tool_calls_json,name,created_at) VALUES(?,?,?,?,?,?,?,?,1)`, want.SessionID, want.Seq, want.Role, want.Content, want.PartsJSON, want.ToolCallID, want.ToolCallsJSON, want.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO messages(session_id,seq,role,content,created_at) VALUES('other',?,'user','other session',1)`, seq); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadMessageAt(context.Background(), want.SessionID, seq)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("point read=%+v err=%v, want=%+v", got, err, want)
	}
	other, err := s.ReadMessageAt(context.Background(), "other", seq)
	if err != nil || other.Content != "other session" || other.SessionID != "other" || other.PartsJSON != "" || other.ToolCallID != "" || other.ToolCallsJSON != "" || other.Name != "" {
		t.Fatalf("nullable/session fields=%+v err=%v", other, err)
	}
	for _, tc := range []struct {
		id  string
		seq int
	}{{want.SessionID, 1}, {want.SessionID, 0}, {want.SessionID, -1}, {"missing", seq}} {
		row, err := s.ReadMessageAt(context.Background(), tc.id, tc.seq)
		if !errors.Is(err, sql.ErrNoRows) || row != (Encoded{}) {
			t.Fatalf("missing %q/%d: row=%+v err=%v", tc.id, tc.seq, row, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if row, err := s.ReadMessageAt(ctx, want.SessionID, seq); !errors.Is(err, context.Canceled) || row != (Encoded{}) {
		t.Fatalf("canceled point read=%+v err=%v", row, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if row, err := s.ReadMessageAt(context.Background(), want.SessionID, seq); err == nil || errors.Is(err, sql.ErrNoRows) || row != (Encoded{}) {
		t.Fatalf("closed store point read=%+v err=%v", row, err)
	}
}
