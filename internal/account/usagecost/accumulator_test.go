package usagecost

import (
	"reflect"
	"supercli/internal/account/credits"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"testing"
)

func TestAccumulatorPreservesResolveBeforeStreaming(t *testing.T) {
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{
		{Provider: "manual", Model: "priced", InputCost: 3.14, CachedInputCost: 1.23, OutputCost: 10.01},
		{Provider: "manual", Model: "no-cache", InputCost: 3.14, OutputCost: 10.01},
		{Provider: "manual-other", Model: "priced", InputCost: 0.27, OutputCost: 1.95},
	}}
	identities := []session.UsageRecord{
		{Provider: "manual", Model: "priced"},
		{Provider: "manual", Model: "no-cache"},
		{Provider: "manual-other", Model: "priced"},
		{Provider: "openai", EndpointHost: "api.openai.com", Model: "gpt-4o"},
		{Provider: "anthropic", EndpointHost: "api.anthropic.com", Model: "claude-3-5-sonnet-20241022"},
		{ProviderType: config.ProviderCodex, Model: "gpt-5"},
		{ProviderType: config.ProviderEcho},
		{EndpointHost: "127.0.0.1", Model: "fixture"},
		{EndpointHost: "fixture.invalid", Model: "fixture-free"},
		{EndpointHost: "fixture.invalid", Model: "unknown"},
		{Provider: "openrouter", EndpointHost: "openrouter.ai", Model: "gpt-4o"},
	}
	check := func(rows []session.UsageRecord) {
		t.Helper()
		a := NewAccumulator(tc)
		for _, u := range rows {
			a.Add(u)
		}
		got, want := a.Summary(), referenceResolveBeforeStatsStream(tc, rows, session.UsageRecord{})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stream cost %+v != original %+v", got, want)
		}
		if !reflect.DeepEqual(Resolve(tc, rows, session.UsageRecord{}), want) {
			t.Fatal("Resolve changed")
		}
	}
	for i, u := range identities {
		u.Input, u.Output, u.CachedInput = int64(1000003+i), int64(700007+i), int64(150003+i)
		identities[i] = u
		check([]session.UsageRecord{u})
		got, want := Resolve(tc, nil, u), referenceResolveBeforeStatsStream(tc, nil, u)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("preview mismatch %+v != %+v", got, want)
		}
	}
	check(identities)
	var long []session.UsageRecord
	for i := 0; i < 1752; i++ {
		long = append(long, identities[(i*7)%len(identities)])
	}
	check(long)
	for i := range identities {
		check([]session.UsageRecord{identities[i], identities[(i+1)%len(identities)]})
	}
}

func TestAccumulatorSummaryDoesNotAliasLaterCalls(t *testing.T) {
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Model: "priced", InputCost: 2, OutputCost: 4}}}
	a := NewAccumulator(tc)
	empty := a.Summary()
	if empty.State != "unknown" || empty.Calls != 0 {
		t.Fatalf("empty: %+v", empty)
	}
	u := session.UsageRecord{Model: "priced", Input: 1000000, Output: 1000000}
	a.Add(u)
	first := a.Summary()
	a.Add(u)
	second := a.Summary()
	if first.Amount == nil || *first.Amount != 6 || first.Calls != 1 || *second.Amount != 12 || second.Calls != 2 {
		t.Fatalf("snapshot changed: %+v %+v", first, second)
	}
}

func referenceResolveBeforeStatsStream(tc config.TomlConfig, usage []session.UsageRecord, preview session.UsageRecord) Summary {
	out := Summary{Currency: "USD", PricingCurrency: "USD", CacheDiscountKnown: true}
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
	if out.Amount != nil {
		out.BaseAmountUSD = floatPtr(*out.Amount)
	}
	return out
}
