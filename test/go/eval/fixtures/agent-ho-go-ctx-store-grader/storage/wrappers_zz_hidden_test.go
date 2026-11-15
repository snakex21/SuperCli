package storage_test

import (
	"context"
	"errors"
	"testing"

	"blobhub/storage"
	"blobhub/storage/disk"
	"blobhub/storage/faulty"
	"blobhub/storage/memory"
	"blobhub/storage/metered"
	"blobhub/storage/prefixed"
	"blobhub/storage/readonly"
	"blobhub/storage/tiered"
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

func TestLeafStoresHonourCancellation(t *testing.T) {
	m := memory.New()
	_ = m.Put("k", []byte("v"))
	dk := disk.Store{Dir: t.TempDir()}
	_ = dk.Put("k", []byte("v"))
	for name, s := range map[string]storage.Store{"memory": m, "disk": dk} {
		if b, err := s.Get(tagged(), "k"); err != nil || string(b) != "v" {
			t.Fatalf("%s: Get = %q, %v", name, b, err)
		}
		if _, err := s.Get(cancelled(), "k"); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: Get with cancelled ctx = %v, want context.Canceled", name, err)
		}
	}
}

func TestWrappersForwardContext(t *testing.T) {
	cases := map[string]func(inner storage.Store) storage.Store{
		"prefixed": func(in storage.Store) storage.Store { return prefixed.Store{Inner: in, Prefix: "p/"} },
		"metered":  func(in storage.Store) storage.Store { return &metered.Store{Inner: in} },
		"readonly": func(in storage.Store) storage.Store { return readonly.Store{Inner: in} },
		"faulty":   func(in storage.Store) storage.Store { return &faulty.Store{Inner: in, N: 0} },
	}
	for name, wrap := range cases {
		inner := &spy{}
		_ = inner.Put("p/k", []byte("v"))
		_ = inner.Put("k", []byte("v"))
		s := wrap(inner)
		if _, err := s.Get(tagged(), "k"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkSeen(t, inner, 1)
		if _, err := s.Get(cancelled(), "k"); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: Get with cancelled ctx = %v, want context.Canceled", name, err)
		}
		checkSeen(t, inner, 1)
	}
}

func TestMeteredDoesNotCountCancelledReads(t *testing.T) {
	m := &metered.Store{Inner: memory.New()}
	_, _ = m.Get(tagged(), "nope")
	_, _ = m.Get(cancelled(), "nope")
	if m.Reads.Load() != 1 || m.Misses.Load() != 1 {
		t.Fatalf("reads=%d misses=%d, want 1/1", m.Reads.Load(), m.Misses.Load())
	}
}

func TestFaultyDoesNotCountCancelledReads(t *testing.T) {
	f := &faulty.Store{Inner: memory.New(), N: 2}
	_ = f.Put("k", []byte("v"))
	if _, err := f.Get(tagged(), "k"); err != nil {
		t.Fatal(err)
	}
	_, _ = f.Get(cancelled(), "k")
	if _, err := f.Get(tagged(), "k"); !errors.Is(err, faulty.ErrInjected) {
		t.Fatalf("second real read should fail with ErrInjected, got %v", err)
	}
}

func TestTieredForwardsToBothTiers(t *testing.T) {
	fast, slow := &spy{}, &spy{}
	_ = slow.Put("k", []byte("v"))
	s := tiered.Store{Fast: fast, Slow: slow}
	if b, err := s.Get(tagged(), "k"); err != nil || string(b) != "v" {
		t.Fatalf("Get = %q, %v", b, err)
	}
	checkSeen(t, fast, 1)
	checkSeen(t, slow, 1)
	if _, err := s.Get(cancelled(), "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	checkSeen(t, fast, 1)
	checkSeen(t, slow, 1)
}
