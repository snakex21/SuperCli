package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderRuntimeContextIsEndpointCredentialScopedAndReplaced(t *testing.T) {
	const model = "same-opaque-id"
	first, second := "http://first.invalid/v1", "http://second.invalid/v1"
	for _, entry := range []struct {
		base, key string
		tokens    int
	}{
		{first, "first-key", 8192}, {second, "first-key", 32768}, {first, "second-key", 65536},
	} {
		RememberProviderRuntimeContexts(entry.base, entry.key, []ModelInfo{{ID: model, RuntimeContextLength: entry.tokens}})
	}
	if got := ProviderRuntimeContext(first+"/", "first-key", model); got != 8192 {
		t.Fatalf("first endpoint=%d", got)
	}
	if got := ProviderRuntimeContext(second, "first-key", model); got != 32768 {
		t.Fatalf("second endpoint=%d", got)
	}
	if got := ProviderRuntimeContext(first, "second-key", model); got != 65536 {
		t.Fatalf("other credential=%d", got)
	}
	if got := ProviderRuntimeContext(first, "first-key", "route/"+model); got != 0 {
		t.Fatalf("invented suffix alias=%d", got)
	}
	older := newRuntimeContextVersion()
	RememberProviderRuntimeContexts(first, "first-key", nil)
	rememberProviderRuntimeContexts(first, "first-key", []ModelInfo{{ID: model, RuntimeContextLength: 999999}}, older)
	if got := ProviderRuntimeContext(first, "first-key", model); got != 0 {
		t.Fatalf("old discovery restored unloaded instance: %d", got)
	}
	if got := ProviderRuntimeContext(second, "first-key", model); got != 32768 {
		t.Fatalf("another endpoint cleared: %d", got)
	}
}

func TestRefreshLocalModelContextsUsesLoadedMetadataAndClearsUnloaded(t *testing.T) {
	var calls, loaded atomic.Int32
	loaded.Store(32768)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/models" {
			t.Errorf("unexpected operation: %s %s", r.Method, r.URL.Path)
		}
		if loaded.Load() > 0 {
			fmt.Fprintf(w, `{"models":[{"key":"metadata-model","max_context_length":262144,"loaded_instances":[{"id":"metadata-model","config":{"context_length":%d}}]}]}`, loaded.Load())
		} else {
			fmt.Fprint(w, `{"models":[{"key":"metadata-model","max_context_length":262144,"loaded_instances":[]}]}`)
		}
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "metadata-model"); got != 32768 {
		t.Fatalf("runtime=%d", got)
	}
	loaded.Store(0)
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "metadata-model"); got != 0 {
		t.Fatalf("unloaded runtime=%d", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("metadata requests=%d, want one per user event", calls.Load())
	}
}

type joinedContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *joinedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func TestRefreshLocalModelContextsSharesWorkAndCancelingOneWaiterKeepsOther(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			fmt.Fprint(w, `{"models":[{"key":"shared","loaded_context_length":8192}]}`)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- RefreshLocalModelContexts(ctx, base, "") }()
	<-entered
	joined := &joinedContext{Context: context.Background(), joined: make(chan struct{})}
	go func() { second <- RefreshLocalModelContexts(joined, base, "") }()
	<-joined.joined
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter=%v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || ProviderRuntimeContext(base, "", "shared") != 8192 {
		t.Fatalf("calls=%d runtime=%d", calls.Load(), ProviderRuntimeContext(base, "", "shared"))
	}
}

func TestUnsupportedLocalMetadataIsNotRetriedUntilExplicitInvalidation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.NotFound(w, r) }))
	defer srv.Close()
	base := srv.URL + "/v1"
	for i := 0; i < 3; i++ {
		if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("unsupported server repeatedly probed: %d", calls.Load())
	}
	InvalidateProviderModelCache(base)
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("explicit scan did not retry: %d", calls.Load())
	}
}

