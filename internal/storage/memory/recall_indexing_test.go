package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type heldIndexEmbedder struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   []string
}

func (e *heldIndexEmbedder) Name() string { return "held-index" }
func (e *heldIndexEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.calls = append(e.calls, text)
	e.mu.Unlock()
	if text == "fresh needle note" {
		e.once.Do(func() { close(e.started) })
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []float32{1, 0}, nil
}
func TestRecallDoesNotWaitForBackgroundIndexing(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := &heldIndexEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s.SetEmbedder(e)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(e.release) }) }
	defer release()
	if err := s.Put(Entry{ID: "fresh", Scope: ScopeFact, Content: "fresh needle note"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("indexer did not start")
	}
	done := make(chan []Entry, 1)
	go func() { got, _ := s.RecallSearch(context.Background(), "needle", 5); done <- got }()
	select {
	case got := <-done:
		if len(got) != 1 || got[0].ID != "fresh" {
			t.Fatalf("fresh keyword result missing: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("recall waited for unrelated background embeddings")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.RecallSearch(cancelled, "needle", 5); err != context.Canceled {
		t.Fatalf("cancelled recall: %v", err)
	}
	e.mu.Lock()
	calls := len(e.calls)
	e.mu.Unlock()
	if calls != 1 {
		t.Fatalf("foreground recall joined the embedding backend: %d calls", calls)
	}
	release()
	s.flushEmbedQueue()
	if lexical, err := s.Search("semantic-only-query", 5); err != nil || len(lexical) != 0 {
		t.Fatalf("semantic test must have no lexical matches: %+v %v", lexical, err)
	}
	got, err := s.RecallSearch(context.Background(), "semantic-only-query", 5)
	if err != nil || len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("semantic recall did not resume after indexing: %+v %v", got, err)
	}
}

func TestQueuedMemoryUpdatesEmbedOnlyLatestVersion(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := &heldIndexEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s.SetEmbedder(e)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(e.release) }) }
	defer release()
	if err := s.Put(Entry{ID: "first", Scope: ScopeFact, Content: "fresh needle note"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("indexer did not start")
	}
	for i := 0; i < 30; i++ {
		if err := s.Put(Entry{ID: "changing", Scope: ScopeFact, Content: fmt.Sprintf("version %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Put(Entry{ID: "independent", Scope: ScopeFact, Content: "another note"}); err != nil {
		t.Fatal(err)
	}
	release()
	s.flushEmbedQueue()
	e.mu.Lock()
	calls := append([]string(nil), e.calls...)
	e.mu.Unlock()
	if len(calls) != 3 || calls[0] != "fresh needle note" || calls[1] != "version 29" || calls[2] != "another note" {
		t.Fatalf("embedded superseded notes or lost independent note: %v", calls)
	}
	// The next batch must not inherit positions from the previous one.
	if err := s.Put(Entry{ID: "changing", Scope: ScopeFact, Content: "version 30"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HybridSearch(context.Background(), "version", 5); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.calls[len(e.calls)-2] != "version 30" || e.calls[len(e.calls)-1] != "version" {
		t.Fatalf("new batch or search query lost: %v", e.calls)
	}
}
