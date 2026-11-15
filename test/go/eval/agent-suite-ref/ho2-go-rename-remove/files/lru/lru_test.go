package lru

import "testing"

func TestPutGetEvict(t *testing.T) {
	var gone []string
	s := New(2)
	s.OnEvict = func(k string) { gone = append(gone, k) }
	s.Put("a", []byte("1"))
	s.Put("b", []byte("2"))
	s.Get("a")
	s.Put("c", []byte("3")) // drops b
	if _, ok := s.Get("b"); ok {
		t.Fatal("b should be gone")
	}
	if !s.Remove("a") {
		t.Fatal("a was present")
	}
	if s.Remove("missing") {
		t.Fatal("missing was absent")
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d", s.Len())
	}
	s.EvictAll()
	if s.Len() != 0 || len(gone) != 3 {
		t.Fatalf("len=%d gone=%v", s.Len(), gone)
	}
}
