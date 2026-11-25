package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// A rewind after the compaction point keeps the compacted projection (the
// audited session recompacted ~612K tokens three times, ~24 s each); a
// rewind into the compacted part drops it.
func TestRewindKeepsAProjectionThatEndsBeforeIt(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	sess, _ := s.Create("/w", "m", "t")
	add := func(n int) {
		for i := 0; i < n; i++ {
			enc, _ := FromMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("message %d", i)})
			if err := s.AppendMessage(ctx, sess.ID, enc); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(10)
	summary := []llm.Message{{Role: llm.RoleUser, Content: "Earlier context was compacted. STATE_NOTE"}}
	if err := s.SaveContextProjection(ctx, sess.ID, summary); err != nil {
		t.Fatal(err)
	}
	add(5) // seq 11..15
	if _, err := s.TruncateFrom(ctx, sess.ID, 13); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.ReadModelContext(ctx, sess.ID)
	if err != nil || len(msgs) != 3 || !strings.Contains(msgs[0].Content, "STATE_NOTE") {
		t.Fatalf("projection lost by a rewind after it: %d messages, err %v", len(msgs), err)
	}
	if _, err := s.TruncateFrom(ctx, sess.ID, 5); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.ReadModelContext(ctx, sess.ID)
	if len(msgs) != 4 || strings.Contains(msgs[0].Content, "STATE_NOTE") {
		t.Fatalf("a rewind into the compacted part kept the projection: %d messages", len(msgs))
	}
}

// The audited row stored by byte-cut text decodes to valid UTF-8.
func TestInvalidUTF8RowDecodesToValidText(t *testing.T) {
	m, err := Encoded{Role: "tool", ToolCallID: "c", Content: "Doprowadzi\xc5"}.ToMessage()
	if err != nil || m.Content != "Doprowadzi�" {
		t.Fatalf("decoded %q, %v", m.Content, err)
	}
}
