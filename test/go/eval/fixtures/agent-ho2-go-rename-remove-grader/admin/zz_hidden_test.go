package admin

import (
	"testing"

	"slab/lru"
)

func TestHiddenPurgeCount(t *testing.T) {
	s := lru.New(8)
	s.Put("a", nil)
	s.Put("b", nil)
	s.Put("c", nil)
	if n := PurgeKeys(s, []string{"a", "x", "c", "a"}); n != 2 {
		t.Fatalf("PurgeKeys = %d, want 2", n)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d", s.Len())
	}
	if n := PurgeKeys(s, nil); n != 0 {
		t.Fatal(n)
	}
}
