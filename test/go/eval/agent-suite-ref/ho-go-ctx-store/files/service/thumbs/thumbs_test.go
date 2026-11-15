package thumbs

import (
	"context"
	"testing"

	"blobhub/cache"
	"blobhub/storage/memory"
)

func TestRenderCaches(t *testing.T) {
	s := memory.New()
	_ = s.Put("cat.png", []byte("12345"))
	r := Renderer{Store: s, Cache: cache.NewLRU(4)}
	b, err := r.Render(context.Background(), "cat.png")
	if err != nil || string(b) != "thumb(5 bytes)" {
		t.Fatalf("Render = %q, %v", b, err)
	}
	_ = s.Delete("cat.png")
	if _, err := r.Render(context.Background(), "cat.png"); err != nil {
		t.Fatalf("cached render failed: %v", err)
	}
	if _, err := s.Get(context.Background(), "cat.png"); err == nil {
		t.Fatal("expected miss")
	}
}
