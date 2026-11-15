"""Go: thread context.Context through one interface method, all implementations and call sites."""
import textwrap

from common import Task, d, cmd, go_filler, GO_MOD

MOD = "blobhub"

FX = {
    "storage/store.go": '''
        // Package storage defines the blob store abstraction.
        package storage

        import "errors"

        // ErrNotFound is returned by Get for unknown keys.
        var ErrNotFound = errors.New("storage: not found")

        // ErrReadOnly is returned by stores that reject writes.
        var ErrReadOnly = errors.New("storage: read-only")

        // Store is a key/value blob store.
        type Store interface {
        	// Get returns the blob stored under key or ErrNotFound.
        	Get(key string) ([]byte, error)
        	// Put stores data under key, replacing any previous blob.
        	Put(key string, data []byte) error
        	// Delete removes key; deleting a missing key is not an error.
        	Delete(key string) error
        }
        ''',
    "storage/memory/memory.go": '''
        // Package memory is an in-memory storage.Store.
        package memory

        import (
        	"sync"

        	"blobhub/storage"
        )

        // Store keeps blobs in a map.
        type Store struct {
        	mu    sync.RWMutex
        	blobs map[string][]byte
        }

        // New returns an empty store.
        func New() *Store { return &Store{blobs: map[string][]byte{}} }

        // Get implements storage.Store.
        func (s *Store) Get(key string) ([]byte, error) {
        	s.mu.RLock()
        	defer s.mu.RUnlock()
        	b, ok := s.blobs[key]
        	if !ok {
        		return nil, storage.ErrNotFound
        	}
        	return append([]byte(nil), b...), nil
        }

        // Put implements storage.Store.
        func (s *Store) Put(key string, data []byte) error {
        	s.mu.Lock()
        	defer s.mu.Unlock()
        	s.blobs[key] = append([]byte(nil), data...)
        	return nil
        }

        // Delete implements storage.Store.
        func (s *Store) Delete(key string) error {
        	s.mu.Lock()
        	defer s.mu.Unlock()
        	delete(s.blobs, key)
        	return nil
        }
        ''',
    "storage/memory/memory_test.go": '''
        package memory

        import (
        	"errors"
        	"testing"

        	"blobhub/storage"
        )

        func TestRoundTrip(t *testing.T) {
        	s := New()
        	if err := s.Put("a", []byte("1")); err != nil {
        		t.Fatal(err)
        	}
        	got, err := s.Get("a")
        	if err != nil || string(got) != "1" {
        		t.Fatalf("Get = %q, %v", got, err)
        	}
        	_ = s.Delete("a")
        	if _, err := s.Get("a"); !errors.Is(err, storage.ErrNotFound) {
        		t.Fatalf("after delete: %v", err)
        	}
        }
        ''',
    "storage/disk/disk.go": '''
        // Package disk stores blobs as files in a directory.
        package disk

        import (
        	"encoding/hex"
        	"errors"
        	"io/fs"
        	"os"
        	"path/filepath"

        	"blobhub/storage"
        )

        // Store writes one file per key (hex-encoded name) under Dir.
        type Store struct {
        	Dir string
        }

        func (s Store) path(key string) string {
        	return filepath.Join(s.Dir, hex.EncodeToString([]byte(key)))
        }

        // Get implements storage.Store.
        func (s Store) Get(key string) ([]byte, error) {
        	b, err := os.ReadFile(s.path(key))
        	if errors.Is(err, fs.ErrNotExist) {
        		return nil, storage.ErrNotFound
        	}
        	return b, err
        }

        // Put implements storage.Store.
        func (s Store) Put(key string, data []byte) error {
        	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
        		return err
        	}
        	return os.WriteFile(s.path(key), data, 0o644)
        }

        // Delete implements storage.Store.
        func (s Store) Delete(key string) error {
        	err := os.Remove(s.path(key))
        	if errors.Is(err, fs.ErrNotExist) {
        		return nil
        	}
        	return err
        }
        ''',
    "storage/prefixed/prefixed.go": '''
        // Package prefixed namespaces keys of another store.
        package prefixed

        import "blobhub/storage"

        // Store prepends Prefix to every key before calling Inner.
        type Store struct {
        	Inner  storage.Store
        	Prefix string
        }

        // Get implements storage.Store.
        func (s Store) Get(key string) ([]byte, error) { return s.Inner.Get(s.Prefix + key) }

        // Put implements storage.Store.
        func (s Store) Put(key string, data []byte) error { return s.Inner.Put(s.Prefix+key, data) }

        // Delete implements storage.Store.
        func (s Store) Delete(key string) error { return s.Inner.Delete(s.Prefix + key) }
        ''',
    "storage/tiered/tiered.go": '''
        // Package tiered combines a fast and a slow store.
        package tiered

        import (
        	"errors"

        	"blobhub/storage"
        )

        // Store reads Fast first, falls back to Slow and back-fills Fast.
        // Writes go to both tiers.
        type Store struct {
        	Fast, Slow storage.Store
        }

        // Get implements storage.Store.
        func (s Store) Get(key string) ([]byte, error) {
        	b, err := s.Fast.Get(key)
        	if err == nil {
        		return b, nil
        	}
        	if !errors.Is(err, storage.ErrNotFound) {
        		return nil, err
        	}
        	b, err = s.Slow.Get(key)
        	if err != nil {
        		return nil, err
        	}
        	_ = s.Fast.Put(key, b)
        	return b, nil
        }

        // Put implements storage.Store.
        func (s Store) Put(key string, data []byte) error {
        	if err := s.Slow.Put(key, data); err != nil {
        		return err
        	}
        	return s.Fast.Put(key, data)
        }

        // Delete implements storage.Store.
        func (s Store) Delete(key string) error {
        	if err := s.Fast.Delete(key); err != nil {
        		return err
        	}
        	return s.Slow.Delete(key)
        }
        ''',
    "storage/tiered/tiered_test.go": '''
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
        ''',
    "storage/metered/metered.go": '''
        // Package metered counts store operations.
        package metered

        import (
        	"errors"
        	"sync/atomic"

        	"blobhub/storage"
        )

        // Store wraps Inner and counts reads, misses and writes.
        type Store struct {
        	Inner                storage.Store
        	Reads, Misses, Writes atomic.Int64
        }

        // Get implements storage.Store.
        func (s *Store) Get(key string) ([]byte, error) {
        	s.Reads.Add(1)
        	b, err := s.Inner.Get(key)
        	if errors.Is(err, storage.ErrNotFound) {
        		s.Misses.Add(1)
        	}
        	return b, err
        }

        // Put implements storage.Store.
        func (s *Store) Put(key string, data []byte) error {
        	s.Writes.Add(1)
        	return s.Inner.Put(key, data)
        }

        // Delete implements storage.Store.
        func (s *Store) Delete(key string) error {
        	s.Writes.Add(1)
        	return s.Inner.Delete(key)
        }
        ''',
    "storage/readonly/readonly.go": '''
        // Package readonly exposes a store without write access.
        package readonly

        import "blobhub/storage"

        // Store forwards reads to Inner and rejects writes.
        type Store struct {
        	Inner storage.Store
        }

        // Get implements storage.Store.
        func (s Store) Get(key string) ([]byte, error) { return s.Inner.Get(key) }

        // Put implements storage.Store.
        func (Store) Put(string, []byte) error { return storage.ErrReadOnly }

        // Delete implements storage.Store.
        func (Store) Delete(string) error { return storage.ErrReadOnly }
        ''',
    "storage/faulty/faulty.go": '''
        // Package faulty injects read failures for resilience tests.
        package faulty

        import (
        	"errors"
        	"sync"

        	"blobhub/storage"
        )

        // ErrInjected is returned by every Nth Get.
        var ErrInjected = errors.New("faulty: injected failure")

        // Store fails every Nth Get (N <= 0 never fails).
        type Store struct {
        	Inner storage.Store
        	N     int

        	mu    sync.Mutex
        	calls int
        }

        // Get implements storage.Store.
        func (s *Store) Get(key string) ([]byte, error) {
        	s.mu.Lock()
        	s.calls++
        	fail := s.N > 0 && s.calls%s.N == 0
        	s.mu.Unlock()
        	if fail {
        		return nil, ErrInjected
        	}
        	return s.Inner.Get(key)
        }

        // Put implements storage.Store.
        func (s *Store) Put(key string, data []byte) error { return s.Inner.Put(key, data) }

        // Delete implements storage.Store.
        func (s *Store) Delete(key string) error { return s.Inner.Delete(key) }
        ''',
    "cache/lru.go": '''
        // Package cache is a small in-process LRU for rendered thumbnails.
        // It is not a storage.Store: lookups cannot fail, they only miss.
        package cache

        import "container/list"

        // Cache is the thumbnail cache contract.
        type Cache interface {
        	Get(key string) ([]byte, bool)
        	Add(key string, data []byte)
        }

        type entry struct {
        	key  string
        	data []byte
        }

        // LRU is a fixed-size least-recently-used Cache.
        type LRU struct {
        	max   int
        	order *list.List
        	items map[string]*list.Element
        }

        // NewLRU returns an LRU holding at most max entries.
        func NewLRU(max int) *LRU {
        	return &LRU{max: max, order: list.New(), items: map[string]*list.Element{}}
        }

        // Get returns the cached blob and marks it recently used.
        func (c *LRU) Get(key string) ([]byte, bool) {
        	el, ok := c.items[key]
        	if !ok {
        		return nil, false
        	}
        	c.order.MoveToFront(el)
        	return el.Value.(*entry).data, true
        }

        // Add inserts or refreshes key, evicting the oldest entry when full.
        func (c *LRU) Add(key string, data []byte) {
        	if el, ok := c.items[key]; ok {
        		el.Value.(*entry).data = data
        		c.order.MoveToFront(el)
        		return
        	}
        	c.items[key] = c.order.PushFront(&entry{key, data})
        	if c.order.Len() > c.max {
        		old := c.order.Back()
        		c.order.Remove(old)
        		delete(c.items, old.Value.(*entry).key)
        	}
        }
        ''',
    "service/thumbs/thumbs.go": '''
        // Package thumbs renders (fake) thumbnails of stored images.
        package thumbs

        import (
        	"context"
        	"fmt"

        	"blobhub/cache"
        	"blobhub/storage"
        )

        // Renderer renders thumbnails and caches the results.
        type Renderer struct {
        	Store storage.Store
        	Cache cache.Cache
        }

        // Render returns the thumbnail for key, using the cache when possible.
        func (r Renderer) Render(ctx context.Context, key string) ([]byte, error) {
        	if b, ok := r.Cache.Get("thumb:" + key); ok {
        		return b, nil
        	}
        	if err := ctx.Err(); err != nil {
        		return nil, err
        	}
        	img, err := r.Store.Get(key)
        	if err != nil {
        		return nil, fmt.Errorf("thumbs: %s: %w", key, err)
        	}
        	thumb := []byte(fmt.Sprintf("thumb(%d bytes)", len(img)))
        	r.Cache.Add("thumb:"+key, thumb)
        	return thumb, nil
        }
        ''',
    "service/thumbs/thumbs_test.go": '''
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
        	if _, err := s.Get("cat.png"); err == nil {
        		t.Fatal("expected miss")
        	}
        }
        ''',
    "service/replicate/replicate.go": '''
        // Package replicate copies blobs between stores.
        package replicate

        import (
        	"context"
        	"fmt"

        	"blobhub/storage"
        )

        // Copy copies keys from src to dst, stopping at the first error.
        func Copy(ctx context.Context, src, dst storage.Store, keys []string) (int, error) {
        	n := 0
        	for _, k := range keys {
        		b, err := src.Get(k)
        		if err != nil {
        			return n, fmt.Errorf("replicate: read %s: %w", k, err)
        		}
        		if err := dst.Put(k, b); err != nil {
        			return n, fmt.Errorf("replicate: write %s: %w", k, err)
        		}
        		n++
        	}
        	return n, nil
        }
        ''',
    "service/audit/audit.go": '''
        // Package audit checks that expected blobs are present.
        package audit

        import (
        	"errors"
        	"sort"

        	"blobhub/storage"
        )

        // Missing returns the keys that s does not have, sorted.
        func Missing(s storage.Store, keys []string) ([]string, error) {
        	var out []string
        	for _, k := range keys {
        		if _, err := s.Get(k); errors.Is(err, storage.ErrNotFound) {
        			out = append(out, k)
        		} else if err != nil {
        			return nil, err
        		}
        	}
        	sort.Strings(out)
        	return out, nil
        }
        ''',
    "service/httpapi/handler.go": '''
        // Package httpapi serves blobs over HTTP.
        package httpapi

        import (
        	"errors"
        	"net/http"
        	"strings"

        	"blobhub/storage"
        )

        // Handler serves GET /blobs/<key>.
        type Handler struct {
        	Store storage.Store
        }

        func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
        	key := strings.TrimPrefix(r.URL.Path, "/blobs/")
        	if r.Method != http.MethodGet || key == "" || key == r.URL.Path {
        		http.Error(w, "not found", http.StatusNotFound)
        		return
        	}
        	b, err := h.Store.Get(key)
        	switch {
        	case errors.Is(err, storage.ErrNotFound):
        		http.Error(w, "not found", http.StatusNotFound)
        	case err != nil:
        		http.Error(w, "storage error", http.StatusBadGateway)
        	default:
        		w.Header().Set("Content-Type", "application/octet-stream")
        		_, _ = w.Write(b)
        	}
        }
        ''',
    "cmd/blobctl/main.go": '''
        // Command blobctl prints a blob from a disk store.
        package main

        import (
        	"fmt"
        	"os"

        	"blobhub/storage/disk"
        )

        func main() {
        	if len(os.Args) != 3 {
        		fmt.Fprintln(os.Stderr, "usage: blobctl <dir> <key>")
        		os.Exit(2)
        	}
        	b, err := disk.Store{Dir: os.Args[1]}.Get(os.Args[2])
        	if err != nil {
        		fmt.Fprintln(os.Stderr, err)
        		os.Exit(1)
        	}
        	os.Stdout.Write(b)
        }
        ''',
    "README.md": '''
        # blobhub

        Blob storage behind one interface, `storage.Store` (storage/store.go), with
        implementations in storage/* and consumers in service/* and cmd/.
        `cache.Cache` is a separate, infallible thumbnail cache.

        `go test ./...`
        ''',
}

