package app

import (
	"supercli/internal/account/pricing"
	"supercli/internal/llm"
)

// applyPricingStartup applies the 24h disk cache synchronously and,
// when needed, kicks a background fetch so live rates appear after
// the TUI is already interactive. Never blocks startup on the network.
func applyPricingStartup(dataDir string, caps *llm.CapabilityRegistry) {
	// F28: external prices (pricepertoken.com, OpenRouter) push
	// fetched rates into the credits package so CostFor/StatusBar
	// use live prices. Non-fatal: if all sources fail, the
	// hardcoded fallback rates in credits/cost.go still work.
	//
	// Startup-latency rule: NEVER hit the network on the startup
	// path. Apply the 24h disk cache synchronously (pure file
	// read); only when it's missing/stale, fetch in the
	// background — rates pop in a second or two after the TUI is
	// already interactive.
	if cachedPrices := pricing.LoadCache(dataDir); len(cachedPrices) > 0 {
		pricing.ApplyCachedRates(dataDir)
		applyPricingMetadata(caps, cachedPrices)
		if !pricing.HasContextMetadata(cachedPrices) {
			fetcher := pricing.NewFetcher(dataDir)
			capsSnapshot := caps.All()
			go func() {
				defer recoverAndLog(dataDir)()
				updated := fetcher.FetchAndUpdate(capsSnapshot)
				applyModelInfoMetadata(caps, updated)
			}()
		}
		return
	}
	fetcher := pricing.NewFetcher(dataDir)
	capsSnapshot := caps.All()
	go func() {
		defer recoverAndLog(dataDir)()
		updated := fetcher.FetchAndUpdate(capsSnapshot)
		applyModelInfoMetadata(caps, updated)
	}()
}

// applyPricingStartupBatch is the batch/bot variant of
// applyPricingStartup. A batch process lives for one turn and exits,
// so a background refresh can never land in time: when the 24h cache
// is stale (the common case for a bot that runs all day), the stale
// entries are still applied synchronously — a context limit learned
// yesterday beats the generic remote fallback guess today — while
// the fresh fetch runs in the background for the NEXT run.
func applyPricingStartupBatch(dataDir string, caps *llm.CapabilityRegistry) {
	cachedPrices := pricing.LoadCache(dataDir)
	if len(cachedPrices) == 0 {
		cachedPrices = pricing.LoadCacheStale(dataDir)
	}
	if len(cachedPrices) > 0 {
		applyPricingMetadata(caps, cachedPrices)
	}
	fetcher := pricing.NewFetcher(dataDir)
	capsSnapshot := caps.All()
	go func() {
		defer recoverAndLog(dataDir)()
		updated := fetcher.FetchAndUpdate(capsSnapshot)
		applyModelInfoMetadata(caps, updated)
	}()
}
