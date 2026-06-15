package memory

import (
	"testing"
	"time"
)

func TestNewMemStore_Empty(t *testing.T) {
	s := NewMemStore()
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
}

func TestMemStore_Put_Get(t *testing.T) {
	s := NewMemStore()
	if err := s.Put(Entry{ID: "a", Content: "alpha"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	got, ok := s.Get("a")
	if !ok {
		t.Fatal("Get(a) not found")
	}
	if got.Content != "alpha" {
		t.Fatalf("Get(a) content = %q, want alpha", got.Content)
	}
	if got.Scope != "general" {
		t.Errorf("default scope = %q, want general", got.Scope)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not set on Put")
	}
}

func TestMemStore_Put_RejectsEmptyID(t *testing.T) {
	s := NewMemStore()
	if err := s.Put(Entry{Content: "x"}); err == nil {
		t.Fatal("expected error on empty id")
	}
}

func TestMemStore_Put_RejectsEmptyContent(t *testing.T) {
	s := NewMemStore()
	if err := s.Put(Entry{ID: "a"}); err == nil {
		t.Fatal("expected error on empty content")
	}
}

func TestMemStore_Put_Replaces(t *testing.T) {
	s := NewMemStore()
	s.Put(Entry{ID: "a", Content: "first"})
	s.Put(Entry{ID: "a", Content: "second"})
	got, _ := s.Get("a")
	if got.Content != "second" {
		t.Fatalf("content = %q, want second", got.Content)
	}
}

func TestMemStore_Put_PreservesCreatedAt(t *testing.T) {
	s := NewMemStore()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.Put(Entry{ID: "a", Content: "x", CreatedAt: when})
	got, _ := s.Get("a")
	if !got.CreatedAt.Equal(when) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, when)
	}
}

func TestMemStore_Delete(t *testing.T) {
	s := NewMemStore()
	s.Put(Entry{ID: "a", Content: "x"})
	if !s.Delete("a") {
		t.Fatal("Delete(a) returned false")
	}
	if s.Delete("a") {
		t.Fatal("second Delete(a) returned true")
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("entry still present after delete")
	}
}

func TestMemStore_Search_Content(t *testing.T) {
	s := NewMemStore()
	s.Put(Entry{ID: "1", Content: "the quick brown fox"})
	s.Put(Entry{ID: "2", Content: "lazy dog"})
	s.Put(Entry{ID: "3", Content: "FOX jumps"})
	hits := s.Search("fox", 10)
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
}

func TestMemStore_Search_Tag(t *testing.T) {
	s := NewMemStore()
	s.Put(Entry{ID: "1", Content: "no match", Tags: []string{"urgent"}})
	hits := s.Search("URGENT", 10)
	if len(hits) != 1 {
		t.Fatalf("tag hits = %d, want 1", len(hits))
	}
}

func TestMemStore_Search_EmptyQuery(t *testing.T) {
	s := NewMemStore()
	s.Put(Entry{ID: "1", Content: "x"})
	if hits := s.Search("", 10); hits != nil {
		t.Fatalf("hits = %v, want nil on empty query", hits)
	}
}

func TestMemStore_Search_Limit(t *testing.T) {
	s := NewMemStore()
	for i := 0; i < 10; i++ {
		s.Put(Entry{ID: idn(i), Content: "common word"})
	}
	hits := s.Search("common", 3)
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3 (limit)", len(hits))
	}
}

func idn(i int) string {
	return "e" + string(rune('0'+i))
}
