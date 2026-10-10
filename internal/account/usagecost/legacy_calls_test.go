package usagecost

import (
	"testing"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestLegacyAggregatesPreserveFrozenAmountWithoutInventingCalls(t *testing.T) {
	amount := 12.5
	legacy := session.UsageRecord{Source: "legacy", Input: 10000, Output: 1000,
		PriceSnapshot: &session.PriceSnapshot{State: "estimated", AmountUSD: &amount, Source: "official", Legacy: true}}
	a := NewAccumulator(config.TomlConfig{})
	a.Add(legacy)
	first := a.Summary()
	if first.Calls != 0 || first.UnknownCalls != 0 || first.IncludedCalls != 0 || first.Amount == nil || *first.Amount != amount {
		t.Fatalf("legacy amount or real calls changed: %+v", first)
	}
	for _, state := range []string{"unknown", "local", "subscription"} {
		a.Add(session.UsageRecord{Source: "legacy", PriceSnapshot: &session.PriceSnapshot{State: state, Legacy: true}})
	}
	a.Add(session.UsageRecord{Source: "main", PriceSnapshot: &session.PriceSnapshot{State: "unknown"}})
	a.Add(session.UsageRecord{Source: "compact", PriceSnapshot: &session.PriceSnapshot{State: "local"}})
	a.Add(session.UsageRecord{Source: "task", PriceSnapshot: &session.PriceSnapshot{State: "subscription"}})
	got := a.Summary()
	if got.Calls != 3 || got.UnknownCalls != 1 || got.IncludedCalls != 2 || got.Amount == nil || *got.Amount != amount {
		t.Fatalf("legacy records inflated real call coverage: %+v", got)
	}
	if first.Amount == nil || *first.Amount != amount || first.Calls != 0 {
		t.Fatal("later records mutated the earlier summary")
	}
}
