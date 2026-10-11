package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const localDiscoveryFixture = `{"models":[{"key":"opaque-model","max_context_length":262144,"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["off","on"]}},"loaded_instances":[{"id":"opaque-instance","config":{"context_length":8192}}]}]}`

type localDiscoveryResult struct {
	models    []ModelInfo
	supported bool
	err       error
}

type localDiscoveryJoinContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *localDiscoveryJoinContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

type localDiscoveryFetcher func(context.Context, string, string) ([]ModelInfo, bool, error)

func startLocalDiscovery(fetch localDiscoveryFetcher, ctx context.Context, base, key string) <-chan localDiscoveryResult {
	done := make(chan localDiscoveryResult, 1)
	go func() {
		models, supported, err := fetch(ctx, base, key)
		done <- localDiscoveryResult{models: models, supported: supported, err: err}
	}()
	return done
}

func joinedLocalDiscovery(fetch localDiscoveryFetcher, ctx context.Context, base, key string) <-chan localDiscoveryResult {
	joined := &localDiscoveryJoinContext{Context: ctx, joined: make(chan struct{})}
	done := startLocalDiscovery(fetch, joined, base, key)
	<-joined.joined
	return done
}

func gatedLocalDiscoveryServer(t *testing.T, status int, body string) (*httptest.Server, <-chan string, func(), *atomic.Int32) {
	t.Helper()
	entered := make(chan string, 256)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	calls := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- r.URL.Path + "\x00" + r.Header.Get("Authorization")
		select {
		case <-release:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { unblock(); srv.Close() })
	return srv, entered, unblock, calls
}

func TestLocalNativeDiscoverySeparatesFullEndpointAndCredentials(t *testing.T) {
	srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	var g localModelDiscoveryFlights
	first := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "first-key")
	<-entered
	otherEndpoint := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/other/v1", "first-key")
	<-entered
	otherCredential := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "second-key")
	<-entered
	duplicate := joinedLocalDiscovery(g.fetch, context.Background(), " "+srv.URL+"/v1/ ", " first-key ")
	release()
	for _, done := range []<-chan localDiscoveryResult{first, otherEndpoint, otherCredential, duplicate} {
		if got := <-done; got.err != nil || !got.supported || len(got.models) != 2 {
			t.Fatalf("discovery=%+v", got)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("GETs=%d, distinct full endpoint and credential identities must stay separate", calls.Load())
	}
	identity := localModelDiscoveryIdentity(srv.URL+"/v1", "first-key")
	if len(identity.credential) != 32 {
		t.Fatal("credential fingerprint is truncated")
	}
}

func TestLocalNativeDiscoveryCancellationKeepsHealthyWaiter(t *testing.T) {
	for _, cancelStarter := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel-starter-%v", cancelStarter), func(t *testing.T) {
			srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
			var g localModelDiscoveryFlights
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			firstCtx, followerCtx := context.Background(), context.Background()
			if cancelStarter {
				firstCtx = ctx
			} else {
				followerCtx = ctx
			}
			first := startLocalDiscovery(g.fetch, firstCtx, srv.URL+"/v1", "")
			<-entered
			follower := joinedLocalDiscovery(g.fetch, followerCtx, srv.URL+"/v1", "")
			cancel()
			canceled, healthy := follower, first
			if cancelStarter {
				canceled, healthy = first, follower
			}
			if got := <-canceled; !errors.Is(got.err, context.Canceled) || got.models != nil {
				t.Fatalf("canceled waiter=%+v", got)
			}
			release()
			if got := <-healthy; got.err != nil || !got.supported || len(got.models) != 2 {
				t.Fatalf("healthy waiter=%+v", got)
			}
			if calls.Load() != 1 {
				t.Fatalf("healthy waiter retried canceled starter's transport: %d GETs", calls.Load())
			}
		})
	}
}

