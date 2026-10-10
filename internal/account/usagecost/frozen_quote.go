package usagecost

import (
	"math"
	"time"

	"supercli/internal/account/credits"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// FreezeQuote records what was knowable when a call was accounted for. Later
// catalog/config changes must not turn an unknown or included call into a new
// charge, or reprice an already quoted call.
func FreezeQuote(tc config.TomlConfig, u session.UsageRecord, legacy bool) session.PriceSnapshot {
	if u.PriceSnapshot != nil {
		out := *u.PriceSnapshot
		if out.AmountUSD != nil {
			out.AmountUSD = floatPtr(*out.AmountUSD)
		}
		return out
	}
	if u.Input < 0 {
		u.Input = 0
	}
	if u.Output < 0 {
		u.Output = 0
	}
	if u.CachedInput < 0 {
		u.CachedInput = 0
	}
	if u.CachedInput > u.Input {
		u.CachedInput = u.Input
	}
	if u.Reasoning < 0 {
		u.Reasoning = 0
	}
	if u.Reasoning > u.Output {
		u.Reasoning = u.Output
	}
	now := time.Now().UTC()
	created := u.CreatedAt
	if created.IsZero() {
		created = now
	}
	q := QuoteUsage(tc, u)
	out := session.PriceSnapshot{State: q.State, Source: q.Source, CacheKnown: q.CacheKnown,
		Legacy: legacy, PriceDate: now.Format("2006-01-02"), UsageDay: created.In(time.Local).Format("2006-01-02")}
	switch q.State {
	case "manual", "estimated":
		if !finiteNonnegative(q.Amount) || !finiteNonnegative(q.Rate.InputPer1k) ||
			!finiteNonnegative(q.Rate.CachedInputPer1k) || !finiteNonnegative(q.Rate.OutputPer1k) {
			out.State, out.Source = "unknown", ""
			return out
		}
		out.AmountUSD = floatPtr(q.Amount)
		out.InputPerMillion = q.Rate.InputPer1k * 1000
		out.CachedInputPerMillion = q.Rate.CachedInputPer1k * 1000
		out.OutputPerMillion = q.Rate.OutputPer1k * 1000
	case "free":
		out.AmountUSD = floatPtr(0)
	}
	return out
}

func quoteFrozen(snapshot session.PriceSnapshot) Quote {
	q := Quote{State: snapshot.State, Source: snapshot.Source, CacheKnown: snapshot.CacheKnown}
	switch snapshot.State {
	case "manual", "estimated":
		if snapshot.AmountUSD == nil || !finiteNonnegative(*snapshot.AmountUSD) ||
			!finiteNonnegative(snapshot.InputPerMillion) || !finiteNonnegative(snapshot.CachedInputPerMillion) ||
			!finiteNonnegative(snapshot.OutputPerMillion) {
			return Quote{State: "unknown"}
		}
		q.Amount, q.RateKnown = *snapshot.AmountUSD, true
		q.Rate = credits.Rate{InputPer1k: snapshot.InputPerMillion / 1000,
			CachedInputPer1k: snapshot.CachedInputPerMillion / 1000, OutputPer1k: snapshot.OutputPerMillion / 1000}
	case "free", "local", "subscription", "unknown":
		// Preserve the classification even if the current catalog differs.
	default:
		return Quote{State: "unknown"}
	}
	return q
}

func finiteNonnegative(v float64) bool { return v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }
