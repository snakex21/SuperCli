package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestMessageCountPreparationCancellationDoesNotPoisonStore(t *testing.T) {
	s := openTestStore(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadMessageCounts(canceled, "future"); err != context.Canceled {
		t.Fatalf("first cancellation: %v", err)
	}
	if err := s.EnsureSession("future", "/project", "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(context.Background(), "future", Encoded{Role: "user", Content: "new"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadMessageCounts(context.Background(), "future")
	if err != nil || got != (MessageCounts{User: 1}) {
		t.Fatalf("retry: %+v %v", got, err)
	}
}
func TestMessageCountStatementConcurrentSessionsRemainFresh(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		id := fmt.Sprint(i)
		if err := s.EnsureSession(id, "/project", "fixture"); err != nil {
			t.Fatal(err)
		}
		for j := 0; j <= i; j++ {
			if err := s.AppendMessage(ctx, id, Encoded{Role: "user", Content: "text"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := s.ReadMessageCounts(ctx, fmt.Sprint(i))
			if err != nil {
				errors <- err
			} else if got != (MessageCounts{User: i + 1}) {
				errors <- fmt.Errorf("session %d got %+v", i, got)
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET content='' WHERE session_id='7'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadMessageCounts(ctx, "7")
	if err != nil || got != (MessageCounts{}) {
		t.Fatalf("stale cached result: %+v %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadMessageCounts(ctx, "7"); err == nil {
		t.Fatal("count query survived a closed store")
	}
}
func TestMessageCountAggregatePreservesTypedLegacyValues(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.EnsureSession("legacy", "/project", "fixture"); err != nil {
		t.Fatal(err)
	}
	// BLOB role/content/id values are legal legacy SQLite rows. An empty BLOB
	// tool id must be rejected exactly like an empty decoded string.
	for i, row := range []struct{ role, content, id any }{
		{[]byte("user"), []byte{0}, ""}, {"user", []byte{}, ""},
		{"tool", "", []byte{}}, {"tool", "", []byte("id")}, {"tool", "", 0},
		{"USER", "text", ""}, {"assistant", " ", ""},
	} {
		if _, err := s.db.Exec(`INSERT INTO messages(session_id,seq,role,content,tool_call_id,created_at) VALUES('legacy',?,?,?,?,1)`, i+1, row.role, row.content, row.id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ReadMessageCounts(ctx, "legacy")
	want, refErr := s.readMessageCountsReference(ctx, "legacy")
	if err != nil || refErr != nil || got != want || got != (MessageCounts{User: 1, Assistant: 1, Tool: 2}) {
		t.Fatalf("got=%+v want=%+v errors=%v/%v", got, want, err, refErr)
	}
}
