package webgui

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// ensureLocalReasoningMetadata fills the native control missing from saved model
// IDs. It runs only for the active local OpenAI-compatible endpoint, outside
// rendering and model inference. Concurrent GUI reads share a bounded attempt;
// refreshes never poll an unavailable server. Explicit Scan remains available.
func (e *Engine) ensureLocalReasoningMetadata(ctx context.Context) {
	e.mu.RLock()
	cfg, caps := e.cfg, e.caps
	e.mu.RUnlock()
	if caps == nil || cfg.Model == "" || !llm.IsLocalBaseURL(cfg.BaseURL) ||
		(cfg.Provider != config.ProviderOpenAI && cfg.Provider != config.ProviderResponses) {
		return
	}
	if info, ok := caps.Get(cfg.Model); ok && info.ReasoningKnown {
		return
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	key := sha256.Sum256([]byte(base + "\x00" + cfg.APIKey))
	value, _ := e.localReasoningDiscovery.LoadOrStore(key, &sync.Once{})
	value.(*sync.Once).Do(func() {
		// Cancellation of one browser read must not poison the shared discovery.
		discoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		for _, native := range llm.ListLocalNativeModelInfos(discoveryCtx, base, cfg.APIKey) {
			if !native.ReasoningKnown {
				continue
			}
			info, ok := caps.Get(native.ID)
			if !ok {
				info = llm.HeuristicCapabilities(native.ID)
			}
			// Keep independently established vision/tool/context metadata.
			info.Reasoning = native.Reasoning
			info.ReasoningKnown = true
			info.ReasoningToggleOnly = native.ReasoningToggleOnly
			caps.Register(info)
		}
	})
}
