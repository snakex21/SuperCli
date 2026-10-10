package usagecost

import (
	"reflect"
	"testing"
	"time"

	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestFrozenQuotePreservesAmountAndRateAfterPriceChange(t *testing.T) {
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Provider: "fixture", Model: "priced", InputCost: 2, CachedInputCost: 1, OutputCost: 4}}}
	u := session.UsageRecord{Provider: "fixture", Model: "priced", Input: 1000000, Output: 1000000, CachedInput: 500000,
		CreatedAt: time.Date(2020, 2, 3, 23, 30, 0, 0, time.FixedZone("east", 2*60*60))}
	want := QuoteUsage(tc, u)
	frozen := FreezeQuote(tc, u, false)
	if frozen.AmountUSD == nil || *frozen.AmountUSD != 5.5 || frozen.InputPerMillion != 2 || frozen.CachedInputPerMillion != 1 || frozen.OutputPerMillion != 4 || !frozen.CacheKnown || frozen.Legacy || frozen.UsageDay != u.CreatedAt.In(time.Local).Format("2006-01-02") || frozen.PriceDate == "" {
		t.Fatalf("snapshot=%+v", frozen)
	}
	u.PriceSnapshot = &frozen
	tc.ModelPrices[0].InputCost, tc.ModelPrices[0].OutputCost = 200, 400
	if got := QuoteUsage(tc, u); got != want {
		t.Fatalf("price change repriced frozen usage: got=%+v want=%+v", got, want)
	}
	clone := FreezeQuote(tc, u, true)
	if !reflect.DeepEqual(clone, frozen) || clone.AmountUSD == frozen.AmountUSD {
		t.Fatalf("freezing again changed snapshot or shares writable amount: %+v", clone)
	}
	*clone.AmountUSD = 99
	if *frozen.AmountUSD != 5.5 {
		t.Fatal("returned snapshot aliases original amount")
	}
	if got := Resolve(tc, []session.UsageRecord{u}, session.UsageRecord{}); got.Amount == nil || *got.Amount != 5.5 || got.State != "manual" {
		t.Fatalf("accumulator ignored frozen quote: %+v", got)
	}
}

func TestFrozenClassificationsDoNotBecomeCharges(t *testing.T) {
	for _, tt := range []struct {
		name, state string
		u           session.UsageRecord
	}{
		{"unknown", "unknown", session.UsageRecord{Provider: "fixture", Model: "unknown", EndpointHost: "gateway.invalid"}},
		{"local", "local", session.UsageRecord{Provider: "fixture", Model: "local", EndpointHost: "127.0.0.1"}},
		{"subscription", "subscription", session.UsageRecord{Provider: "fixture", ProviderType: config.ProviderCodex, Model: "subscription"}},
		{"free", "free", session.UsageRecord{Provider: "fixture", Model: "vendor/free-model:free"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.u
			u.Input, u.Output = 1000, 1000
			frozen := FreezeQuote(config.TomlConfig{}, u, true)
			if frozen.State != tt.state || !frozen.Legacy || (tt.state != "free" && frozen.AmountUSD != nil) {
				t.Fatalf("snapshot=%+v", frozen)
			}
			u.PriceSnapshot = &frozen
			tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Provider: u.Provider, Model: u.Model, InputCost: 200, OutputCost: 400}}}
			if got := QuoteUsage(tc, u); got.State != tt.state || got.Amount != 0 || got.RateKnown {
				t.Fatalf("frozen classification became a charge: %+v", got)
			}
		})
	}
}

func TestFreezeQuoteClampsProviderTokenSubsets(t *testing.T) {
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Model: "clamp", InputCost: 2, CachedInputCost: 1, OutputCost: 4}}}
	u := session.UsageRecord{Model: "clamp", Input: 1000000, Output: -100, CachedInput: 2000000}
	frozen := FreezeQuote(tc, u, false)
	if frozen.AmountUSD == nil || *frozen.AmountUSD != 1 || !frozen.CacheKnown {
		t.Fatalf("overlarge cached/negative output was priced: %+v", frozen)
	}
	u.Input, u.CachedInput = -100, -50
	frozen = FreezeQuote(tc, u, false)
	if frozen.AmountUSD == nil || *frozen.AmountUSD != 0 {
		t.Fatalf("negative token counts were priced: %+v", frozen)
	}
}

func TestMalformedFrozenQuoteDoesNotFallBackToCurrentCatalog(t *testing.T) {
	tc := config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Model: "priced", InputCost: 2, OutputCost: 4}}}
	u := session.UsageRecord{Model: "priced", Input: 1000000, PriceSnapshot: &session.PriceSnapshot{State: "manual"}}
	if got := QuoteUsage(tc, u); got.State != "unknown" || got.RateKnown {
		t.Fatalf("invalid frozen amount repriced from current configuration: %+v", got)
	}
}
