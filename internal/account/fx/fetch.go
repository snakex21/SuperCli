package fx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const maxRangeDays = 93

type nbpStatusError struct{ code int }

func (e *nbpStatusError) Error() string {
	return fmt.Sprintf("fx: NBP historical request returned HTTP %d", e.code)
}

type dateRange struct {
	start, end time.Time
	days       []string
}

type nbpTable struct {
	Table         string `json:"table"`
	EffectiveDate string `json:"effectiveDate"`
	Rates         []struct {
		Code string  `json:"code"`
		Mid  float64 `json:"mid"`
	} `json:"rates"`
}

func rangesForDays(days []string) []dateRange {
	return rangesForDaysWithLookback(days, lookbackDays)
}

func rangesForDaysWithLookback(days []string, lookback int) []dateRange {
	var ranges []dateRange
	for _, day := range days {
		date, _ := parseDay(day) // normalized by EnsureDays
		if len(ranges) == 0 || date.After(ranges[len(ranges)-1].start.AddDate(0, 0, maxRangeDays-1)) {
			ranges = append(ranges, dateRange{start: date.AddDate(0, 0, -lookback), end: date, days: []string{day}})
		} else {
			ranges[len(ranges)-1].end = date
			ranges[len(ranges)-1].days = append(ranges[len(ranges)-1].days, day)
		}
	}
	return ranges
}

func (c *Cache) fetchDays(ctx context.Context, days []string) (map[string]dayRates, error) {
	result := make(map[string]dayRates, len(days))
	var errs []error
	for _, span := range rangesForDays(days) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := c.fetchRange(ctx, span)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Only a successfully fetched day's own range can establish its latest
		// published table. An overlap from another chunk must not hide an error.
		sort.Slice(batch, func(i, j int) bool { return batch[i].Date < batch[j].Date })
		for _, day := range span.days {
			date, _ := parseDay(day)
			earliest := date.AddDate(0, 0, -lookbackDays).Format(dateLayout)
			index := sort.Search(len(batch), func(i int) bool { return batch[i].Date > day }) - 1
			if index < 0 || batch[index].Date < earliest {
				errs = append(errs, fmt.Errorf("fx: no published table within %d days before %s", lookbackDays, day))
				continue
			}
			result[day] = batch[index]
		}
	}
	return result, errors.Join(errs...)
}

func (c *Cache) fetchRange(ctx context.Context, span dateRange) ([]dayRates, error) {
	raw, err := c.fetchMidRange(ctx, span, "A")
	if err != nil {
		return nil, err
	}
	var result []dayRates
	for _, table := range raw {
		usd := table.mid["USD"]
		if !validMultiplier(usd) {
			return nil, fmt.Errorf("fx: NBP returned an invalid USD rate for %s", table.date)
		}
		converted := map[string]float64{"USD": 1, "PLN": usd}
		for _, currency := range currencyCatalog {
			code := currency.code
			if currency.table != "A" || code == "USD" {
				continue
			}
			mid, present := table.mid[code]
			if !present && !currency.legacy {
				continue // Some newly published currencies have no older history.
			}
			if !validMultiplier(mid) || !validMultiplier(usd/mid) {
				return nil, fmt.Errorf("fx: NBP returned an invalid %s rate for %s", code, table.date)
			}
			converted[code] = usd / mid
		}
		result = append(result, dayRates{Date: table.date, Source: rateSource, Multipliers: converted})
	}
	return result, nil
}

type nbpMidTable struct {
	date string
	mid  map[string]float64
}

func (c *Cache) fetchMidRange(ctx context.Context, span dateRange, tableType string) ([]nbpMidTable, error) {
	if tableType != "A" && tableType != "B" {
		return nil, fmt.Errorf("fx: unsupported NBP table %q", tableType)
	}
	url := "https://api.nbp.pl/api/exchangerates/tables/" + strings.ToLower(tableType) + "/" + span.start.Format(dateLayout) + "/" + span.end.Format(dateLayout) + "/?format=json"
	requestCtx, cancel := context.WithTimeout(ctx, requestBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// NBP rejects the default Go HTTP client identifier. Identify the real
	// application rather than disguising this API request as a browser.
	req.Header.Set("User-Agent", "SuperCli/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fx: NBP request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &nbpStatusError{code: resp.StatusCode}
	}
	// A 93-day table response is small; bound malformed/untrusted responses.
	const maxResponseBytes = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fx: NBP response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("fx: NBP response exceeds %d bytes", maxResponseBytes)
	}
	var raw []nbpTable
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("fx: NBP JSON: %w", err)
	}
	var result []nbpMidTable
	seenDates := make(map[string]struct{}, len(raw))
	for _, table := range raw {
		date, err := parseDay(table.EffectiveDate)
		if err != nil || table.Table != tableType || date.Before(span.start) || date.After(span.end) {
			return nil, fmt.Errorf("fx: NBP returned an invalid/out-of-range table date %q", table.EffectiveDate)
		}
		if _, duplicate := seenDates[table.EffectiveDate]; duplicate {
			return nil, fmt.Errorf("fx: NBP returned duplicate publication date %q", table.EffectiveDate)
		}
		seenDates[table.EffectiveDate] = struct{}{}
		mid := make(map[string]float64, len(table.Rates))
		for _, item := range table.Rates {
			code := strings.ToUpper(item.Code)
			if !validCurrencyCode(code) || !validMultiplier(item.Mid) {
				return nil, fmt.Errorf("fx: NBP returned an invalid %s rate for %s", code, table.EffectiveDate)
			}
			if _, exists := mid[code]; exists {
				return nil, fmt.Errorf("fx: NBP returned duplicate currency %q", code)
			}
			mid[code] = item.Mid
		}
		result = append(result, nbpMidTable{date: table.EffectiveDate, mid: mid})
	}
	return result, nil
}

func validMultiplier(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
