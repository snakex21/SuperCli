package llm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ProviderDiscoveryTimeout caps how long a /v1/models fetch may take.
// The payload is small (a few KB of JSON), but payload size is not what
// sets the latency: gateways that queue, cold-start, or fan out to an
// upstream regularly need 15-30s to answer, and a 10s cap made those
// providers impossible to add at all. This is a one-off configuration
// path, not the agent's hot loop, so waiting is cheaper than a false
// rejection. Callers that wrap discovery in their own context must use
// this same budget so the two limits cannot disagree.
const ProviderDiscoveryTimeout = 45 * time.Second

// IsTimeoutError reports whether err is a deadline/timeout rather than a
// refusal. The distinction matters because a timeout is inconclusive: the
// provider may be perfectly usable and merely slow, while a 401 or an
// unresolvable host is a definite answer. Callers use this to decide
// between rejecting a provider and accepting it with a warning.
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// http.Client.Timeout surfaces as an *url.Error whose message carries
	// "Client.Timeout"; some transports lose the net.Error wrapping.
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "deadline exceeded") || strings.Contains(low, "client.timeout") || strings.Contains(low, "timeout awaiting")
}

// providerListCacheTTL is how long a successful /v1/models
// response is reused before re-fetching. One hour: long enough
// that menu opens and rescans are free, short enough that a
// provider enabling/retiring models surfaces the same day.
const providerListCacheTTL = time.Hour

// providerListCache memoizes successful ListProviderModels calls keyed by
// protocol, baseURL, and a one-way credential fingerprint. Errors are never
// cached. Guarded by its own
// mutex; the map is tiny (one entry per configured provider).
var providerListCache = struct {
	mu sync.Mutex
	m  map[string]providerListEntry
}{m: make(map[string]providerListEntry)}

type providerListEntry struct {
	models  []ModelInfo
	fetched time.Time
}

func providerModelsCacheKey(protocol, baseURL, apiKey string) string {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	sum := sha256.Sum256([]byte(CleanAPIKey(apiKey)))
	return protocol + "\x00" + cleanBase + "\x00" + fmt.Sprintf("%x", sum[:8])
}

// InvalidateProviderModelCache forces the next discovery request for baseURL
// to hit the server. Provider edits call this before their verification scan
// so a success cached under old credentials cannot hide a 401.
func InvalidateProviderModelCache(baseURL string) {
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	providerListCache.mu.Lock()
	defer providerListCache.mu.Unlock()
	for key := range providerListCache.m {
		if strings.HasPrefix(key, "openai\x00"+cleanBase+"\x00") || strings.HasPrefix(key, "anthropic\x00"+cleanBase+"\x00") {
			delete(providerListCache.m, key)
		}
	}
}

// ListProviderModels returns the model ids exposed
// by a provider's /v1/models endpoint. The result
// is what the provider advertises; the caller
// feeds each id into HeuristicCapabilities to get
// capability flags.
//
// baseURL is the API root (e.g.
// https://api.openai.com/v1). A trailing slash is
// tolerated. An empty baseURL is an error. A
// non-2xx response is an error (we do not return
// partial data — silently dropping a real failure
// would corrupt the catalog). A 2xx with empty
// data is OK and returns an empty slice.
//
// If apiKey is non-empty, it is sent as a Bearer
// token. Some local servers (llama.cpp, LM Studio)
// ignore the header; some reject missing auth.
// Callers can pass an empty key for the unauth
// case.
func ListProviderModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	models, err := ListProviderModelInfos(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids, nil
}

// ListProviderModelInfos returns provider-advertised models together with
// modality and context metadata when the endpoint publishes it. Missing
// metadata remains unknown; model names are never used to infer vision.
func ListProviderModelInfos(ctx context.Context, baseURL, apiKey string) ([]ModelInfo, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("llm: ListProviderModels: baseURL is empty")
	}
	cacheKey := providerModelsCacheKey("openai", baseURL, apiKey)
	providerListCache.mu.Lock()
	if e, ok := providerListCache.m[cacheKey]; ok && time.Since(e.fetched) < providerListCacheTTL {
		models := append([]ModelInfo(nil), e.models...)
		providerListCache.mu.Unlock()
		return models, nil
	}
	providerListCache.mu.Unlock()
	u := ResolveOpenAIEndpoints(baseURL).Models
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	if key := CleanAPIKey(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	ApplyOpenCodeZenHeaders(req, baseURL)
	client := &http.Client{Timeout: ProviderDiscoveryTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// "context deadline exceeded" tells the user neither that this was a
		// timeout nor how long we waited, so it reads like a rejection. Say
		// what happened and what it implies.
		if IsTimeoutError(err) {
			return nil, fmt.Errorf("llm: ListProviderModels: %s did not answer within %s - the provider is slow or does not implement /v1/models: %w", u, ProviderDiscoveryTimeout, err)
		}
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	defer resp.Body.Close()
	// Cap the body at 4 MB. Real /v1/models
	// responses are a few KB; 4 MB covers the
	// wildest local llama.cpp catalogs.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: ListProviderModels: status %d: %s", resp.StatusCode, body)
	}
	out, err := parseProviderModelInfos(body)
	if err != nil {
		return nil, fmt.Errorf("llm: ListProviderModels: parse: %w", err)
	}
	providerListCache.mu.Lock()
	providerListCache.m[cacheKey] = providerListEntry{models: append([]ModelInfo(nil), out...), fetched: time.Now()}
	providerListCache.mu.Unlock()
	return out, nil
}
