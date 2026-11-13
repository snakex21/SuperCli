package usagecost

import (
	"testing"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestClaudeSubscriptionCorrectsOldAPIQuotesAndExcludesTokenPrice(t *testing.T) {
	amount := 4.5
	u := session.UsageRecord{Provider: "claude", ProviderType: config.ProviderClaude, EndpointHost: "api.anthropic.com", Model: "claude-sonnet-4-6", Input: 1000000, Output: 200000, PriceSnapshot: &session.PriceSnapshot{State: "estimated", Source: "catalog", AmountUSD: &amount}}
	cost := Resolve(config.TomlConfig{}, []session.UsageRecord{u}, session.UsageRecord{})
	if cost.State != "subscription" || cost.Amount != nil || cost.IncludedCalls != 1 || cost.UnknownCalls != 0 {
		t.Fatalf("subscription priced as API: %+v", cost)
	}
	u.ProviderType = config.ProviderAnthropic
	api := QuoteUsage(config.TomlConfig{}, u)
	if api.State != "estimated" || api.Amount != 4.5 {
		t.Fatal("ordinary Anthropic API price changed")
	}
	u.ProviderType = config.ProviderClaude
	currency := NewCurrencyAccumulator(config.TomlConfig{CostCurrency: "PLN"}, nil)
	currency.Add(u)
	if got := currency.Summary(); got.Amount != nil || got.MissingFXCalls != 0 || got.State != "subscription" {
		t.Fatalf("subscription requires exchange rate: %+v", got)
	}
}
