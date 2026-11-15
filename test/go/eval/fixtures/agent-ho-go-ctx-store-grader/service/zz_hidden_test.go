package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"blobhub/cache"
	"blobhub/service/audit"
	"blobhub/service/httpapi"
	"blobhub/service/replicate"
	"blobhub/service/thumbs"
	"blobhub/storage"
	"blobhub/storage/memory"
)

type ctxKey struct{}

// spy is a storage.Store that records the context of every Get.
type spy struct {
	blobs map[string][]byte
	seen  []any
}

func (s *spy) Get(ctx context.Context, key string) ([]byte, error) {
	s.seen = append(s.seen, ctx.Value(ctxKey{}))
	if b, ok := s.blobs[key]; ok {
		return b, nil
	}
	return nil, storage.ErrNotFound
}

func (s *spy) Put(key string, data []byte) error {
	if s.blobs == nil {
		s.blobs = map[string][]byte{}
	}
	s.blobs[key] = data
	return nil
}

func (s *spy) Delete(key string) error { delete(s.blobs, key); return nil }

func tagged() context.Context { return context.WithValue(context.Background(), ctxKey{}, "trace-42") }

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(tagged())
	cancel()
	return ctx
}

func checkSeen(t *testing.T, s *spy, want int) {
	t.Helper()
	if len(s.seen) != want {
		t.Fatalf("inner Get called %d times, want %d", len(s.seen), want)
	}
	for _, v := range s.seen {
		if v != "trace-42" {
			t.Fatalf("inner Get got a different context (value %v); the caller's ctx must be forwarded", v)
		}
	}
}

func TestThumbsForwardsContext(t *testing.T) {
	s := &spy{}
	_ = s.Put("a.png", []byte("xyz"))
	r := thumbs.Renderer{Store: s, Cache: cache.NewLRU(2)}
	if b, err := r.Render(tagged(), "a.png"); err != nil || string(b) != "thumb(3 bytes)" {
		t.Fatalf("Render = %q, %v", b, err)
	}
	checkSeen(t, s, 1)
}

func TestReplicateForwardsContext(t *testing.T) {
	src := &spy{}
	_ = src.Put("a", []byte("1"))
	_ = src.Put("b", []byte("2"))
	dst := memory.New()
	if n, err := replicate.Copy(tagged(), src, dst, []string{"a", "b"}); err != nil || n != 2 {
		t.Fatalf("Copy = %d, %v", n, err)
	}
	checkSeen(t, src, 2)
}

func TestHTTPHandlerUsesRequestContext(t *testing.T) {
	s := &spy{}
	_ = s.Put("k", []byte("v"))
	req := httptest.NewRequest(http.MethodGet, "/blobs/k", nil).WithContext(tagged())
	rec := httptest.NewRecorder()
	httpapi.Handler{Store: s}.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "v" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	checkSeen(t, s, 1)
}

func TestAuditStillWorks(t *testing.T) {
	m := memory.New()
	_ = m.Put("a", nil)
	got, err := audit.Missing(m, []string{"c", "a", "b"})
	if err != nil || len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("Missing = %v, %v", got, err)
	}
	var _ func(storage.Store, []string) ([]string, error) = audit.Missing
}
