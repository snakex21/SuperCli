// Package tax knows VAT rates (in basis points) and applies them.
package tax

import (
	"fmt"
	"strings"

	"invoicer/money"
)

var rates = map[string]int{"AT": 2000, "DE": 1900, "FR": 2000, "NL": 2100, "PL": 2300}

// RateFor returns the VAT rate in basis points for an ISO country code.
func RateFor(country string) (int, error) {
	r, ok := rates[strings.ToUpper(strings.TrimSpace(country))]
	if !ok {
		return 0, fmt.Errorf("tax: unknown country %q", country)
	}
	return r, nil
}

// Tax returns net * bp / 10000 rounded half up to the cent.
func Tax(net money.Cents, bp int) money.Cents {
	return (net*money.Cents(bp) + 5000) / 10000
}
