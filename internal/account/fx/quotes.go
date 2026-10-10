package fx

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// EnsureCurrency adds only missing currency/day quotes. Existing day snapshots
// and quotes are immutable. Range requests retrieve whole official tables, so
// switching to another currency from the same table needs no per-code GET.
func (c *Cache) EnsureCurrency(ctx context.Context, days []string, currency string) error {
	code, err := NormalizeCurrency(currency)
	if err != nil {
		return err
	}
	wanted, err := normalizeDays(days)
	if err != nil || len(wanted) == 0 {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if code == "USD" {
		return nil
	}
	if err := c.EnsureDays(ctx, wanted); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return fmt.Errorf("fx: cache is closed")
		}
		if err := c.refreshPersisted(ctx); err != nil {
			c.mu.Unlock()
			return err
		}
		missing := c.missingCurrencyDays(wanted, code)
		if len(missing) == 0 {
			c.mu.Unlock()
			return nil
		}
		flight := c.flight
		if flight == nil {
			fetchCtx, cancel := context.WithTimeout(context.Background(), fetchBudget)
			flight = &cacheFlight{days: missing, currency: code, done: make(chan struct{}), cancel: cancel}
			c.flight = flight
			go c.runQuoteFlight(fetchCtx, flight)
		}
		flight.waiters++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.leaveFlight(flight)
			return ctx.Err()
		case <-flight.done:
			c.leaveFlight(flight)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		c.mu.Lock()
		remaining, flightErr := c.missingCurrencyDays(wanted, code), flight.err
		c.mu.Unlock()
		if len(remaining) == 0 {
			return nil
		}
		if flightErr != nil && flight.currency == code && daysOverlap(remaining, flight.days) {
			return flightErr
		}
	}
}

func (c *Cache) missingCurrencyDays(days []string, code string) []string {
	var missing []string
	for _, day := range days {
		if _, ok := c.Lookup(day, code); !ok {
			missing = append(missing, day)
		}
	}
	return missing
}

func (c *Cache) runQuoteFlight(ctx context.Context, flight *cacheFlight) {
	defer flight.cancel()
	fetched, fetchErr := c.fetchQuotes(ctx, flight.days, flight.currency)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.flight != flight || ctx.Err() != nil {
		flight.err = ctx.Err()
		if flight.err == nil {
			flight.err = context.Canceled
		}
	} else {
		flight.err = c.commitQuotes(ctx, fetched)
		if flight.err == nil {
			flight.err = fetchErr
		}
	}
	if c.flight == flight {
		c.flight = nil
	}
	close(flight.done)
}

func (c *Cache) fetchQuotes(ctx context.Context, days []string, code string) (map[string]map[string]Rate, error) {
	anchors := make(map[string]dayRates, len(days))
	c.lookupMu.RLock()
	for _, day := range days {
		anchors[day] = c.days[day]
	}
	c.lookupMu.RUnlock()
	result := make(map[string]map[string]Rate)
	preferred := currencyTable(code)
	if preferred != "A" && preferred != "B" {
		return nil, fmt.Errorf("fx: no official table for %s", code)
	}
	if err := c.fetchQuoteTable(ctx, days, preferred, anchors, result); err != nil {
		var status *nbpStatusError
		if !errors.As(err, &status) || status.code != 404 {
			return result, err // A server failure is not a reason to retry another table.
		}
	}
	var missing []string
	for _, day := range days {
		if !validMultiplier(result[day][code].Multiplier) {
			missing = append(missing, day)
		}
	}
	// A currency can move between official A and B tables over its history.
	// Fetch the other table only for a still-missing requested currency.
	if len(missing) != 0 {
		other := "A"
		if preferred == "A" {
			other = "B"
		}
		if err := c.fetchQuoteTable(ctx, missing, other, anchors, result); err != nil {
			return result, err
		}
	}
	var errs []error
	for _, day := range days {
		if !validMultiplier(result[day][code].Multiplier) {
			errs = append(errs, fmt.Errorf("fx: no published %s quote for %s", code, day))
		}
	}
	return result, errors.Join(errs...)
}

