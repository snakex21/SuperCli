package usagecost

import (
	"reflect"
	"testing"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type savedCurrencyRates struct {
	t              *testing.T
	values         map[string]fx.Rate
	wantedCurrency string
	lookups        []string
}

func TestCurrencyAccumulatorRetainsIndependentWeeklyAndUSDDates(t *testing.T) {
	rates := &savedCurrencyRates{t: t, wantedCurrency: "AED", values: map[string]fx.Rate{
		"2026-10-09": {Multiplier: 2, Date: "2026-10-07", USDDate: "2026-10-09", Source: "NBP table B / USD table A"},
	}}
	c := NewCurrencyAccumulator(config.TomlConfig{CostCurrency: "AED"}, rates)
	c.Add(currencyPricedRecord("2026-10-09", 3, false))
	if summary := c.Summary(); summary.Amount == nil || *summary.Amount != 6 || summary.BaseAmountUSD == nil || *summary.BaseAmountUSD != 3 || summary.Currency != "AED" {
		t.Fatalf("frozen weekly conversion=%+v", summary)
	}
	if !reflect.DeepEqual(c.RateDates(), []string{"2026-10-07"}) || !reflect.DeepEqual(c.USDRateDates(), []string{"2026-10-09"}) || !reflect.DeepEqual(c.RateSources(), []string{"NBP table B / USD table A"}) {
		t.Fatalf("publication provenance target=%v USD=%v source=%v", c.RateDates(), c.USDRateDates(), c.RateSources())
	}
	dates := c.USDRateDates()
	dates[0] = "tampered"
	if c.USDRateDates()[0] != "2026-10-09" {
		t.Fatal("USD dates expose mutable state")
	}
}

func (r *savedCurrencyRates) Lookup(day, currency string) (fx.Rate, bool) {
	if currency != r.wantedCurrency {
		r.t.Errorf("currency=%s want=%s", currency, r.wantedCurrency)
	}
	r.lookups = append(r.lookups, day)
	rate, ok := r.values[day]
	return rate, ok
}

func currencyPricedRecord(day string, amount float64, legacy bool) session.UsageRecord {
	return session.UsageRecord{Model: "m", Input: 1000000, CreatedAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		PriceSnapshot: &session.PriceSnapshot{State: "manual", AmountUSD: &amount, Source: "manual", InputPerMillion: amount,
			UsageDay: day, Legacy: legacy, CacheKnown: true}}
}

func TestCurrencyAccumulatorUsesFrozenPerCallDaysAmountsAndPublicationDates(t *testing.T) {
	tc := config.TomlConfig{CostCurrency: "PLN", ModelPrices: []config.ModelPriceConf{{Model: "m", InputCost: 900}}}
	rates := &savedCurrencyRates{t: t, wantedCurrency: "PLN", values: map[string]fx.Rate{
		"2026-10-08": {Multiplier: 4, Date: "2026-10-08"},
		"2026-10-09": {Multiplier: 5, Date: "2026-10-09"},
		"2026-10-10": {Multiplier: 5, Date: "2026-10-09"},
	}}
	c := NewCurrencyAccumulator(tc, rates)
	c.Add(currencyPricedRecord("2026-10-09", 2, false))
	c.Add(currencyPricedRecord("2026-10-08", 1, true))
	c.Add(currencyPricedRecord("2026-10-10", 3, false))
	got := c.Summary()
	if got.Amount == nil || *got.Amount != 29 || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 6 || got.Currency != "PLN" || got.PricingCurrency != "USD" || got.Partial || c.LegacyCalls() != 1 || c.MissingFXCalls() != 0 {
		t.Fatalf("converted summary=%+v legacy=%d missing=%d", got, c.LegacyCalls(), c.MissingFXCalls())
	}
	if !reflect.DeepEqual(rates.lookups, []string{"2026-10-09", "2026-10-08", "2026-10-10"}) {
		t.Fatalf("lookups=%v", rates.lookups)
	}
	dates := c.RateDates()
	if !reflect.DeepEqual(dates, []string{"2026-10-08", "2026-10-09"}) {
		t.Fatalf("publication dates=%v", dates)
	}
	dates[0] = "tampered"
	if c.RateDates()[0] != "2026-10-08" {
		t.Fatal("publication date getter exposes mutable state")
	}
	*got.Amount = 999
	if again := c.Summary(); *again.Amount != 29 || *again.BaseAmountUSD != 6 {
		t.Fatal("editing presentation amount changed retained USD prices")
	}
}

func TestCurrencyAccumulatorPreservesUSDWithoutRateLookupOrPriceChanges(t *testing.T) {
	rates := &savedCurrencyRates{t: t, wantedCurrency: "should not be queried"}
	c := NewCurrencyAccumulator(config.TomlConfig{ModelPrices: []config.ModelPriceConf{{Model: "m", InputCost: 999}}}, rates)
	c.Add(currencyPricedRecord("2026-10-08", 1.25, false))
	got := c.Summary()
	if got.Amount == nil || *got.Amount != 1.25 || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 1.25 || got.InputPerMillion == nil || *got.InputPerMillion != 1.25 || got.Currency != "USD" || len(rates.lookups) != 0 || c.RateDates() == nil {
		t.Fatalf("USD snapshot=%+v lookups=%v dates=%v", got, rates.lookups, c.RateDates())
	}
}

func TestCurrencyAccumulatorLegacyAggregateHasNoInventedUsageDay(t *testing.T) {
	u := currencyPricedRecord("2026-10-08", 1.25, true)
	u.Source = "legacy"
	rates := &savedCurrencyRates{t: t, wantedCurrency: "PLN", values: map[string]fx.Rate{"2026-10-08": {Multiplier: 4, Date: "2026-10-08"}}}
	c := NewCurrencyAccumulator(config.TomlConfig{CostCurrency: "PLN"}, rates)
	c.Add(u)
	got := c.Summary()
	if got.Amount != nil || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 1.25 || !got.Partial || got.Source != "fx_missing" || got.MissingFXCalls != 1 || len(rates.lookups) != 0 {
		t.Fatalf("session timestamp invented a daily rate: %+v lookups=%v", got, rates.lookups)
	}
	usd := NewCurrencyAccumulator(config.TomlConfig{}, rates)
	usd.Add(u)
	if got := usd.Summary(); got.Amount == nil || *got.Amount != 1.25 || got.Partial {
		t.Fatalf("legacy USD price changed: %+v", got)
	}
}

func TestCurrencyAccumulatorMissingRatesKeepUnknownAndKnownPartialTotals(t *testing.T) {
	tc := config.TomlConfig{CostCurrency: "EUR"}
	rates := &savedCurrencyRates{t: t, wantedCurrency: "EUR", values: map[string]fx.Rate{
		"2026-10-08": {Multiplier: .9, Date: "2026-10-08"},
	}}
	c := NewCurrencyAccumulator(tc, rates)
	c.Add(currencyPricedRecord("2026-10-09", 2, false))
	got := c.Summary()
	if got.Amount != nil || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 2 || !got.Partial || got.Source != "fx_missing" || got.MissingFXCalls != 1 {
		t.Fatalf("missing rate fabricated a total: %+v", got)
	}
	c.Add(currencyPricedRecord("2026-10-08", 10, true))
	got = c.Summary()
	if got.Amount == nil || *got.Amount != 9 || got.BaseAmountUSD == nil || *got.BaseAmountUSD != 12 || !got.Partial || got.MissingFXCalls != 1 || c.LegacyCalls() != 1 {
		t.Fatalf("partial conversion=%+v", got)
	}
}

func TestCurrencyAccumulatorIncludedUnknownAndZeroCallsNeedNoRates(t *testing.T) {
	for _, state := range []string{"free", "local", "subscription", "unknown", "manual"} {
		t.Run(state, func(t *testing.T) {
			rates := &savedCurrencyRates{t: t, wantedCurrency: "should not be queried"}
			amount := 0.0
			u := session.UsageRecord{PriceSnapshot: &session.PriceSnapshot{State: state, UsageDay: "2026-10-08"}}
			if state == "manual" || state == "free" {
				u.PriceSnapshot.AmountUSD = &amount
			}
			c := NewCurrencyAccumulator(config.TomlConfig{CostCurrency: "GBP"}, rates)
			c.Add(u)
			got := c.Summary()
			if got.State != state || got.Currency != "GBP" || got.MissingFXCalls != 0 || got.Partial || len(rates.lookups) != 0 {
				t.Fatalf("state=%s summary=%+v lookups=%v", state, got, rates.lookups)
			}
			if state == "manual" || state == "free" {
				if got.Amount == nil || *got.Amount != 0 {
					t.Fatal("known zero became missing FX")
				}
			} else if got.Amount != nil {
				t.Fatal("included/unknown classification became numeric zero")
			}
		})
	}
}

func TestCurrencyAccumulatorLegacyUsesCapturedCallTimeAndUSDRates(t *testing.T) {
	created := time.Date(2020, 2, 3, 22, 30, 0, 0, time.UTC)
	day := created.In(time.Local).Format("2006-01-02")
	rates := &savedCurrencyRates{t: t, wantedCurrency: "CHF", values: map[string]fx.Rate{day: {Multiplier: .8, Date: day}}}
	c := NewCurrencyAccumulator(config.TomlConfig{CostCurrency: "CHF", ModelPrices: []config.ModelPriceConf{{Model: "legacy", InputCost: 2}}}, rates)
	c.Add(session.UsageRecord{Model: "legacy", Input: 1000000, CreatedAt: created})
	got := c.Summary()
	if c.LegacyCalls() != 1 || got.Amount == nil || *got.Amount != 1.6 || got.InputPerMillion == nil || *got.InputPerMillion != 2 || got.PricingCurrency != "USD" || !reflect.DeepEqual(rates.lookups, []string{day}) {
		t.Fatalf("legacy=%+v lookups=%v", got, rates.lookups)
	}
}
