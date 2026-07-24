package webgui

import (
	"net"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func resolveStatsCost(tc config.TomlConfig, usage []session.UsageRecord, preview session.UsageRecord) statsCostView {
	out := statsCostView{Currency: "USD", CacheDiscountKnown: true}
	actualCalls := len(usage)
	if len(usage) == 0 {
		usage = []session.UsageRecord{preview}
	}
	var manual, estimated, free, subscription, local, unknown int
	var amount float64
	var firstRate *credits.Rate
	firstSource := ""
	sameRate := true
	for _, u := range usage {
		q := quoteUsage(tc, u)
		switch q.state {
		case "manual":
			manual++
			amount += q.amount
		case "estimated":
			estimated++
			amount += q.amount
		case "free":
			free++
		case "subscription":
			subscription++
		case "local":
			local++
		default:
			unknown++
		}
		if q.rateKnown {
			if firstRate == nil {
				r := q.rate
				firstRate = &r
				firstSource = q.source
			} else if *firstRate != q.rate || firstSource != q.source {
				sameRate = false
			}
			out.CacheDiscountKnown = out.CacheDiscountKnown && q.cacheKnown
		}
	}
	out.Calls = actualCalls
	if actualCalls > 0 {
		out.UnknownCalls = unknown
		out.IncludedCalls = subscription + local
	}
	priced := manual + estimated
	switch {
	case priced > 0:
		out.State = "estimated"
		out.Estimated = true
		if manual == len(usage) && estimated == 0 {
			out.State = "manual"
			out.Estimated = false
			out.Manual = true
		}
		out.Amount = floatPtr(amount)
		out.Partial = unknown+subscription+local+free > 0
	case free == len(usage):
		out.State = "free"
		out.Amount = floatPtr(0)
		out.Source = "free"
	case subscription == len(usage):
		out.State = "subscription"
		out.Source = "subscription"
	case local == len(usage):
		out.State = "local"
		out.Source = "local"
	default:
		out.State = "unknown"
		out.Partial = actualCalls > 0 && unknown != len(usage)
	}
	if firstRate != nil && sameRate && priced == len(usage) {
		in := firstRate.InputPer1k * 1000
		cached := firstRate.CachedInputPer1k * 1000
		output := firstRate.OutputPer1k * 1000
		out.InputPerMillion = floatPtr(in)
		if cached > 0 {
			out.CachedInputPerMillion = floatPtr(cached)
		}
		out.OutputPerMillion = floatPtr(output)
		out.Source = firstSource
	} else if priced > 0 {
		out.Source = "mixed"
	}
	return out
}

func quoteUsage(tc config.TomlConfig, u session.UsageRecord) usageQuote {
	if manual, ok := manualPrice(tc.ModelPrices, u.Provider, u.Model); ok {
		rate := credits.Rate{
			InputPer1k: manual.InputCost / 1000, CachedInputPer1k: manual.CachedInputCost / 1000,
			OutputPer1k: manual.OutputCost / 1000,
		}
		amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
		return usageQuote{state: "manual", amount: amount, source: "manual", rate: rate, rateKnown: true, cacheKnown: cacheKnown}
	}
	if u.ProviderType == config.ProviderCodex {
		return usageQuote{state: "subscription", source: "subscription"}
	}
	if u.ProviderType == config.ProviderEcho || isLocalEndpointHost(u.EndpointHost) {
		return usageQuote{state: "local", source: "local"}
	}
	if llm.IsFreeModelID(u.Model) {
		return usageQuote{state: "free", source: "free"}
	}

	rateProvider := u.Provider
	if u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai" {
		rateProvider = "openrouter"
	}
	rate, source, ok := credits.LookupRateForProvider(rateProvider, u.Model)
	if !ok {
		return usageQuote{state: "unknown"}
	}
	endpointRate := strings.Contains(source, "(endpoint)")
	isOpenRouter := u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai"
	if isOpenRouter && !endpointRate {
		return usageQuote{state: "unknown"}
	}
	if !isOfficialMeteredHost(u.EndpointHost) {
		return usageQuote{state: "unknown"}
	}
	amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
	sourceLabel := "official"
	if endpointRate {
		sourceLabel = "provider"
	} else if strings.Contains(source, "(fetched)") {
		sourceLabel = "catalog"
	}
	return usageQuote{state: "estimated", amount: amount, source: sourceLabel, rate: rate, rateKnown: true, cacheKnown: cacheKnown}
}

func manualPrice(prices []config.ModelPriceConf, provider, model string) (config.ModelPriceConf, bool) {
	for _, p := range prices {
		if p.Provider == provider && strings.EqualFold(p.Model, model) {
			return p, true
		}
	}
	for _, p := range prices {
		if p.Provider == "" && strings.EqualFold(p.Model, model) {
			return p, true
		}
	}
	return config.ModelPriceConf{}, false
}

func isLocalEndpointHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func isOfficialMeteredHost(host string) bool {
	switch strings.TrimSpace(strings.ToLower(host)) {
	case "api.openai.com", "api.anthropic.com", "api.deepseek.com",
		"generativelanguage.googleapis.com", "api.mistral.ai", "api.groq.com",
		"api.together.xyz", "api.x.ai", "openrouter.ai", "www.openrouter.ai":
		return true
	default:
		return false
	}
}

func floatPtr(v float64) *float64 { return &v }
