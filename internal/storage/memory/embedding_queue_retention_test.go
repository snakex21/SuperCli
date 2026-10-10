package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"
	"weak"
)

type contentDigestEmbedder struct {
	mu      sync.Mutex
	digests [][32]byte
}

func (*contentDigestEmbedder) Name() string { return "content-digest-fixture" }
func (e *contentDigestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.digests = append(e.digests, sha256.Sum256([]byte(text)))
	e.mu.Unlock()
	return []float32{1, 0}, nil
}

// Return only a weak pointer and hash; the helper frame cannot accidentally
// keep the original string alive when the completed queue is checked.
//
//go:noinline
func putWeakQueueNote(t *testing.T, s *Store, id string) (weak.Pointer[byte], [32]byte) {
	t.Helper()
	content := strings.Repeat(id+"-", 800)
	if len(content) > MaxEntryContentBytes {
		t.Fatal("fixture exceeds entry budget")
	}
	ref := weak.Make(unsafe.StringData(content))
	digest := sha256.Sum256([]byte(content))
	if err := s.Put(Entry{ID: id, Scope: ScopeFact, Content: content, Tags: []string{"queued"}}); err != nil {
		t.Fatal(err)
	}
	return ref, digest
}

func TestFlushedEmbeddingQueueReleasesSourceContent(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%v", disabled), func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			emb := &contentDigestEmbedder{}
			s.SetEmbedder(emb)
			// Block indexing while real Put operations enqueue the complete fixture.
			// The worker waits on this mutex; no sleeps or progress polling are used.
			s.embedWorkMu.Lock()
			locked := true
			defer func() {
				if locked {
					s.embedWorkMu.Unlock()
				}
			}()
			a, ha := putWeakQueueNote(t, s, "first")
			b, hb := putWeakQueueNote(t, s, "second")
			if disabled {
				s.SetEmbedder(nil)
			}
			s.embedWorkMu.Unlock()
			locked = false
			s.flushEmbedQueue()
			emb.mu.Lock()
			got := append([][32]byte(nil), emb.digests...)
			emb.mu.Unlock()
			if disabled {
				if len(got) != 0 {
					t.Fatalf("disabled backend called %d times", len(got))
				}
			} else if len(got) != 2 || got[0] != ha || got[1] != hb {
				t.Fatalf("indexing lost source content or order: calls=%d", len(got))
			}
			runtime.GC()
			runtime.GC()
			if a.Value() != nil || b.Value() != nil {
				t.Fatal("completed queue retains original note strings")
			}
			// Releasing queue references must preserve durable note and vector data.
			for _, id := range []string{"first", "second"} {
				note, err := s.Get(id)
				if err != nil || note.Content == "" {
					t.Fatalf("durable note lost: %s, %v", id, err)
				}
			}
			var vectors int
			if err := s.db.QueryRow("SELECT count(*) FROM memory_vectors").Scan(&vectors); err != nil {
				t.Fatal(err)
			}
			want := 2
			if disabled {
				want = 0
			}
			if vectors != want {
				t.Fatalf("vectors=%d, want %d", vectors, want)
			}
			runtime.KeepAlive(s)
		})
	}
}

func BenchmarkEmbeddingQueueDrain(b *testing.B) {
	// Measure only production dequeue/disabled-backend drain. A backend switch
	// may disable vectors with an already queued batch; its source notes are
	// durable independently. Queue construction is outside the timed region.
	entries := make([]Entry, 256)
	for i := range entries {
		entries[i] = Entry{ID: fmt.Sprintf("note-%03d", i), Scope: ScopeFact, Content: strings.Repeat("note ", 2000)}
	}
	s := &Store{embedQueue: make([]Entry, 0, len(entries))}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		s.embedQueue = append(s.embedQueue[:0], entries...)
		b.StartTimer()
		s.flushEmbedQueue()
	}
	runtime.KeepAlive(entries)
	runtime.KeepAlive(s)
}
