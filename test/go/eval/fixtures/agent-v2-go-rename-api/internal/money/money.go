// Package money handles amounts in minor units (cents).
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Amount is a value in minor units (cents).
type Amount int64

// ErrEmpty is returned for blank input. The message is part of the
// shopctl CLI contract (scripts match on it), keep it byte-identical.
var ErrEmpty = errors.New("money.ParseAmount: empty input")

// ParseAmount parses "12.34", "12", "-0.5" or "1,234.50" into cents.
func ParseAmount(s string) (Amount, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, ErrEmpty
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("money.ParseAmount: too many decimals in %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	var w int64
	if whole != "" {
		v, err := strconv.ParseInt(whole, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("money.ParseAmount: bad number %q", s)
		}
		w = v
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money.ParseAmount: bad number %q", s)
	}
	a := Amount(w*100 + f)
	if neg {
		a = -a
	}
	return a, nil
}

// ParseAmountStrict is like ParseAmount but rejects thousands
// separators and requires exactly two decimals.
func ParseAmountStrict(s string) (Amount, error) {
	if strings.Contains(s, ",") {
		return 0, fmt.Errorf("money.ParseAmountStrict: separator in %q", s)
	}
	if i := strings.IndexByte(s, '.'); i < 0 || len(s)-i-1 != 2 {
		return 0, fmt.Errorf("money.ParseAmountStrict: need two decimals in %q", s)
	}
	return ParseAmount(s)
}

// String renders the amount as "-12.34".
func (a Amount) String() string {
	sign := ""
	if a < 0 {
		sign, a = "-", -a
	}
	return fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
}
