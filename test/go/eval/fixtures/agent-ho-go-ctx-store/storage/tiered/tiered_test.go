package tiered

import (
	"testing"

	"blobhub/storage/memory"
)

func TestBackfill(t *testing.T) {
	fast, slow := memory.New(), memory.New()
	_ = slow.Put("k", []byte("v"))
	s := Store{Fast: fast, Slow: slow}
	if b, err := s.Get("k"); err != nil || string(b) != "v" {
		t.Fatalf("Get = %q, %v", b, err)
	}
	if b, err := fast.Get("k"); err != nil || string(b) != "v" {
		t.Fatalf("not back-filled: %q, %v", b, err)
	}
}