func (c *Cache) fetchQuoteTable(ctx context.Context, days []string, table string, anchors map[string]dayRates, result map[string]map[string]Rate) error {
	anchorDays := make([]string, 0, len(days))
	for _, day := range days {
		requestDay := anchors[day].Date
		if table == "B" {
			requestDay = day
		}
		anchorDays = append(anchorDays, requestDay)
	}
	anchorDays, err := normalizeDays(anchorDays)
	if err != nil {
		return err
	}
	lookback := lookbackDays
	if table == "B" {
		lookback = bLookbackDays
	}
	for _, span := range rangesForDaysWithLookback(anchorDays, lookback) {
		batch, err := c.fetchMidRange(ctx, span, table)
		if err != nil {
			return err
		}
		sort.Slice(batch, func(i, j int) bool { return batch[i].date < batch[j].date })
		for _, day := range days {
			anchor := anchors[day]
			requestDay := anchor.Date
			if table == "B" {
				requestDay = day
			}
			owned := sort.SearchStrings(span.days, requestDay)
			if owned == len(span.days) || span.days[owned] != requestDay {
				continue
			}
			index := sort.Search(len(batch), func(i int) bool { return batch[i].date > requestDay }) - 1
			if index < 0 {
				continue
			}
			published := batch[index]
			requestDate, _ := parseDay(requestDay)
			if table == "A" && published.date != anchor.Date || published.date < requestDate.AddDate(0, 0, -lookback).Format(dateLayout) {
				continue
			}
			if result[day] == nil {
				result[day] = make(map[string]Rate)
			}
			for currency, mid := range published.mid {
				if currency == "USD" || currency == "PLN" {
					continue
				}
				if _, err := NormalizeCurrency(currency); err != nil {
					continue
				}
				if _, exists := c.Lookup(day, currency); exists {
					continue
				}
				if _, exists := result[day][currency]; exists {
					continue
				}
				multiplier := anchor.Multipliers["PLN"] / mid
				if !validMultiplier(multiplier) {
					return fmt.Errorf("fx: invalid cross rate for %s", currency)
				}
				source := rateSource
				if table == "B" {
					source = quoteBSource
				}
				result[day][currency] = Rate{Multiplier: multiplier, Date: published.date, USDDate: anchor.Date, Source: source}
			}
		}
	}
	return ctx.Err()
}

func validCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, ch := range code {
		if ch < 'A' || ch > 'Z' {
			return false
		}
	}
	return true
}

// ValidateQuote also serves backup validation. It checks a frozen cross rate
// against its USD table date, without network calls or changing historical data.
func ValidateQuote(day, currency, publicationDay, usdDay, source string, multiplier float64) error {
	usage, err := parseDay(day)
	if err != nil {
		return err
	}
	usd, err := parseDay(usdDay)
	if err != nil || usd.After(usage) || usd.Before(usage.AddDate(0, 0, -lookbackDays)) {
		return fmt.Errorf("fx: invalid frozen USD publication for %s", day)
	}
	published, err := parseDay(publicationDay)
	if err != nil || !validCurrencyCode(currency) || currency == "USD" || currency == "PLN" || !validMultiplier(multiplier) {
		return fmt.Errorf("fx: invalid %s quote for %s", currency, day)
	}
	switch source {
	case rateSource:
		if !published.Equal(usd) {
			return fmt.Errorf("fx: table A quote differs from its frozen USD date")
		}
	case quoteBSource:
		if published.After(usage) || published.Before(usage.AddDate(0, 0, -bLookbackDays)) {
			return fmt.Errorf("fx: invalid weekly table B publication for %s", day)
		}
	default:
		return fmt.Errorf("fx: invalid quote source %q", source)
	}
	return nil
}
