package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Runtime context belongs to an endpoint and credential, never to the global
// model catalog. It is a fresh loaded-instance property and is not persisted.
type providerRuntimeContexts struct {
	windows map[string]int
	updated time.Time
	version uint64
}

var runtimeContextState = struct {
	sync.Mutex
	snapshots   map[string]providerRuntimeContexts
	flights     map[string]*runtimeContextFlight
	unsupported map[string]bool
	reads       map[*runtimeContextRead]struct{}
	nextVersion uint64
}{snapshots: make(map[string]providerRuntimeContexts), flights: make(map[string]*runtimeContextFlight), unsupported: make(map[string]bool), reads: make(map[*runtimeContextRead]struct{})}

// Reads live only until their request returns. Invalidation marks these handles
// rather than retaining an unbounded map of endpoint tombstones.
type runtimeContextRead struct {
	key         string
	version     uint64
	invalidated bool
}

type runtimeContextFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	err     error
	closed  bool
	read    *runtimeContextRead
}

func runtimeContextKey(baseURL, apiKey string) string {
	return providerModelsCacheKey("runtime", baseURL, apiKey)
}

// RememberProviderRuntimeContexts replaces one successful discovery snapshot.
// Missing/not-loaded models clear old instance limits from that endpoint only.
func RememberProviderRuntimeContexts(baseURL, apiKey string, models []ModelInfo) {
	rememberProviderRuntimeContexts(baseURL, apiKey, models, newRuntimeContextVersion())
}

func newRuntimeContextVersion() uint64 {
	runtimeContextState.Lock()
	defer runtimeContextState.Unlock()
	runtimeContextState.nextVersion++
	return runtimeContextState.nextVersion
}

func rememberProviderRuntimeContexts(baseURL, apiKey string, models []ModelInfo, version uint64) {
	windows := providerRuntimeWindows(models)
	key := runtimeContextKey(baseURL, apiKey)
	runtimeContextState.Lock()
	defer runtimeContextState.Unlock()
	rememberProviderRuntimeContextsLocked(key, windows, version)
}

func providerRuntimeWindows(models []ModelInfo) map[string]int {
	windows := make(map[string]int)
	for _, model := range models {
		if model.ID != "" && model.RuntimeContextLength > 0 {
			windows[model.ID] = model.RuntimeContextLength
		}
	}
	return windows
}

func rememberProviderRuntimeContextsLocked(key string, windows map[string]int, version uint64) {
	if previous, found := runtimeContextState.snapshots[key]; found && previous.version > version {
		return // a discovery started before a newer snapshot cannot restore it
	}
	// An accepted result makes older reads obsolete even if the bounded cache
	// later evicts this snapshot before those reads return.
	for read := range runtimeContextState.reads {
		if read.key == key && read.version < version {
			read.invalidated = true
		}
	}
	// Bound metadata churn without a timer, disk cache or background maintenance.
	if _, found := runtimeContextState.snapshots[key]; !found && len(runtimeContextState.snapshots) >= 64 {
		var oldest string
		var at time.Time
		for candidate, snapshot := range runtimeContextState.snapshots {
			if oldest == "" || snapshot.updated.Before(at) {
				oldest, at = candidate, snapshot.updated
			}
		}
		delete(runtimeContextState.snapshots, oldest)
		delete(runtimeContextState.unsupported, oldest)
	}
	runtimeContextState.snapshots[key] = providerRuntimeContexts{windows: windows, updated: time.Now(), version: version}
	delete(runtimeContextState.unsupported, key)
}

func newRuntimeContextReadLocked(key string) *runtimeContextRead {
	runtimeContextState.nextVersion++
	read := &runtimeContextRead{key: key, version: runtimeContextState.nextVersion}
	runtimeContextState.reads[read] = struct{}{}
	return read
}

func rememberRuntimeContextRead(read *runtimeContextRead, models []ModelInfo) {
	windows := providerRuntimeWindows(models)
	runtimeContextState.Lock()
	defer runtimeContextState.Unlock()
	if !read.invalidated {
		rememberProviderRuntimeContextsLocked(read.key, windows, read.version)
	}
}

func forgetRuntimeContextRead(read *runtimeContextRead) {
	runtimeContextState.Lock()
	delete(runtimeContextState.reads, read)
	runtimeContextState.Unlock()
}

// ProviderRuntimeContext is a memory-only exact instance/model lookup. Aliases
// are supplied by discovery; suffix matches cannot borrow another host's limit.
func ProviderRuntimeContext(baseURL, apiKey, model string) int {
	runtimeContextState.Lock()
	defer runtimeContextState.Unlock()
	return runtimeContextState.snapshots[runtimeContextKey(baseURL, apiKey)].windows[model]
}

