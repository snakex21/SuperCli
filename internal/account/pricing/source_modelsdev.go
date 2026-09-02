// ── Source: models.dev ──
//
// models.dev is the community registry that OpenCode itself consumes
// (its compaction and status-bar math read the same api.json). It is
// the only public source that lists gateway-exclusive models — the
// OpenCode Zen "-free" tier among them — together with their real
// per-tier limits:
//
//	https://models.dev/api.json
//	→ { "opencode": { "models": { "deepseek-v4-flash-free":
//	      { "limit": { "context": 200000, "output": 128000 } } } } }
//
// Without this source those ids resolve nowhere: neither
// pricepertoken nor OpenRouter has ever heard of them, the capability
// seed carries no entry, and ResolveContextWindow falls back to the
// generic remote guess (128k). A free-tier cap of 200k then runs the
// prune trigger at ~77k and auto-compaction at ~115k — silently
// discarding half the usable window on every front-end that shares
// the catalog (TUI, web GUI, embedded bots).
//
// Prices ride along when models.dev publishes them; context-only rows
// are kept as metadata, mirroring how parseOpenRouter treats rows
// that carry a context_length but no usable price.
package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ModelsDevSource fetches context limits and prices from models.dev.
type ModelsDevSource struct{}

func (s *ModelsDevSource) Name() string { return "modelsdev" }

// modelsDevURL is the registry snapshot. One document covers every
// provider, so a single GET per cache TTL is the whole network cost.
const modelsDevURL = "https://models.dev/api.json"

func (s *ModelsDevSource) Fetch(client *http.Client) ([]PriceEntry, error) {
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("modelsdev fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modelsdev: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20MB cap
	if err != nil {
		return nil, fmt.Errorf("modelsdev read: %w", err)
	}
	return parseModelsDev(body)
}

// parseModelsDev decodes the models.dev registry document.
//
// Shape (v1): a top-level object keyed by provider id, each provider
// carrying a "models" map keyed by model id. Per model, "limit" holds
// the window ("context" input tokens, "output" max generation) and
// "cost" the per-1M rates — both optional, since free tiers often
// publish a limit with no price.
func parseModelsDev(data []byte) ([]PriceEntry, error) {
	var raw map[string]struct {
		Models map[string]struct {
			Limit struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			} `json:"limit"`
			Cost struct {
				Input     float64 `json:"input"`
				Output    float64 `json:"output"`
				CacheRead float64 `json:"cache_read"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("modelsdev parse: %w", err)
	}
	now := time.Now().UTC()
	var entries []PriceEntry
	for _, prov := range raw {
		for id, m := range prov.Models {
			if id == "" {
				continue
			}
			// Keep a row when it teaches us anything: a window, or a
			// price. Rows with neither are noise.
			if m.Limit.Context <= 0 && m.Cost.Input <= 0 && m.Cost.Output <= 0 {
				continue
			}
			entries = append(entries, PriceEntry{
				ModelID:          id,
				InputPer1M:       m.Cost.Input,
				CachedInputPer1M: m.Cost.CacheRead,
				OutputPer1M:      m.Cost.Output,
				ContextLength:    m.Limit.Context,
				Source:           "modelsdev",
				FetchedAt:        now,
			})
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("modelsdev: no usable entries in document")
	}
	return entries, nil
}
