package session

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDialogueExcerptOrderAndSelection(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sid := excerptSession(t, s)
	for i := 0; i < 40; i++ {
		role, content := "assistant", fmt.Sprintf("line-%02d", i)
		if i%3 == 0 {
			role = "tool"
		}
		if i == 1 || i == 7 || i == 37 {
			role = "user"
		}
		if i == 1 || i == 38 {
			content = "skip"
		}
		if err := s.AppendMessage(ctx, sid, Encoded{Role: role, Content: content, ToolCallID: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendMessage(ctx, excerptSession(t, s), Encoded{Role: "user", Content: "foreign"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ReadMessages(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	accept := func(m Encoded) bool { return m.Content != "skip" }
	for _, limit := range []int{1, 2, 8, 100, 0, -1, 1000} {
		n := limit
		if n <= 0 {
			n = 8
		}
		if n > 500 {
			n = 500
		}
		var eligible []Encoded
		first := -1
		for _, m := range all {
			if (m.Role == "user" || m.Role == "assistant") && accept(m) {
				if first < 0 && m.Role == "user" {
					first = len(eligible)
				}
				eligible = append(eligible, m)
			}
		}
		start := max(0, len(eligible)-n)
		want := append([]Encoded{}, eligible[start:]...)
		if first >= 0 && first < start {
			want = append([]Encoded{eligible[first]}, want...)
		}
		got, err := s.ReadDialogueExcerpt(ctx, sid, limit, accept)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("limit %d got=%+v want=%+v err=%v", limit, got, want, err)
		}
	}
}

func TestDialogueExcerptStopsEarlyAndCancels(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sid := excerptSession(t, s)
	for i := 0; i < 50; i++ {
		if err := s.AppendMessage(ctx, sid, Encoded{Role: "user", Content: "readable"}); err != nil {
			t.Fatal(err)
		}
	}
	visits := 0
	rows, err := s.ReadDialogueExcerpt(ctx, sid, 8, func(Encoded) bool { visits++; return true })
	if err != nil || len(rows) != 9 || visits != 9 {
		t.Fatalf("rows=%d visits=%d err=%v", len(rows), visits, err)
	}
	rows, err = s.ReadDialogueExcerpt(ctx, "missing", 8, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing: %+v %v", rows, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.ReadDialogueExcerpt(canceled, sid, 8, nil); err == nil {
		t.Fatal("canceled read succeeded")
	}
	// Cancellation while skipping unusable entries must not return partial success.
	during, stop := context.WithCancel(ctx)
	defer stop()
	if _, err = s.ReadDialogueExcerpt(during, sid, 8, func(Encoded) bool { stop(); return false }); err == nil {
		t.Fatal("cancellation during scan succeeded")
	}
	if _, err = s.ReadDialogueExcerpt(ctx, sid, 8, nil); err != nil {
		t.Fatalf("transaction leaked after cancellation: %v", err)
	}
}

func TestDialogueExcerptUsesOneSnapshot(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sid := excerptSession(t, s)
	for i := 0; i < 12; i++ {
		if err := s.AppendMessage(ctx, sid, Encoded{Role: "user", Content: fmt.Sprintf("old-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	changed := false
	got, err := s.ReadDialogueExcerpt(ctx, sid, 8, func(Encoded) bool {
		if !changed {
			changed = true
			// Force a writer between reading the tail and reading the first user.
			// WAL lets the reader retain the original snapshot on its connection.
			if _, err := s.db.ExecContext(ctx, "UPDATE messages SET content='new-first' WHERE session_id=? AND seq=1", sid); err != nil {
				t.Fatal(err)
			}
		}
		return true
	})
	if err != nil || len(got) != 9 || got[0].Content != "old-0" {
		t.Fatalf("mixed snapshot: %+v %v", got, err)
	}
	fresh, err := s.ReadDialogueExcerpt(ctx, sid, 8, nil)
	if err != nil || !strings.HasPrefix(fresh[0].Content, "new-") {
		t.Fatalf("fresh snapshot missing: %+v %v", fresh, err)
	}
}

func excerptSession(t *testing.T, s *Store) string {
	t.Helper()
	sess, err := s.Create(t.TempDir(), "echo-test", "excerpt")
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}
