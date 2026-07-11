// Package pricing fetches model prices from external sources and
// merges them into the F16 capability catalog. Sources are tried
// in priority order:
//
//  1. pricepertoken.com — 300+ models, updated daily
//  2. OpenRouter /v1/models — prices in response (F15 probe)
//  3. Hardcoded fallback — internal/credits/cost.go rates
//
// Results are cached for 24h on disk. Manual user overrides
// (SourceUser) always win over fetched prices.
package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
)

// CacheFileName is the on-disk cache name.
const CacheFileName = "pricing_cache.json"

// CacheTTL is how long fetched prices are considered fresh.
const CacheTTL = 24 * time.Hour

// PriceEntry is one model's pricing fetched from an external source.
type PriceEntry struct {
	ModelID          string    `json:"model_id"`
	InputPer1M       float64   `json:"input_per_1m"`
	CachedInputPer1M float64   `json:"cached_input_per_1m,omitempty"`
	OutputPer1M      float64   `json:"output_per_1m"`
	ContextLength    int       `json:"context_length,omitempty"`
	Source           string    `json:"source"`
	FetchedAt        time.Time `json:"fetched_at"`
}

// Cache is the on-disk format for fetched prices.
type Cache struct {
	FetchedAt time.Time    `json:"fetched_at"`
	Entries   []PriceEntry `json:"entries"`
}

// Fetcher fetches prices from external sources.
type Fetcher struct {
	home    string
	client  *http.Client
	sources []Source
}

// Source is an external price source.
type Source interface {
	Name() string
	Fetch(client *http.Client) ([]PriceEntry, error)
}

// NewFetcher creates a price fetcher.
func NewFetcher(home string) *Fetcher {
	return &Fetcher{
		home:   home,
		client: &http.Client{Timeout: 15 * time.Second},
		sources: []Source{
			&PricePerTokenSource{},
			&OpenRouterSource{},
		},
	}
}

// CachePath returns the cache file path inside the resolved data
// directory (portable default: supercli-data next to the executable).
func CachePath(dataDir string) string {
	return filepath.Join(dataDir, CacheFileName)
}

// LoadCache reads the cache from disk. Returns nil if missing
// or stale.
func LoadCache(home string) []PriceEntry {
	path := CachePath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	if time.Since(cache.FetchedAt) > CacheTTL {
		return nil // stale
	}
	return cache.Entries
}

// HasContextMetadata reports whether cached entries contain at least one
// context_length. Older cache files from before context parsing have prices but
// no context windows; callers should apply them immediately but still refresh
// in the background so /models gets context metadata.
func HasContextMetadata(entries []PriceEntry) bool {
	for _, e := range entries {
		if e.ContextLength > 0 {
			return true
		}
	}
	return false
}

