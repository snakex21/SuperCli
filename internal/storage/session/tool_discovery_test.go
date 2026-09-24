package session

import (
	"context"
	"reflect"
	"testing"

	"supercli/internal/llm"
)

func TestDiscoveredToolsPersistReopenAndInvalidate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create(dir, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, sess.ID)
	if names, err := w.ReadDiscoveredTools(ctx); err != nil || len(names) != 0 {
		t.Fatalf("old session=%v %v", names, err)
	}
	if err := w.SaveDiscoveredTools(ctx, []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "request"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w = NewWriter(s, sess.ID)
	names, err := w.ReadDiscoveredTools(ctx)
	if err != nil || !reflect.DeepEqual(names, []string{"one", "two"}) {
		t.Fatalf("reopened=%v %v", names, err)
	}
	other, err := s.Create(dir, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if names, err := s.ReadDiscoveredTools(ctx, other.ID); err != nil || len(names) != 0 {
		t.Fatalf("state leaked to other session=%v %v", names, err)
	}
	if _, err := s.TruncateFrom(ctx, sess.ID, 1); err != nil {
		t.Fatal(err)
	}
	if names, err := w.ReadDiscoveredTools(ctx); err != nil || len(names) != 0 {
		t.Fatalf("rewind retained discoveries=%v %v", names, err)
	}
	if err := w.SaveDiscoveredTools(ctx, []string{"one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE session_tool_discovery SET names_json='{' WHERE session_id=?", sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReadDiscoveredTools(ctx); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	if _, err := s.db.Exec("DELETE FROM sessions WHERE id=?", sess.ID); err != nil {
		t.Fatal(err)
	}
	if names, err := w.ReadDiscoveredTools(ctx); err != nil || len(names) != 0 {
		t.Fatalf("deleted session retained discoveries=%v %v", names, err)
	}
}
