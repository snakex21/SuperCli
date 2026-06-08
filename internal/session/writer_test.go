package session

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

func TestWriter_AppendMessage_PersistsViaStore(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	w := NewWriter(s, sess.ID)
	msg := llm.Message{Role: llm.RoleUser, Content: "hello"}
	if err := w.AppendMessage(context.Background(), msg); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	all, _ := s.ReadMessages(context.Background(), sess.ID)
	if len(all) != 1 {
		t.Fatalf("len = %d, want 1", len(all))
	}
	if all[0].Content != "hello" {
		t.Errorf("Content = %q", all[0].Content)
	}
}

func TestWriter_UpdateUsage_Accumulates(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	w := NewWriter(s, sess.ID)
	w.UpdateUsage(10, 5)
	w.UpdateUsage(20, 3)
	got, _ := s.Get(sess.ID)
	if got.TokenIn != 30 {
		t.Errorf("TokenIn = %d, want 30", got.TokenIn)
	}
	if got.TokenOut != 8 {
		t.Errorf("TokenOut = %d, want 8", got.TokenOut)
	}
}

func TestWriter_AppendMessage_RejectsInvalid(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	w := NewWriter(s, sess.ID)
	// empty message fails validation
	if err := w.AppendMessage(context.Background(), llm.Message{}); err == nil {
		t.Fatal("expected error on empty message")
	}
}

func TestWriter_SessionID(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create("/a", "m", "")
	w := NewWriter(s, sess.ID)
	if w.SessionID() != sess.ID {
		t.Errorf("SessionID = %q, want %q", w.SessionID(), sess.ID)
	}
}