# Reference edits: (old, new) replacements per file.
REF = {
    "storage/store.go": [
        ('import "errors"', 'import (\n\t"context"\n\t"errors"\n)'),
        ("\t// Get returns the blob stored under key or ErrNotFound.\n\tGet(key string) ([]byte, error)",
         "\t// Get returns the blob stored under key or ErrNotFound. If ctx is\n\t// already done it returns ctx.Err() without doing any work.\n\tGet(ctx context.Context, key string) ([]byte, error)"),
    ],
    "storage/memory/memory.go": [
        ('import (\n\t"sync"', 'import (\n\t"context"\n\t"sync"'),
        ("func (s *Store) Get(key string) ([]byte, error) {\n", "func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n"),
    ],
    "storage/memory/memory_test.go": [
        ('import (\n\t"errors"', 'import (\n\t"context"\n\t"errors"'),
        ('s.Get("a")', 's.Get(context.Background(), "a")'),
    ],
    "storage/disk/disk.go": [
        ('import (\n\t"encoding/hex"', 'import (\n\t"context"\n\t"encoding/hex"'),
        ("func (s Store) Get(key string) ([]byte, error) {\n", "func (s Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n"),
    ],
    "storage/prefixed/prefixed.go": [
        ('import "blobhub/storage"', 'import (\n\t"context"\n\n\t"blobhub/storage"\n)'),
        ("func (s Store) Get(key string) ([]byte, error) { return s.Inner.Get(s.Prefix + key) }",
         "func (s Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\treturn s.Inner.Get(ctx, s.Prefix+key)\n}"),
    ],
    "storage/tiered/tiered.go": [
        ('import (\n\t"errors"', 'import (\n\t"context"\n\t"errors"'),
        ("func (s Store) Get(key string) ([]byte, error) {\n\tb, err := s.Fast.Get(key)",
         "func (s Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\tb, err := s.Fast.Get(ctx, key)"),
        ("b, err = s.Slow.Get(key)", "b, err = s.Slow.Get(ctx, key)"),
    ],
    "storage/tiered/tiered_test.go": [
        ('import (\n\t"testing"', 'import (\n\t"context"\n\t"testing"'),
        ('s.Get("k")', 's.Get(context.Background(), "k")'),
        ('fast.Get("k")', 'fast.Get(context.Background(), "k")'),
    ],
    "storage/metered/metered.go": [
        ('import (\n\t"errors"', 'import (\n\t"context"\n\t"errors"'),
        ("func (s *Store) Get(key string) ([]byte, error) {\n\ts.Reads.Add(1)\n\tb, err := s.Inner.Get(key)",
         "func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\ts.Reads.Add(1)\n\tb, err := s.Inner.Get(ctx, key)"),
    ],
    "storage/readonly/readonly.go": [
        ('import "blobhub/storage"', 'import (\n\t"context"\n\n\t"blobhub/storage"\n)'),
        ("func (s Store) Get(key string) ([]byte, error) { return s.Inner.Get(key) }",
         "func (s Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\treturn s.Inner.Get(ctx, key)\n}"),
    ],
    "storage/faulty/faulty.go": [
        ('import (\n\t"errors"', 'import (\n\t"context"\n\t"errors"'),
        ("func (s *Store) Get(key string) ([]byte, error) {\n\ts.mu.Lock()",
         "func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {\n\tif err := ctx.Err(); err != nil {\n\t\treturn nil, err\n\t}\n\ts.mu.Lock()"),
        ("return s.Inner.Get(key)", "return s.Inner.Get(ctx, key)"),
    ],
    "service/thumbs/thumbs.go": [
        ("img, err := r.Store.Get(key)", "img, err := r.Store.Get(ctx, key)"),
    ],
    "service/thumbs/thumbs_test.go": [
        ('s.Get("cat.png")', 's.Get(context.Background(), "cat.png")'),
    ],
    "service/replicate/replicate.go": [
        ("b, err := src.Get(k)", "b, err := src.Get(ctx, k)"),
    ],
    "service/audit/audit.go": [
        ('import (\n\t"errors"', 'import (\n\t"context"\n\t"errors"'),
        ("s.Get(k)", "s.Get(context.Background(), k)"),
    ],
    "service/httpapi/handler.go": [
        ("h.Store.Get(key)", "h.Store.Get(r.Context(), key)"),
    ],
    "cmd/blobctl/main.go": [
        ('import (\n\t"fmt"', 'import (\n\t"context"\n\t"fmt"'),
        ("disk.Store{Dir: os.Args[1]}.Get(os.Args[2])", "disk.Store{Dir: os.Args[1]}.Get(context.Background(), os.Args[2])"),
    ],
}

