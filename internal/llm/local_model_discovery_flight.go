package llm

import (
	"context"
	"crypto/sha256"
	"strings"
)

const maxLocalModelDiscoveryFlights = 64

// Raw native discovery and fresh runtime refresh have different lifetimes and
// freshness contracts. Only overlapping raw callers share this collection;
// RefreshLocalModelContexts continues to own its separate read and flight.
var localNativeModelDiscovery localModelDiscoveryFlights

type localModelDiscoveryKey struct {
	base       string
	credential [sha256.Size]byte
}

type localModelDiscoveryFlight struct {
	done      chan struct{}
	cancel    context.CancelFunc
	read      *runtimeContextRead
	waiters   int
	closed    bool
	models    []ModelInfo
	supported bool
	err       error
}

// All bookkeeping uses runtimeContextState's mutex, including invalidation and
// read-version registration. No second lock or completed result cache is needed.
type localModelDiscoveryFlights struct {
	pending map[localModelDiscoveryKey]*localModelDiscoveryFlight
}

func localModelDiscoveryIdentity(baseURL, apiKey string) localModelDiscoveryKey {
	return localModelDiscoveryKey{
		base:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		credential: sha256.Sum256([]byte(CleanAPIKey(apiKey))),
	}
}

func (g *localModelDiscoveryFlights) fetch(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, bool, error) {
	if localNativeModelsRoot(baseURL) == "" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := localModelDiscoveryIdentity(baseURL, apiKey)
	runtimeContextState.Lock()
	flight := g.pending[key]
	if flight != nil && flight.read.invalidated {
		// A newer snapshot or explicit edit made this read obsolete. Existing
		// waiters may still receive it, but a new caller must start fresh.
		delete(g.pending, key)
		flight = nil
	}
	if flight == nil && len(g.pending) >= maxLocalModelDiscoveryFlights {
		// Overflow keeps the original independent, caller-owned behavior.
		read := newRuntimeContextReadLocked(runtimeContextKey(baseURL, apiKey))
		runtimeContextState.Unlock()
		defer forgetRuntimeContextRead(read)
		return fetchLocalNativeModelInfosForRead(ctx, baseURL, apiKey, read)
	}
	start := flight == nil
	var workCtx context.Context
	if start {
		var cancel context.CancelFunc
		// Each waiter keeps its own deadline. Cancel the transport only when
		// the last waiter leaves; individual HTTP requests retain their normal
		// discovery timeout and v1/v0 fallback behavior.
		workCtx, cancel = context.WithCancel(context.WithoutCancel(ctx))
		flight = &localModelDiscoveryFlight{
			done: make(chan struct{}), cancel: cancel,
			read: newRuntimeContextReadLocked(runtimeContextKey(baseURL, apiKey)),
		}
		if g.pending == nil {
			g.pending = make(map[localModelDiscoveryKey]*localModelDiscoveryFlight)
		}
		g.pending[key] = flight
	}
	flight.waiters++
	runtimeContextState.Unlock()
	if start {
		go g.collect(workCtx, key, flight, baseURL, apiKey)
	}
	select {
	case <-flight.done:
	case <-ctx.Done():
	}
	runtimeContextState.Lock()
	flight.waiters--
	if !flight.closed && flight.waiters == 0 {
		flight.read.invalidated = true
		flight.cancel()
		if g.pending[key] == flight {
			delete(g.pending, key)
		}
	}
	models, supported, err := flight.models, flight.supported, flight.err
	runtimeContextState.Unlock()
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	return cloneLocalModelDiscoveryResult(models), supported, err
}

func (g *localModelDiscoveryFlights) collect(ctx context.Context, key localModelDiscoveryKey, flight *localModelDiscoveryFlight, baseURL, apiKey string) {
	models, supported, err := fetchLocalNativeModelInfosForRead(ctx, baseURL, apiKey, flight.read)
	flight.cancel()
	runtimeContextState.Lock()
	flight.models, flight.supported, flight.err, flight.closed = models, supported, err, true
	if g.pending[key] == flight {
		delete(g.pending, key)
	}
	delete(runtimeContextState.reads, flight.read)
	close(flight.done)
	runtimeContextState.Unlock()
}

// detachLocked preserves the existing direct discovery contract: invalidation
// prevents publication/rejoining, but does not take a response away from callers
// already waiting. Fresh runtime flights retain their own cancellation policy.
func (g *localModelDiscoveryFlights) detachLocked(baseURL string) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for key, flight := range g.pending {
		if key.base == base {
			flight.read.invalidated = true
			delete(g.pending, key)
		}
	}
}

func cloneLocalModelDiscoveryResult(models []ModelInfo) []ModelInfo {
	if models == nil {
		return nil
	}
	// ModelInfo contains values, immutable strings and time.Time, with no
	// mutable nested slices/maps. Copying the slice isolates every caller's
	// records and preserves runtime-only fields and non-nil empty results.
	out := make([]ModelInfo, len(models))
	copy(out, models)
	return out
}