func TestInvalidatedNativeDiscoveryCannotRestoreRuntime(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"models":[{"key":"invalidated","loaded_context_length":32768}]}`)
			return
		}
		fmt.Fprint(w, `{"models":[{"key":"invalidated","loaded_context_length":8192}]}`)
	}))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); srv.Close() })
	base := srv.URL + "/v1"
	done := make(chan error, 1)
	go func() { _, _, err := fetchLocalNativeModelInfos(context.Background(), base, ""); done <- err }()
	<-entered
	InvalidateProviderModelCache(base)
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "invalidated"); got != 0 {
		t.Fatalf("invalidated response restored its limit: %d", got)
	}
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "invalidated"); got != 8192 {
		t.Fatalf("new discovery limit=%d", got)
	}
}

func TestInvalidationDetachesAndCancelsPreviousRuntimeFlight(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		fmt.Fprint(w, `{"models":[{"key":"new-flight","loaded_context_length":6144}]}`)
	}))
	defer srv.Close()
	base := srv.URL + "/v1"
	first := make(chan error, 1)
	go func() { first <- RefreshLocalModelContexts(context.Background(), base, "") }()
	<-entered
	InvalidateProviderModelCache(base)
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	<-canceled
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("invalidated flight did not cancel: %v", err)
	}
	if calls.Load() != 2 || ProviderRuntimeContext(base, "", "new-flight") != 6144 {
		t.Fatalf("new event rejoined old flight: calls=%d limit=%d", calls.Load(), ProviderRuntimeContext(base, "", "new-flight"))
	}
}

func TestOlderUnsupportedDiscoveryCannotDisableNewerSupportedSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var v1Calls, v0Calls atomic.Int32
	var releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models" {
			v0Calls.Add(1)
			http.NotFound(w, r)
			return
		}
		if v1Calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"models":[{"key":"supported","loaded_context_length":%d}]}`, v1Calls.Load()*4096)
	}))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); srv.Close() })
	base := srv.URL + "/v1"
	first := make(chan error, 1)
	go func() { first <- RefreshLocalModelContexts(context.Background(), base, "") }()
	<-entered
	if _, _, err := fetchLocalNativeModelInfos(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := ProviderRuntimeContext(base, "", "supported"); got != 8192 {
		t.Fatalf("newer supported discovery lost: %d", got)
	}
	if err := RefreshLocalModelContexts(context.Background(), base, ""); err != nil {
		t.Fatal(err)
	}
	if v1Calls.Load() != 3 || v0Calls.Load() != 1 || ProviderRuntimeContext(base, "", "supported") != 12288 {
		t.Fatalf("old unsupported result disabled refresh: v1=%d v0=%d limit=%d", v1Calls.Load(), v0Calls.Load(), ProviderRuntimeContext(base, "", "supported"))
	}
}

func TestEvictedRuntimeSnapshotCannotBeRestoredByOlderRead(t *testing.T) {
	const base = "http://eviction-order.invalid/v1"
	key := runtimeContextKey(base, "")
	runtimeContextState.Lock()
	older := newRuntimeContextReadLocked(key)
	runtimeContextState.Unlock()
	defer forgetRuntimeContextRead(older)
	RememberProviderRuntimeContexts(base, "", []ModelInfo{{ID: "evicted", RuntimeContextLength: 8192}})
	// Make the victim unambiguously oldest, including on coarse Windows clocks.
	runtimeContextState.Lock()
	snapshot := runtimeContextState.snapshots[key]
	snapshot.updated = time.Unix(0, 0)
	runtimeContextState.snapshots[key] = snapshot
	runtimeContextState.Unlock()
	for i := 0; i < 64; i++ {
		other := fmt.Sprintf("http://eviction-%d.invalid/v1", i)
		RememberProviderRuntimeContexts(other, "", nil)
	}
	if got := ProviderRuntimeContext(base, "", "evicted"); got != 0 {
		t.Fatalf("fixture did not evict newer snapshot: %d", got)
	}
	rememberRuntimeContextRead(older, []ModelInfo{{ID: "evicted", RuntimeContextLength: 999999}})
	if got := ProviderRuntimeContext(base, "", "evicted"); got != 0 {
		t.Fatalf("older read restored a stale limit after eviction: %d", got)
	}
}