func localNativeModelsRoot(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !isLocalDiscoveryHost(u.Hostname()) ||
		!strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// fetchLocalNativeModelInfos distinguishes an unsupported API from a transient
// failure. Unsupported APIs need an explicit Scan/edit before another attempt.
func fetchLocalNativeModelInfos(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, bool, error) {
	if localNativeModelsRoot(baseURL) == "" {
		return nil, false, nil
	}
	runtimeContextState.Lock()
	read := newRuntimeContextReadLocked(runtimeContextKey(baseURL, apiKey))
	runtimeContextState.Unlock()
	defer forgetRuntimeContextRead(read)
	return fetchLocalNativeModelInfosForRead(ctx, baseURL, apiKey, read)
}

func fetchLocalNativeModelInfosForRead(ctx context.Context, baseURL, apiKey string, read *runtimeContextRead) ([]ModelInfo, bool, error) {
	root := localNativeModelsRoot(baseURL)
	if root == "" {
		return nil, false, nil
	}
	var lastErr error
	for _, path := range []string{"/api/v1/models", "/api/v0/models"} {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+path, nil)
		if err != nil {
			return nil, false, err
		}
		if key := CleanAPIKey(apiKey); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := (&http.Client{Timeout: ProviderDiscoveryTimeout}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("local model metadata: HTTP %d", resp.StatusCode)
			continue
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal(body, &envelope) == nil && envelope["models"] == nil && envelope["data"] == nil {
			continue
		}
		models, err := parseProviderModelInfos(body)
		if err != nil {
			lastErr = err
			continue
		}
		rememberRuntimeContextRead(read, models)
		return models, true, nil
	}
	return nil, false, lastErr
}

// RefreshLocalModelContexts is event-driven (user turn/model change), with no
// polling or model inference. Concurrent callers share one bounded discovery;
// canceling the last waiter also cancels its work.
func RefreshLocalModelContexts(ctx context.Context, baseURL, apiKey string) error {
	if localNativeModelsRoot(baseURL) == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := runtimeContextKey(baseURL, apiKey)
	runtimeContextState.Lock()
	if runtimeContextState.unsupported[key] {
		runtimeContextState.Unlock()
		return nil
	}
	flight := runtimeContextState.flights[key]
	if flight == nil {
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		flight = &runtimeContextFlight{done: make(chan struct{}), cancel: cancel, read: newRuntimeContextReadLocked(key)}
		runtimeContextState.flights[key] = flight
		go func() {
			_, supported, err := fetchLocalNativeModelInfosForRead(workCtx, baseURL, apiKey, flight.read)
			cancel()
			runtimeContextState.Lock()
			flight.err, flight.closed = err, true
			if !supported && err == nil && !flight.read.invalidated && runtimeContextState.flights[key] == flight && runtimeContextState.snapshots[key].version <= flight.read.version {
				rememberProviderRuntimeContextsLocked(key, nil, flight.read.version)
				if len(runtimeContextState.unsupported) >= 64 {
					for stale := range runtimeContextState.unsupported {
						delete(runtimeContextState.unsupported, stale)
						break
					}
				}
				runtimeContextState.unsupported[key] = true
			}
			if runtimeContextState.flights[key] == flight {
				delete(runtimeContextState.flights, key)
			}
			delete(runtimeContextState.reads, flight.read)
			close(flight.done)
			runtimeContextState.Unlock()
		}()
	}
	flight.waiters++
	runtimeContextState.Unlock()
	select {
	case <-flight.done:
	case <-ctx.Done():
	}
	runtimeContextState.Lock()
	flight.waiters--
	if !flight.closed && flight.waiters == 0 {
		flight.read.invalidated = true
		flight.cancel()
		if runtimeContextState.flights[key] == flight {
			delete(runtimeContextState.flights, key)
		}
	}
	err := flight.err
	runtimeContextState.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func invalidateProviderRuntimeContexts(baseURL string) {
	prefix := "runtime\x00" + strings.TrimRight(strings.TrimSpace(baseURL), "/") + "\x00"
	runtimeContextState.Lock()
	defer runtimeContextState.Unlock()
	for key := range runtimeContextState.snapshots {
		if strings.HasPrefix(key, prefix) {
			delete(runtimeContextState.snapshots, key)
		}
	}
	for key := range runtimeContextState.unsupported {
		if strings.HasPrefix(key, prefix) {
			delete(runtimeContextState.unsupported, key)
		}
	}
	for read := range runtimeContextState.reads {
		if strings.HasPrefix(read.key, prefix) {
			read.invalidated = true
		}
	}
	for key, flight := range runtimeContextState.flights {
		if strings.HasPrefix(key, prefix) {
			flight.read.invalidated = true
			flight.cancel()
			delete(runtimeContextState.flights, key)
		}
	}
}