func TestLocalNativeDiscoveryLastWaiterCancelsAndNextStartsFresh(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		_, _ = w.Write([]byte(localDiscoveryFixture))
	}))
	defer srv.Close()
	var g localModelDiscoveryFlights
	base := srv.URL + "/v1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := startLocalDiscovery(g.fetch, ctx, base, "")
	<-entered
	runtimeContextState.Lock()
	flight := g.pending[localModelDiscoveryIdentity(base, "")]
	runtimeContextState.Unlock()
	cancel()
	if got := <-first; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancellation=%+v", got)
	}
	<-canceled
	<-flight.done
	if got := ProviderRuntimeContext(base, "", "opaque-instance"); got != 0 {
		t.Fatalf("canceled discovery published runtime %d", got)
	}
	next, supported, err := g.fetch(context.Background(), base, "")
	if err != nil || !supported || len(next) != 2 || calls.Load() != 2 {
		t.Fatalf("fresh retry=%+v supported=%v err=%v GETs=%d", next, supported, err, calls.Load())
	}
	runtimeContextState.Lock()
	pending := len(g.pending)
	_, readRetained := runtimeContextState.reads[flight.read]
	runtimeContextState.Unlock()
	if pending != 0 || readRetained {
		t.Fatal("canceled flight retained bookkeeping")
	}
}

func TestLocalNativeDiscoveryCanceledBeforeAdmissionDoesNoWork(t *testing.T) {
	srv, _, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	release()
	var g localModelDiscoveryFlights
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if models, supported, err := g.fetch(ctx, srv.URL+"/v1", ""); models != nil || supported || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled result=%+v %v %v", models, supported, err)
	}
	if calls.Load() != 0 || len(g.pending) != 0 {
		t.Fatal("already-canceled caller started discovery")
	}
	if models, supported, err := g.fetch(ctx, "https://public.invalid/v1", ""); models != nil || supported || err != nil {
		t.Fatal("unsupported endpoint's no-op behavior changed")
	}
}

func TestLocalNativeDiscoveryPreservesEmptyUnsupportedAndErrorResults(t *testing.T) {
	for _, fixture := range []struct {
		name                           string
		status                         int
		body                           string
		supported, nilModels, hasError bool
		reads                          int32
		errorText                      string
		syntaxError                    bool
	}{
		{"empty-success", http.StatusOK, `{"models":[]}`, true, false, false, 1, "", false},
		{"unsupported", http.StatusNotFound, "not found", false, true, false, 2, "", false},
		{"unknown-envelope", http.StatusOK, `{"unrelated":[]}`, false, true, false, 2, "", false},
		{"ordinary-error", http.StatusServiceUnavailable, "unavailable", false, true, true, 2, "local model metadata: HTTP 503", false},
		{"malformed-json", http.StatusOK, `{"models":[`, false, true, true, 2, "unexpected end of JSON input", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			srv, entered, release, calls := gatedLocalDiscoveryServer(t, fixture.status, fixture.body)
			var g localModelDiscoveryFlights
			first := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
			<-entered
			second := joinedLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
			release()
			for _, done := range []<-chan localDiscoveryResult{first, second} {
				got := <-done
				if got.supported != fixture.supported || (got.models == nil) != fixture.nilModels || (got.err != nil) != fixture.hasError || len(got.models) != 0 {
					t.Fatalf("result=%+v fixture=%+v", got, fixture)
				}
				if got.err != nil && got.err.Error() != fixture.errorText {
					t.Fatalf("error text changed: %v", got.err)
				}
				if fixture.syntaxError {
					var syntax *json.SyntaxError
					if !errors.As(got.err, &syntax) {
						t.Fatalf("JSON error classification changed: %T", got.err)
					}
				}
			}
			if calls.Load() != fixture.reads {
				t.Fatalf("fallback chain reads=%d, want %d", calls.Load(), fixture.reads)
			}
			_, _, _ = g.fetch(context.Background(), srv.URL+"/v1", "")
			if calls.Load() != 2*fixture.reads {
				t.Fatal("completed/negative result suppressed a new discovery")
			}
		})
	}
}

