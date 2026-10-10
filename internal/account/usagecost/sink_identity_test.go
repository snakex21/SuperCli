package usagecost

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestCallUsageKeepsActualHelperConnectionAndFrozenPrice(t *testing.T) {
	tc := config.TomlConfig{Providers: []config.ProviderConf{
		{Name: "main", Type: "openai", BaseURL: "https://gateway.example/main", APIKey: "main-secret", Model: "m"},
		{Name: "worker", Type: "openai", BaseURL: "https://gateway.example/work", APIKey: "worker-secret", Model: "m"},
	}, ModelPrices: []config.ModelPriceConf{{Provider: "main", Model: "m", InputCost: 100}, {Provider: "worker", Model: "m", InputCost: 2, CachedInputCost: 1}}}
	stat := llm.CallStat{ProviderType: "openai", EndpointHost: "gateway.example", ConnectionKey: llm.ProviderConnectionKey("openai", "https://gateway.example/work", "worker-secret"), Model: "m", Purpose: llm.PurposeTask, TokensIn: 1_000_000, TokensCached: 400_000, TokensOut: 50, TokensReasoning: 20, StartedAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	u := CallUsage(tc, stat, session.UsageRecord{SessionID: "s", Provider: "main", ProviderType: "openai", Model: "m"})
	if u.Provider != "worker" || u.Input+u.Output != 1_000_050 || u.PriceSnapshot.AmountUSD == nil || *u.PriceSnapshot.AmountUSD != 1.6 {
		t.Fatalf("record=%+v price=%+v", u, u.PriceSnapshot)
	}
	tc.ModelPrices[1].InputCost = 900
	if got := QuoteUsage(tc, u); got.Amount != 1.6 {
		t.Fatalf("historical price changed: %+v", got)
	}
	data, _ := json.Marshal(u)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), stat.ConnectionKey) || strings.Contains(string(data), "/work") {
		t.Fatal("credential, matching digest or URL path persisted")
	}
}

func TestCallUsageOlderInjectedProviderRetainsCapturedDestination(t *testing.T) {
	u := CallUsage(config.TomlConfig{}, llm.CallStat{Model: "helper", TokensIn: 4, TokensOut: 2}, session.UsageRecord{SessionID: "s", Provider: "local", ProviderType: "openai", EndpointHost: "127.0.0.1"})
	if u.SessionID != "s" || u.Provider != "local" || u.Model != "helper" || u.PriceSnapshot.State != "local" {
		t.Fatalf("record=%+v", u)
	}
}

func TestCallUsagePreservesSelectedAliasWithIdenticalConnection(t *testing.T) {
	tc := config.TomlConfig{Providers: []config.ProviderConf{{Name: "first", Type: "openai", BaseURL: "https://gateway.example", APIKey: "key", Model: "m"}, {Name: "selected", Type: "openai", BaseURL: "https://gateway.example", APIKey: "key", Model: "m"}}, ModelPrices: []config.ModelPriceConf{{Provider: "first", Model: "m", InputCost: 90}, {Provider: "selected", Model: "m", InputCost: 2}}}
	u := CallUsage(tc, llm.CallStat{ProviderType: "openai", ConnectionKey: llm.ProviderConnectionKey("openai", "https://gateway.example", "key"), Model: "m", TokensIn: 1_000_000}, session.UsageRecord{Provider: "selected"})
	if u.Provider != "selected" || *u.PriceSnapshot.AmountUSD != 2 {
		t.Fatalf("selected alias lost: %+v", u)
	}
}
