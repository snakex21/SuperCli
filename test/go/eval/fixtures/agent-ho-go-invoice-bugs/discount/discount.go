// Package discount implements quantity tiers.
package discount

import "invoicer/money"

// PercentFor returns the tier discount for a total quantity:
// 10 or more units 5%, 50 or more 10%, 100 or more 15%.
func PercentFor(qty int) int {
	switch {
	case qty > 100:
		return 15
	case qty >= 50:
		return 10
	case qty >= 10:
		return 5
	}
	return 0
}

// Amount returns pct percent of c, rounded half up to the cent.
func Amount(c money.Cents, pct int) money.Cents {
	return (c*money.Cents(pct) + 50) / 100
}
