// Package reports renders sales reports.
package reports

import (
	"fmt"
	"strings"

	"shopcore/internal/money"
)

// DailyLine is "sku;amount".
func SumDaily(lines []string) (money.Amount, error) {
	var sum money.Amount
	for i, l := range lines {
		_, amt, ok := strings.Cut(l, ";")
		if !ok {
			return 0, fmt.Errorf("line %d: no amount", i+1)
		}
		a, err := money.ParseAmount(amt)
		if err != nil {
			return 0, fmt.Errorf("line %d: %w", i+1, err)
		}
		sum += a
	}
	return sum, nil
}