// SaveCache writes entries to disk.
func SaveCache(home string, entries []PriceEntry) error {
	path := CachePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cache := Cache{
		FetchedAt: time.Now().UTC(),
		Entries:   entries,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FetchAll tries each source in order, merges results.
// Returns all entries from successful sources.
func (f *Fetcher) FetchAll() []PriceEntry {
	seen := make(map[string]PriceEntry)
	for _, src := range f.sources {
		entries, err := src.Fetch(f.client)
		if err != nil {
			continue // try next source
		}
		for _, e := range entries {
			key := strings.ToLower(e.ModelID)
			if key == "" {
				continue
			}
			if existing, exists := seen[key]; exists {
				seen[key] = mergePriceEntryMetadata(existing, e)
				continue
			}
			seen[key] = e
		}
	}
	all := make([]PriceEntry, 0, len(seen))
	for _, e := range seen {
		all = append(all, e)
	}
	// Sort by model ID for stable output.
	sort.Slice(all, func(i, j int) bool {
		return all[i].ModelID < all[j].ModelID
	})
	return all
}

func mergePriceEntryMetadata(existing, fresh PriceEntry) PriceEntry {
	// Keep the first source's price priority, but do not throw away later
	// metadata. This matters for context windows: pricepertoken can have a
	// price for a model while OpenRouter has the context_length for the same id.
	if existing.InputPer1M == 0 && fresh.InputPer1M > 0 {
		existing.InputPer1M = fresh.InputPer1M
	}
	if existing.CachedInputPer1M == 0 && fresh.CachedInputPer1M > 0 {
		existing.CachedInputPer1M = fresh.CachedInputPer1M
	}
	if existing.OutputPer1M == 0 && fresh.OutputPer1M > 0 {
		existing.OutputPer1M = fresh.OutputPer1M
	}
	if existing.ContextLength == 0 && fresh.ContextLength > 0 {
		existing.ContextLength = fresh.ContextLength
	}
	if existing.Source == "" {
		existing.Source = fresh.Source
	}
	if fresh.FetchedAt.After(existing.FetchedAt) {
		existing.FetchedAt = fresh.FetchedAt
	}
	return existing
}

// ApplyCachedRates loads the on-disk price cache and, when it is
// still fresh (24h TTL), pushes the cached rates into the credits
// package without any network IO. Returns true when fresh cached
// rates were applied — the caller can then skip the network fetch
// entirely. Keeping the fetch off the hot path matters:
// FetchAndUpdate scrapes two external HTTP sources and used to
// run synchronously during startup.
func ApplyCachedRates(home string) bool {
	entries := LoadCache(home)
	if len(entries) == 0 {
		return false
	}
	pushRatesToCredits(entries)
	return true
}

// FetchAndUpdate fetches prices, saves cache, and merges into
// the existing catalog. Manual (SourceUser) entries are never
// overwritten. Also pushes fetched rates into the credits
// package so CostFor/StatusBar use live prices.
func (f *Fetcher) FetchAndUpdate(existing []llm.ModelInfo) []llm.ModelInfo {
	entries := f.FetchAll()
	if len(entries) == 0 {
		return existing
	}
	// Save cache.
	_ = SaveCache(f.home, entries)
	// Push rates into credits package for CostFor/StatusBar.
	pushRatesToCredits(entries)
	// Convert to ModelInfo and merge.
	fresh := make([]llm.ModelInfo, 0, len(entries))
	for _, e := range entries {
		fresh = append(fresh, llm.ModelInfo{
			ID:            e.ModelID,
			InputCost:     e.InputPer1M,
			OutputCost:    e.OutputPer1M,
			ContextLength: e.ContextLength,
			Source:        llm.SourceExternal,
			LastVerified:  e.FetchedAt,
		})
	}
	return llm.MergeCatalog(existing, fresh)
}

// pushRatesToCredits converts PriceEntry (per-1M) into
// credits.Rate (per-1k) and sets them on the credits package.
func pushRatesToCredits(entries []PriceEntry) {
	rates := make(map[string]credits.Rate, len(entries))
	providerRates := make(map[string]credits.Rate)
	for _, e := range entries {
		if e.InputPer1M <= 0 && e.OutputPer1M <= 0 {
			continue
		}
		key := strings.ToLower(e.ModelID)
		// Convert per-1M → per-1k.
		rate := credits.Rate{
			InputPer1k:       e.InputPer1M / 1000.0,
			CachedInputPer1k: e.CachedInputPer1M / 1000.0,
			OutputPer1k:      e.OutputPer1M / 1000.0,
		}
		rates[key] = rate
		if e.Source == "openrouter" {
			providerRates["openrouter/"+key] = rate
		}
	}
	credits.SetFetchedRates(rates)
	if len(providerRates) == 0 {
		credits.SetProviderRates(nil)
	} else {
		credits.SetProviderRates(providerRates)
	}
}

// ── Source: pricepertoken.com ──

// PricePerTokenSource scrapes pricepertoken.com for model prices.
type PricePerTokenSource struct{}

func (s *PricePerTokenSource) Name() string { return "pricepertoken" }

func (s *PricePerTokenSource) Fetch(client *http.Client) ([]PriceEntry, error) {
	// pricepertoken.com has a JSON API at /api/models.
	resp, err := client.Get("https://pricepertoken.com/api/models")
	if err != nil {
		return nil, fmt.Errorf("pricepertoken fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pricepertoken: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB cap
	if err != nil {
		return nil, fmt.Errorf("pricepertoken read: %w", err)
	}
	return parsePricePerToken(body)
}

// parsePricePerToken decodes the pricepertoken.com JSON response.
// Format: [{"model":"gpt-4o","input_price":2.50,"output_price":10.00}, ...]
// Prices are per 1M tokens.
func parsePricePerToken(data []byte) ([]PriceEntry, error) {
	var raw []struct {
		Model       string  `json:"model"`
		InputPrice  float64 `json:"input_price"`
		OutputPrice float64 `json:"output_price"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("pricepertoken parse: %w", err)
	}
	now := time.Now().UTC()
	entries := make([]PriceEntry, 0, len(raw))
	for _, r := range raw {
		if r.Model == "" {
			continue
		}
		entries = append(entries, PriceEntry{
			ModelID:     r.Model,
			InputPer1M:  r.InputPrice,
			OutputPer1M: r.OutputPrice,
			Source:      "pricepertoken",
			FetchedAt:   now,
		})
	}
	return entries, nil
}

// ── Source: OpenRouter /v1/models ──

// OpenRouterSource fetches prices from OpenRouter's /v1/models endpoint.
type OpenRouterSource struct{}

func (s *OpenRouterSource) Name() string { return "openrouter" }

func (s *OpenRouterSource) Fetch(client *http.Client) ([]PriceEntry, error) {
	resp, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		return nil, fmt.Errorf("openrouter fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("openrouter read: %w", err)
	}
	return parseOpenRouter(body)
}

// parseOpenRouter decodes OpenRouter's /v1/models response.
// Format: {"data":[{"id":"openai/gpt-4o","context_length":128000,
// "pricing":{"prompt":"0.0000025","completion":"0.00001"}},...]}
// Prices are per token (not per 1M), so we multiply by 1M; context-only
// rows are kept as metadata but do not create fetched cost rates.
func parseOpenRouter(data []byte) ([]PriceEntry, error) {
	var resp struct {
		Data []struct {
			ID            string          `json:"id"`
			ContextLength json.RawMessage `json:"context_length"`
			TopProvider   struct {
				ContextLength json.RawMessage `json:"context_length"`
			} `json:"top_provider"`
			Pricing struct {
				Prompt         string `json:"prompt"`
				Completion     string `json:"completion"`
				InputCacheRead string `json:"input_cache_read"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("openrouter parse: %w", err)
	}
	now := time.Now().UTC()
	entries := make([]PriceEntry, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID == "" {
			continue
		}
		prompt, _ := parseFlexFloat(m.Pricing.Prompt)
		completion, _ := parseFlexFloat(m.Pricing.Completion)
		cachedInput, _ := parseFlexFloat(m.Pricing.InputCacheRead)
		if prompt < 0 {
			prompt = 0
		}
		if completion < 0 {
			completion = 0
		}
		if cachedInput < 0 {
			cachedInput = 0
		}
		contextLength := parseFlexInt(m.ContextLength)
		if contextLength == 0 {
			contextLength = parseFlexInt(m.TopProvider.ContextLength)
		}
		if prompt <= 0 && completion <= 0 && contextLength <= 0 {
			continue // no usable price or context metadata
		}
		entries = append(entries, PriceEntry{
			ModelID:          m.ID,
			InputPer1M:       prompt * 1_000_000, // per-token → per-1M
			CachedInputPer1M: cachedInput * 1_000_000,
			OutputPer1M:      completion * 1_000_000,
			ContextLength:    contextLength,
			Source:           "openrouter",
			FetchedAt:        now,
		})
	}
	return entries, nil
}

func parseFlexInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int(f)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		f, _ := parseFlexFloat(s)
		return int(f)
	}
	return 0
}

// parseFlexFloat parses a string that might be "0.0025" or 0.0025 (number).
func parseFlexFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	var f float64
	if err := json.Unmarshal([]byte(s), &f); err == nil {
		return f, nil
	}
	// Try as JSON string.
	if err := json.Unmarshal([]byte(`"`+s+`"`), &f); err == nil {
		return f, nil
	}
	return 0, fmt.Errorf("parseFlexFloat: %q", s)
}
