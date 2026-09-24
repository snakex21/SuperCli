package session

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

func TestContextModelMetadataSurvivesReopenAndIgnoresPicker(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Create(dir, "picker", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w := NewWriter(s, sess.ID)
	provider, model, err := w.ReadContextModel(ctx)
	if err != nil || provider != "" || model != "" {
		t.Fatalf("new identity: %q/%q %v", provider, model, err)
	}
	if err := w.SaveContextModel(ctx, "cloud", "actual"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE sessions SET model='unused-picker-choice' WHERE id=?", sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w = NewWriter(s, sess.ID)
	provider, model, err = w.ReadContextModel(ctx)
	if err != nil || provider != "cloud" || model != "actual" {
		t.Fatalf("reopened identity: %q/%q %v", provider, model, err)
	}
	if _, err := s.TruncateFrom(ctx, sess.ID, 1); err != nil {
		t.Fatal(err)
	}
	provider, model, err = w.ReadContextModel(ctx)
	if err != nil || provider != "" || model != "" {
		t.Fatalf("truncated identity: %q/%q %v", provider, model, err)
	}
	if err := w.SaveContextModel(ctx, "local", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM sessions WHERE id=?", sess.ID); err != nil {
		t.Fatal(err)
	}
	provider, model, err = w.ReadContextModel(ctx)
	if err != nil || provider != "" || model != "" {
		t.Fatalf("deleted identity: %q/%q %v", provider, model, err)
	}
}
