package session

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
	"supercli/internal/llm"
)

func TestAppendMessageConcurrentWritersKeepEveryMessage(t *testing.T) {
	s := openTestStore(t)
	sess, err := s.Create(t.TempDir(), "m", "")
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	start := make(chan struct{})
	results := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			enc, err := FromMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("message-%d", i)})
			if err == nil {
				err = s.AppendMessage(context.Background(), sess.ID, enc)
			}
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent append: %v", err)
		}
	}
	rows, err := s.ReadMessages(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != count {
		t.Fatalf("stored %d/%d messages", len(rows), count)
	}
	seen := make(map[string]bool)
	for i, row := range rows {
		if row.Seq != i+1 || seen[row.Content] {
			t.Fatalf("sequence/duplicate at row %d: %+v", i, row)
		}
		seen[row.Content] = true
	}
	metadata, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.MessageCount != count {
		t.Fatalf("count = %d, want %d", metadata.MessageCount, count)
	}
}

func TestAppendMessageCanceledContextWritesNothing(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create(t.TempDir(), "m", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.AppendMessage(ctx, sess.ID, mustEncoded(t, llm.Message{Role: llm.RoleUser, Content: "canceled"}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("append error = %v, want canceled", err)
	}
	rows, err := s.ReadMessages(context.Background(), sess.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("canceled append wrote messages: %+v, %v", rows, err)
	}
	metadata, err := s.Get(sess.ID)
	if err != nil || metadata.MessageCount != 0 {
		t.Fatalf("canceled append bumped metadata: %+v, %v", metadata, err)
	}
}

func TestAppendMessageFailedInsertRollsBackMetadata(t *testing.T) {
	s := openTestStore(t)
	sess, _ := s.Create(t.TempDir(), "m", "")
	if _, err := s.db.Exec(`CREATE TRIGGER reject_test_message BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT, 'fixture rejected insert'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.AppendMessage(context.Background(), sess.ID, mustEncoded(t, llm.Message{Role: llm.RoleUser, Content: "rejected"}))
	if err == nil {
		t.Fatal("rejected insert succeeded")
	}
	metadata, err := s.Get(sess.ID)
	if err != nil || metadata.MessageCount != 0 {
		t.Fatalf("failed insert bumped metadata: %+v, %v", metadata, err)
	}
}

func TestReadModelContextKeepsOneSnapshotAcrossTruncate(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	function := fmt.Sprintf("projection_snapshot_%d", time.Now().UnixNano())
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		if first.CompareAndSwap(false, true) {
			close(reached)
			<-release
		}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	s := openTestStore(t)
	sess, _ := s.Create(t.TempDir(), "m", "")
	old := []llm.Message{{Role: llm.RoleUser, Content: "first"}, {Role: llm.RoleAssistant, Content: "old answer"}}
	for _, msg := range old {
		if err := s.AppendMessage(context.Background(), sess.ID, mustEncoded(t, msg)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveContextProjection(context.Background(), sess.ID, old); err != nil {
		t.Fatal(err)
	}
	// A view pauses the real projection SELECT while the test changes the tail.
	// Production code gets no hooks, and the selected JSON remains unchanged.
	for _, query := range []string{
		"ALTER TABLE session_context_projections RENAME TO snapshot_projection_rows",
		fmt.Sprintf(`CREATE VIEW session_context_projections AS SELECT session_id,through_seq,messages_json || %s() AS messages_json,updated_at FROM snapshot_projection_rows`, function),
		`CREATE TRIGGER snapshot_projection_delete INSTEAD OF DELETE ON session_context_projections BEGIN DELETE FROM snapshot_projection_rows WHERE session_id=OLD.session_id; END`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		messages []llm.Message
		err      error
	}
	done := make(chan result, 1)
	go func() { messages, err := s.ReadModelContext(ctx, sess.ID); done <- result{messages, err} }()
	select {
	case <-reached:
	case <-ctx.Done():
		close(release)
		<-done
		t.Fatal("projection SELECT did not reach barrier")
	}
	mutation := make(chan error, 1)
	go func() {
		if _, err := s.TruncateFrom(ctx, sess.ID, 2); err != nil {
			mutation <- err
			return
		}
		for _, msg := range []llm.Message{{Role: llm.RoleUser, Content: "new question"}, {Role: llm.RoleAssistant, Content: "new answer"}} {
			enc, err := FromMessage(msg)
			if err == nil {
				err = s.AppendMessage(ctx, sess.ID, enc)
			}
			if err != nil {
				mutation <- err
				return
			}
		}
		mutation <- nil
	}()
	select {
	case err := <-mutation:
		if err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
	case <-ctx.Done():
		close(release)
		<-done
		t.Fatal("read snapshot blocked writer")
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.messages) != 2 || got.messages[0].Content != "first" || got.messages[1].Content != "old answer" {
		t.Fatalf("mixed old/new branches: %+v", got.messages)
	}
	fresh, err := s.ReadModelContext(context.Background(), sess.ID)
	if err != nil || len(fresh) != 3 || fresh[1].Content != "new question" || fresh[2].Content != "new answer" {
		t.Fatalf("fresh read missed replacement branch: %+v, %v", fresh, err)
	}
}
