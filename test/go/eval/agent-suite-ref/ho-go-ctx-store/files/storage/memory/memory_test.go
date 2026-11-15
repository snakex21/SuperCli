package memory

import (
	"context"
	"errors"
	"testing"

	"blobhub/storage"
)

func TestRoundTrip(t *testing.T) {
	s := New()
	if err := s.Put("a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), "a")
	if err != nil || string(got) != "1" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	_ = s.Delete("a")
	if _, err := s.Get(context.Background(), "a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