SPY = '''
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
'''

HIDDEN = {
    "storage/zz_hidden_test.go": '''
        package storage_test

        import (
        	"context"

        	"blobhub/cache"
        	"blobhub/storage"
        	"blobhub/storage/disk"
        	"blobhub/storage/faulty"
        	"blobhub/storage/memory"
        	"blobhub/storage/metered"
        	"blobhub/storage/prefixed"
        	"blobhub/storage/readonly"
        	"blobhub/storage/tiered"
        )

        type wantStore interface {
        	Get(ctx context.Context, key string) ([]byte, error)
        	Put(key string, data []byte) error
        	Delete(key string) error
        }

        var (
        	_ wantStore   = storage.Store(nil)
        	_ storage.Store = wantStore(nil)
        	_ storage.Store = (*memory.Store)(nil)
        	_ storage.Store = disk.Store{}
        	_ storage.Store = prefixed.Store{}
        	_ storage.Store = tiered.Store{}
        	_ storage.Store = (*metered.Store)(nil)
        	_ storage.Store = readonly.Store{}
        	_ storage.Store = (*faulty.Store)(nil)
        	_ cache.Cache   = (*cache.LRU)(nil)
        )

        type oldCache interface {
        	Get(key string) ([]byte, bool)
        	Add(key string, data []byte)
        }

        var _ oldCache = cache.Cache(nil)
        ''',
    "storage/wrappers_zz_hidden_test.go": '''
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
        ''' + textwrap.indent(SPY, " " * 8) + '''
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
        ''',
    "service/zz_hidden_test.go": '''
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
        ''' + textwrap.indent(SPY, " " * 8) + '''
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
        ''',
}


