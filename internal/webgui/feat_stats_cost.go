package webgui

import (
	"supercli/internal/account/usagecost"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func resolveStatsCost(tc config.TomlConfig, usage []session.UsageRecord, preview session.UsageRecord) statsCostView {
	return usagecost.Resolve(tc, usage, preview)
}
func quoteUsage(tc config.TomlConfig, u session.UsageRecord) usageQuote {
	q := usagecost.QuoteUsage(tc, u)
	return usageQuote{state: q.State, amount: q.Amount, source: q.Source, rate: q.Rate, rateKnown: q.RateKnown, cacheKnown: q.CacheKnown}
}
func floatPtr(v float64) *float64 { return &v }
