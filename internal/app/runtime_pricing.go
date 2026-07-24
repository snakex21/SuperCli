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
