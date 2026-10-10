package usagecost

import (
	"supercli/internal/account/credits"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// Accumulator quotes calls in their original order without retaining a history slice.
type Accumulator struct {
	config                                                               config.TomlConfig
	calls, quotes, manual, estimated, free, subscription, local, unknown int
	actualUnknown, actualIncluded                                        int
	amount                                                               float64
	firstRate                                                            *credits.Rate
	firstSource                                                          string
	sameRate, cacheKnown                                                 bool
}

func NewAccumulator(tc config.TomlConfig) Accumulator {
	return Accumulator{config: tc, sameRate: true, cacheKnown: true}
}

func (a *Accumulator) Add(u session.UsageRecord) { a.observe(u, u.Source != "legacy") }

func (a *Accumulator) observe(u session.UsageRecord, actual bool) {
	if actual {
		a.calls++
	}
	a.quotes++
	q := QuoteUsage(a.config, u)
	if actual {
		switch q.State {
		case "subscription", "local":
			a.actualIncluded++
		case "manual", "estimated", "free":
		default:
			a.actualUnknown++
		}
	}
	switch q.State {
	case "manual":
		a.manual++
		a.amount += q.Amount
	case "estimated":
		a.estimated++
		a.amount += q.Amount
	case "free":
		a.free++
	case "subscription":
		a.subscription++
	case "local":
		a.local++
	default:
		a.unknown++
	}
	if q.RateKnown {
		if a.firstRate == nil {
			r := q.Rate
			a.firstRate = &r
			a.firstSource = q.Source
		} else if *a.firstRate != q.Rate || a.firstSource != q.Source {
			a.sameRate = false
		}
		a.cacheKnown = a.cacheKnown && q.CacheKnown
	}
}

func (a *Accumulator) Summary() Summary {
	out := Summary{Currency: "USD", PricingCurrency: "USD", CacheDiscountKnown: a.cacheKnown}
	if a.quotes == 0 {
		out.State = "unknown"
		return out
	}
	out.Calls = a.calls
	out.UnknownCalls = a.actualUnknown
	out.IncludedCalls = a.actualIncluded
	priced := a.manual + a.estimated
	switch {
	case priced > 0:
		out.State = "estimated"
		out.Estimated = true
		if a.manual == a.quotes && a.estimated == 0 {
			out.State = "manual"
			out.Estimated = false
			out.Manual = true
		}
		out.Amount = floatPtr(a.amount)
		out.Partial = a.unknown+a.subscription+a.local+a.free > 0
	case a.free == a.quotes:
		out.State = "free"
		out.Amount = floatPtr(0)
		out.Source = "free"
	case a.subscription == a.quotes:
		out.State = "subscription"
		out.Source = "subscription"
	case a.local == a.quotes:
		out.State = "local"
		out.Source = "local"
	default:
		out.State = "unknown"
		out.Partial = a.calls > 0 && a.unknown != a.quotes
	}
	if a.firstRate != nil && a.sameRate && priced == a.quotes {
		in := a.firstRate.InputPer1k * 1000
		cached := a.firstRate.CachedInputPer1k * 1000
		output := a.firstRate.OutputPer1k * 1000
		out.InputPerMillion = floatPtr(in)
		if cached > 0 {
			out.CachedInputPerMillion = floatPtr(cached)
		}
		out.OutputPerMillion = floatPtr(output)
		out.Source = a.firstSource
	} else if priced > 0 {
		out.Source = "mixed"
	}
	if out.Amount != nil {
		out.BaseAmountUSD = floatPtr(*out.Amount)
	}
	return out
}