def build():
    t = Task("go-ctx-store")
    t.fx("go.mod", GO_MOD.format(mod=MOD))
    for rel, body in FX.items():
        t.fx(rel, d(body))
    for pkg in ["internal/quota", "internal/naming", "internal/retention", "internal/webhooks"]:
        go_filler(t, MOD, pkg, count=3)
    for rel, reps in REF.items():
        t.ref_from_fx(rel, *reps)
    for rel, body in HIDDEN.items():
        t.gr(rel, d(body))
    t.register(
        name="Add context.Context to one interface method and update every implementation and call site",
        category="multi-file", tags=["go", "refactor", "interface", "context", "multi-file"], difficulty="hard",
        prompt=("Change the `Get` method of the `storage.Store` interface (storage/store.go) to "
                "`Get(ctx context.Context, key string) ([]byte, error)`. `Put` and `Delete` keep their signatures. "
                "Update every implementation of `storage.Store` in this repository so that each one: returns "
                "`ctx.Err()` immediately, without doing any other work (no counting, no calls to other stores), when "
                "ctx is already cancelled or expired; and passes the ctx it received unchanged to every store it "
                "wraps. Update every caller of `Store.Get`: where the calling function already has a "
                "`context.Context` parameter pass that one, in HTTP handlers pass the request's context, and "
                "everywhere else use `context.Background()`. Do not change the signature of any other function or "
                "method, and do not change `cache.Cache`, which is a different interface. Update the existing tests "
                "as needed; `go vet ./...` and `go test ./...` must pass."),
        strictDiff=True,
        allowedChangedFiles=["storage/**", "service/**", "cmd/**"],
        forbiddenChangedFiles=["cache/**", "internal/**", "go.mod"],
        expectedChangedFiles=sorted(REF),
        verificationCommands=[cmd("go-vet", "go", "vet", "./..."), cmd("go-test", "go", "test", "-count=1", "./...")],
        fileChecks=[{"id": "store-signature", "path": "storage/store.go",
                     "regex": [r"Get\(ctx context\.Context, key string\) \(\[\]byte, error\)",
                               r"Put\(key string, data \[\]byte\) error", r"Delete\(key string\) error"], "critical": True}],
    )
