package usagecost

import (
	"net"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func Resolve(tc config.TomlConfig, usage []session.UsageRecord, preview session.UsageRecord) Summary {
	out := Summary{Currency: "USD", CacheDiscountKnown: true}
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
		q := QuoteUsage(tc, u)
		switch q.State {
		case "manual":
			manual++
			amount += q.Amount
		case "estimated":
			estimated++
			amount += q.Amount
		case "free":
			free++
		case "subscription":
			subscription++
		case "local":
			local++
		default:
			unknown++
		}
		if q.RateKnown {
			if firstRate == nil {
				r := q.Rate
				firstRate = &r
				firstSource = q.Source
			} else if *firstRate != q.Rate || firstSource != q.Source {
				sameRate = false
			}
			out.CacheDiscountKnown = out.CacheDiscountKnown && q.CacheKnown
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

func QuoteUsage(tc config.TomlConfig, u session.UsageRecord) Quote {
	if manual, ok := manualPrice(tc.ModelPrices, u.Provider, u.Model); ok {
		rate := credits.Rate{
			InputPer1k: manual.InputCost / 1000, CachedInputPer1k: manual.CachedInputCost / 1000,
			OutputPer1k: manual.OutputCost / 1000,
		}
		amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
		return Quote{State: "manual", Amount: amount, Source: "manual", Rate: rate, RateKnown: true, CacheKnown: cacheKnown}
	}
	if u.ProviderType == config.ProviderCodex {
		return Quote{State: "subscription", Source: "subscription"}
	}
	if u.ProviderType == config.ProviderEcho || isLocalEndpointHost(u.EndpointHost) {
		return Quote{State: "local", Source: "local"}
	}
	if llm.IsFreeModelID(u.Model) {
		return Quote{State: "free", Source: "free"}
	}

	rateProvider := u.Provider
	if u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai" {
		rateProvider = "openrouter"
	}
	rate, source, ok := credits.LookupRateForProvider(rateProvider, u.Model)
	if !ok {
		return Quote{State: "unknown"}
	}
	endpointRate := strings.Contains(source, "(endpoint)")
	isOpenRouter := u.EndpointHost == "openrouter.ai" || u.EndpointHost == "www.openrouter.ai"
	if isOpenRouter && !endpointRate {
		return Quote{State: "unknown"}
	}
	if !isOfficialMeteredHost(u.EndpointHost) {
		return Quote{State: "unknown"}
	}
	amount, cacheKnown := credits.CostAtRate(rate, u.Input, u.Output, u.CachedInput)
	sourceLabel := "official"
	if endpointRate {
		sourceLabel = "provider"
	} else if strings.Contains(source, "(fetched)") {
		sourceLabel = "catalog"
	}
	return Quote{State: "estimated", Amount: amount, Source: sourceLabel, Rate: rate, RateKnown: true, CacheKnown: cacheKnown}
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

type Summary struct {
	State                 string   `json:"state"`
	Amount                *float64 `json:"amount"`
	Currency              string   `json:"currency"`
	Source                string   `json:"source,omitempty"`
	Estimated             bool     `json:"estimated"`
	Partial               bool     `json:"partial"`
	Calls                 int      `json:"calls"`
	UnknownCalls          int      `json:"unknown_calls"`
	IncludedCalls         int      `json:"included_calls"`
	InputPerMillion       *float64 `json:"input_per_million,omitempty"`
	CachedInputPerMillion *float64 `json:"cached_input_per_million,omitempty"`
	OutputPerMillion      *float64 `json:"output_per_million,omitempty"`
	CacheDiscountKnown    bool     `json:"cache_discount_known"`
	Manual                bool     `json:"manual"`
}

type Quote struct {
	State      string
	Amount     float64
	Source     string
	Rate       credits.Rate
	RateKnown  bool
	CacheKnown bool
}
