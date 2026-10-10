package config

import (
	"context"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
)

// RefreshLocalContextWindows refreshes native runtime metadata once per user
// operation. Discovery is optional: unavailable or unsupported servers keep the
// ordinary context-window fallback. Cancellation of the operation still stops
// its wait. No configuration or catalog is persisted here.
func RefreshLocalContextWindows(ctx context.Context, configs ...Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	seen := make(map[string]bool, len(configs))
	var waiting sync.WaitGroup
	for _, cfg := range configs {
		if cfg.Provider != ProviderOpenAI && cfg.Provider != ProviderResponses {
			continue
		}
		base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
		if !llm.IsLocalBaseURL(base) {
			continue
		}
		key := llm.CleanAPIKey(cfg.APIKey)
		identity := base + "\x00" + key
		if seen[identity] {
			continue
		}
		seen[identity] = true
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			_ = llm.RefreshLocalModelContexts(refreshCtx, base, key)
		}()
	}
	waiting.Wait()
	return ctx.Err()
}
