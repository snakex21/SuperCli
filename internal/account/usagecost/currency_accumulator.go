package usagecost

import (
	"sort"
	"time"

	"supercli/internal/account/fx"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// CurrencyRateLookup exposes only a saved in-memory rate lookup. Accumulating
// costs cannot fetch, retry or warm exchange rates.
type CurrencyRateLookup interface {
	Lookup(day, currency string) (fx.Rate, bool)
}

// CurrencyAccumulator converts each call with its usage day's saved rate.
// Model price snapshots and per-million prices remain in USD.
type CurrencyAccumulator struct {
	usd                                Accumulator
	tc                                 config.TomlConfig
	currency                           string
	cache                              CurrencyRateLookup
	amount                             float64
	priced, converted, missing, legacy int
	dates                              map[string]bool
	usdDates, sources                  map[string]bool
}

func NewCurrencyAccumulator(tc config.TomlConfig, cache CurrencyRateLookup) *CurrencyAccumulator {
	return &CurrencyAccumulator{usd: NewAccumulator(tc), tc: tc, currency: config.EffectiveCostCurrency(tc), cache: cache, dates: make(map[string]bool), usdDates: make(map[string]bool), sources: make(map[string]bool)}
}

// UsageDay prefers the immutable captured local date, including after moving
// the portable application into another time zone.
func UsageDay(u session.UsageRecord) string {
	if u.PriceSnapshot != nil && u.PriceSnapshot.UsageDay != "" {
		return u.PriceSnapshot.UsageDay
	}
	return u.CreatedAt.In(time.Local).Format("2006-01-02")
}

func (c *CurrencyAccumulator) Add(u session.UsageRecord) {
	c.usd.Add(u)
	if u.PriceSnapshot == nil || u.PriceSnapshot.Legacy {
		c.legacy++
	}
	if c.currency == "USD" {
		return
	}
	quote := QuoteUsage(c.tc, u)
	if quote.State != "manual" && quote.State != "estimated" {
		return
	}
	c.priced++
	if quote.Amount == 0 {
		c.converted++
		return
	}
	// A pre-ledger aggregate can span many days. Its session timestamp is
	// not a usage day, so one day's rate would manufacture an FX amount.
	if u.Source == "legacy" {
		c.missing++
		return
	}
	if c.cache != nil {
		if rate, ok := c.cache.Lookup(UsageDay(u), c.currency); ok {
			c.amount += quote.Amount * rate.Multiplier
			c.converted++
			c.dates[rate.Date] = true
			usdDate := rate.USDDate
			if usdDate == "" {
				usdDate = rate.Date
			}
			c.usdDates[usdDate] = true
			if rate.Source != "" {
				c.sources[rate.Source] = true
			}
			return
		}
	}
	c.missing++
}

func (c *CurrencyAccumulator) Summary() Summary {
	summary := c.usd.Summary()
	summary.BaseAmountUSD = summary.Amount
	summary.PricingCurrency = "USD"
	summary.Currency = c.currency
	summary.MissingFXCalls = c.missing
	if c.priced > 0 {
		if c.converted > 0 {
			amount := c.amount
			summary.Amount = &amount
		} else {
			summary.Amount = nil
		}
		if c.missing > 0 {
			summary.Partial = true
			if c.converted == 0 {
				summary.Source = "fx_missing"
			}
		}
	}
	return summary
}

func (c *CurrencyAccumulator) MissingFXCalls() int { return c.missing }
func (c *CurrencyAccumulator) LegacyCalls() int    { return c.legacy }

// RateDates returns an independent, sorted set of publication dates.
func (c *CurrencyAccumulator) RateDates() []string {
	return sortedRateValues(c.dates)
}

// USDRateDates identifies the independently frozen USD reference of a cross
// rate. A weekly B publication need not share its date with the daily A rate.
func (c *CurrencyAccumulator) USDRateDates() []string { return sortedRateValues(c.usdDates) }
func (c *CurrencyAccumulator) RateSources() []string  { return sortedRateValues(c.sources) }

func sortedRateValues(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for date := range values {
		result = append(result, date)
	}
	sort.Strings(result)
	return result
}