func TestLocalNativeDiscoveryStaleFlightDoesNotAdmitNewCaller(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit-invalidation-%v", explicit), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capacity := 8192
				if calls.Add(1) == 1 {
					close(entered)
					<-release
					capacity = 32768
				}
				fmt.Fprintf(w, `{"models":[{"key":"stale-check","loaded_context_length":%d}]}`, capacity)
			}))
			t.Cleanup(func() { once.Do(func() { close(release) }); srv.Close() })
			base := srv.URL + "/v1"
			first := startLocalDiscovery(fetchLocalNativeModelInfos, context.Background(), base, "")
			<-entered
			if explicit {
				InvalidateProviderModelCache(base)
			} else {
				RememberProviderRuntimeContexts(base, "", []ModelInfo{{ID: "stale-check", RuntimeContextLength: 4096}})
			}
			fresh, supported, err := fetchLocalNativeModelInfos(context.Background(), base, "")
			if err != nil || !supported || len(fresh) != 1 || fresh[0].RuntimeContextLength != 8192 {
				t.Fatalf("fresh discovery=%+v %v %v", fresh, supported, err)
			}
			once.Do(func() { close(release) })
			old := <-first
			if old.err != nil || !old.supported || len(old.models) != 1 || old.models[0].RuntimeContextLength != 32768 {
				t.Fatalf("existing waiter lost its old response: %+v", old)
			}
			if calls.Load() != 2 || ProviderRuntimeContext(base, "", "stale-check") != 8192 {
				t.Fatal("obsolete read was rejoined or restored stale runtime")
			}
		})
	}
}

func TestLocalNativeDiscoveryPendingCapacityFallsBackIndependently(t *testing.T) {
	var g localModelDiscoveryFlights
	g.pending = make(map[localModelDiscoveryKey]*localModelDiscoveryFlight)
	for i := 0; i < maxLocalModelDiscoveryFlights; i++ {
		key := localModelDiscoveryIdentity(fmt.Sprintf("http://pending-%d.invalid/v1", i), "")
		g.pending[key] = &localModelDiscoveryFlight{read: &runtimeContextRead{}, done: make(chan struct{})}
	}
	srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	base := srv.URL + "/v1"
	first := startLocalDiscovery(g.fetch, context.Background(), base, "")
	<-entered
	second := startLocalDiscovery(g.fetch, context.Background(), base, "")
	<-entered
	runtimeContextState.Lock()
	_, admitted := g.pending[localModelDiscoveryIdentity(base, "")]
	count := len(g.pending)
	runtimeContextState.Unlock()
	if admitted || count != maxLocalModelDiscoveryFlights {
		t.Fatal("pending-map capacity exceeded")
	}
	release()
	for _, done := range []<-chan localDiscoveryResult{first, second} {
		if got := <-done; got.err != nil || !got.supported {
			t.Fatalf("overflow=%+v", got)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("overflow altered independent reads")
	}
}

// Independent frozen behavior: one owned read per invocation. The underlying
// HTTP/fallback/parser code is unchanged by this optimization.
func legacyLocalNativeDiscoveryForTest(ctx context.Context, base, key string) ([]ModelInfo, bool, error) {
	if localNativeModelsRoot(base) == "" {
		return nil, false, nil
	}
	runtimeContextState.Lock()
	read := newRuntimeContextReadLocked(runtimeContextKey(base, key))
	runtimeContextState.Unlock()
	defer forgetRuntimeContextRead(read)
	return fetchLocalNativeModelInfosForRead(ctx, base, key, read)
}

func TestLocalNativeDiscoveryRawAndRuntimeReadsStayIndependent(t *testing.T) {
	srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	base := srv.URL + "/v1"
	first := startLocalDiscovery(fetchLocalNativeModelInfos, context.Background(), base, "")
	<-entered
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- RefreshLocalModelContexts(context.Background(), base, "") }()
	<-entered
	release()
	if got := <-first; got.err != nil || !got.supported {
		t.Fatalf("raw=%+v", got)
	}
	if err := <-runtimeDone; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("fresh runtime refresh was merged into raw discovery")
	}
}

