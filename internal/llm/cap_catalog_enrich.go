package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Some gateways publish only {id, object, created, owned_by} from /models while
// using the exact ids of a public catalog that does carry context_length. The
// map below names that catalog per gateway host; everything else is unchanged.
const openRouterPublicCatalogURL = "https://openrouter.ai/api/v1/models"

const (
	catalogEnrichTTL         = 6 * time.Hour
	catalogEnrichFailureTTL  = 5 * time.Minute
	catalogEnrichTimeout     = 15 * time.Second
	catalogEnrichMaxBodySize = 16 << 20
)

var catalogEnrichSources = struct {
	mu sync.RWMutex
	m  map[string]string
}{m: map[string]string{
	"api.cline.bot": openRouterPublicCatalogURL,
}}

type catalogEnrichEntry struct {
	models  map[string]ModelInfo
	fetched time.Time
	err     error
}

var catalogEnrichCache = struct {
	mu sync.Mutex
	m  map[string]catalogEnrichEntry
}{m: make(map[string]catalogEnrichEntry)}

// RegisterCatalogEnrichSource maps a gateway host to a public catalog URL and
// returns a function restoring the previous mapping. Tests use it to point an
// httptest host at a fixture catalog without touching the real network.
func RegisterCatalogEnrichSource(host, catalogURL string) (restore func()) {
	host = strings.ToLower(strings.TrimSpace(host))
	catalogEnrichSources.mu.Lock()
	previous, existed := catalogEnrichSources.m[host]
	catalogEnrichSources.m[host] = catalogURL
	catalogEnrichSources.mu.Unlock()
	return func() {
		catalogEnrichSources.mu.Lock()
		if existed {
			catalogEnrichSources.m[host] = previous
		} else {
			delete(catalogEnrichSources.m, host)
		}
		catalogEnrichSources.mu.Unlock()
	}
}

// CatalogEnrichmentURL returns the public catalog used to fill missing
// metadata for baseURL, or "" when the endpoint needs no enrichment.
func CatalogEnrichmentURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	catalogEnrichSources.mu.RLock()
	defer catalogEnrichSources.mu.RUnlock()
	return catalogEnrichSources.m[strings.ToLower(u.Hostname())]
}

// EnrichModelInfos fills ContextLength (and missing prices) for models of an
// endpoint whose own /models omits them, using one cached, bounded fetch of the
// mapped public catalog. Only exact id matches are used. Any failure returns
// the input unchanged, so discovery behaves exactly as before.
func EnrichModelInfos(ctx context.Context, baseURL string, models []ModelInfo) []ModelInfo {
	catalogURL := CatalogEnrichmentURL(baseURL)
	if catalogURL == "" || len(models) == 0 {
		return models
	}
	missing := false
	for _, m := range models {
		if m.ContextLength <= 0 {
			missing = true
			break
		}
	}
	if !missing {
		return models
	}
	catalog, err := publicCatalog(ctx, catalogURL)
	if err != nil || len(catalog) == 0 {
		return models
	}
	out := append([]ModelInfo(nil), models...)
	for i := range out {
		info, ok := catalog[out[i].ID]
		if !ok {
			continue
		}
		if out[i].ContextLength <= 0 && info.ContextLength > 0 {
			out[i].ContextLength = info.ContextLength
		}
		if out[i].InputCost <= 0 && info.InputCost > 0 {
			out[i].InputCost = info.InputCost
		}
		if out[i].OutputCost <= 0 && info.OutputCost > 0 {
			out[i].OutputCost = info.OutputCost
		}
	}
	return out
}

func publicCatalog(ctx context.Context, catalogURL string) (map[string]ModelInfo, error) {
	// Holding the lock across the fetch collapses concurrent scans into one
	// request; the catalog is fetched at most once per TTL per URL.
	catalogEnrichCache.mu.Lock()
	defer catalogEnrichCache.mu.Unlock()
	if e, ok := catalogEnrichCache.m[catalogURL]; ok {
		ttl := catalogEnrichTTL
		if e.err != nil {
			ttl = catalogEnrichFailureTTL
		}
		if time.Since(e.fetched) < ttl {
			return e.models, e.err
		}
	}
	models, err := fetchPublicCatalog(ctx, catalogURL)
	catalogEnrichCache.m[catalogURL] = catalogEnrichEntry{models: models, fetched: time.Now(), err: err}
	return models, err
}

func fetchPublicCatalog(ctx context.Context, catalogURL string) (map[string]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, catalogEnrichTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: catalogEnrichTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: public catalog: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, catalogEnrichMaxBodySize))
	if err != nil {
		return nil, err
	}
	return parsePublicCatalog(body)
}

func parsePublicCatalog(body []byte) (map[string]ModelInfo, error) {
	var payload struct {
		Data []struct {
			ID            string          `json:"id"`
			ContextLength json.RawMessage `json:"context_length"`
			TopProvider   struct {
				ContextLength json.RawMessage `json:"context_length"`
			} `json:"top_provider"`
			Pricing struct {
				Prompt     json.RawMessage `json:"prompt"`
				Completion json.RawMessage `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: public catalog: parse: %w", err)
	}
	out := make(map[string]ModelInfo, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		info := ModelInfo{
			ID:            id,
			ContextLength: firstPositiveInt(m.ContextLength, m.TopProvider.ContextLength),
			InputCost:     perTokenToPerMillion(m.Pricing.Prompt),
			OutputCost:    perTokenToPerMillion(m.Pricing.Completion),
			Source:        SourceExternal,
		}
		if info.ContextLength <= 0 && info.InputCost <= 0 && info.OutputCost <= 0 {
			continue
		}
		out[id] = info
	}
	return out, nil
}

// perTokenToPerMillion converts OpenRouter's per-token price (a string or a
// number) to the per-1M figure ModelInfo uses. Negative sentinels ("-1" for
// dynamic routers) and unparsable values are treated as unknown.
func perTokenToPerMillion(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0
		}
		if f, err = strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
			return 0
		}
	}
	if f <= 0 {
		return 0
	}
	return f * 1_000_000
}
