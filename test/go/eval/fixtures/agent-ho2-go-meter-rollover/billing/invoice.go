// Package billing builds monthly invoices.
package billing

import (
	"fmt"
	"io"
	"sort"

	"meterd/ingest"
	"meterd/tariff"
	"meterd/usage"
)

// Invoice is one customer's monthly bill.
type Invoice struct {
	Customer string
	KWh      int64
	Amount   int64 // grosze
}

// Build reads the meter list and readings exports and prices every
// customer's consumption with t. Invoices are sorted by customer.
func Build(metersCSV, readingsCSV io.Reader, t tariff.Tariff) ([]Invoice, error) {
	ms, err := ingest.Meters(metersCSV)
	if err != nil {
		return nil, fmt.Errorf("billing: meters: %w", err)
	}
	rs, err := ingest.Readings(readingsCSV)
	if err != nil {
		return nil, fmt.Errorf("billing: readings: %w", err)
	}
	perCustomer, err := usage.PerCustomer(ms, rs)
	if err != nil {
		return nil, fmt.Errorf("billing: %w", err)
	}
	out := make([]Invoice, 0, len(perCustomer))
	for c, kwh := range perCustomer {
		// TODO(finance): amounts are whole grosze; revisit rounding if we
		// ever bill fractional kWh.
		out = append(out, Invoice{Customer: c, KWh: kwh, Amount: t.Price(kwh)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Customer < out[j].Customer })
	return out, nil
}

// Total sums the amounts of invs.
func Total(invs []Invoice) (kwh, amount int64) {
	for _, inv := range invs {
		kwh += inv.KWh
		amount += inv.Amount
	}
	return kwh, amount
}
