// Package money handles amounts in cents.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

// Cents is an amount in euro cents.
type Cents int64

// Format renders c with two decimals, e.g. "1234.50" or "-0.05".
func Format(c Cents) string {
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}

// Parse reads "12", "12.5" or "12.50" (optionally negative) into cents.
func Parse(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("money: too many decimals in %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: %w", err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: %w", err)
	}
	c := Cents(w*100 + f)
	if neg {
		c = -c
	}
	return c, nil
}