func TestLocalNativeDiscoverySharesCompleteV1ToV0Fallback(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- r.URL.Path
		if r.URL.Path == "/api/v1/models" {
			<-release
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(localDiscoveryFixture))
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }); srv.Close() })
	var g localModelDiscoveryFlights
	first := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
	if path := <-entered; path != "/api/v1/models" {
		t.Fatalf("first endpoint=%q", path)
	}
	second := joinedLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
	once.Do(func() { close(release) })
	for _, done := range []<-chan localDiscoveryResult{first, second} {
		if got := <-done; got.err != nil || !got.supported || len(got.models) != 2 || got.models[1].RuntimeContextLength != 8192 {
			t.Fatalf("fallback result=%+v", got)
		}
	}
	if path := <-entered; path != "/api/v0/models" {
		t.Fatalf("fallback endpoint=%q", path)
	}
	if calls.Load() != 2 {
		t.Fatalf("fallback GETs=%d, want one shared v1/v0 chain", calls.Load())
	}
}

func TestLocalNativeDiscoveryWorkDoesNotInheritStarterDeadline(t *testing.T) {
	srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	var g localModelDiscoveryFlights
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Count Deadline queries from the real net/http client. WithoutCancel must
	// hide the starter's deadline; a legacy control proves that this probe sees
	// the original client path. No deadline is expired by waiting or sleeping.
	probe := &localDiscoveryDeadlineProbeContext{Context: ctx}
	first := startLocalDiscovery(g.fetch, probe, srv.URL+"/v1", "")
	<-entered
	second := joinedLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
	cancel()
	if got := <-first; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("starter=%+v", got)
	}
	release()
	if got := <-second; got.err != nil || !got.supported || len(got.models) != 2 {
		t.Fatalf("healthy follower=%+v", got)
	}
	if calls.Load() != 1 {
		t.Fatal("canceling starter restarted work")
	}
	if probe.queries.Load() != 0 {
		t.Fatal("shared HTTP work consulted starter's deadline")
	}
	control := &localDiscoveryDeadlineProbeContext{Context: context.Background()}
	if _, supported, err := legacyLocalNativeDiscoveryForTest(control, srv.URL+"/v1", ""); err != nil || !supported {
		t.Fatalf("legacy probe failed: %v", err)
	}
	if control.queries.Load() == 0 {
		t.Fatal("deadline probe did not observe the original HTTP path")
	}
}

type localDiscoveryDeadlineProbeContext struct {
	context.Context
	queries atomic.Int32
}

func (c *localDiscoveryDeadlineProbeContext) Deadline() (time.Time, bool) {
	c.queries.Add(1)
	return time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC), true
}

func TestLocalNativeDiscoveryWaiterDeadlineDoesNotCancelHealthyTransport(t *testing.T) {
	srv, entered, release, calls := gatedLocalDiscoveryServer(t, http.StatusOK, localDiscoveryFixture)
	var g localModelDiscoveryFlights
	first := startLocalDiscovery(g.fetch, context.Background(), srv.URL+"/v1", "")
	<-entered
	deadline := &localDiscoveryDeadlineWaiterContext{Context: context.Background(), done: make(chan struct{})}
	second := joinedLocalDiscovery(g.fetch, deadline, srv.URL+"/v1", "")
	// Trigger this waiter's deadline explicitly. This is a context event, not
	// a sleep-based race against net/http or the real discovery timeout.
	deadline.expired.Store(true)
	close(deadline.done)
	if got := <-second; !errors.Is(got.err, context.DeadlineExceeded) || got.models != nil {
		t.Fatalf("expired waiter=%+v", got)
	}
	release()
	if got := <-first; got.err != nil || !got.supported || len(got.models) != 2 {
		t.Fatalf("healthy waiter=%+v", got)
	}
	if calls.Load() != 1 {
		t.Fatal("one waiter's deadline canceled healthy transport")
	}
}

type localDiscoveryDeadlineWaiterContext struct {
	context.Context
	done    chan struct{}
	expired atomic.Bool
}

func (c *localDiscoveryDeadlineWaiterContext) Done() <-chan struct{} { return c.done }

func (c *localDiscoveryDeadlineWaiterContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *localDiscoveryDeadlineWaiterContext) Deadline() (time.Time, bool) {
	return time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC), true
}
