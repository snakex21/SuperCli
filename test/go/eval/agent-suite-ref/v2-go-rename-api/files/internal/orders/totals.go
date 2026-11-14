package orders

import "shopcore/internal/money"

// Total sums qty*unit over all lines.
func Total(o Order) (money.Amount, error) {
	var sum money.Amount
	for _, l := range o.Lines {
		u, err := l.UnitPrice()
		if err != nil {
			return 0, err
		}
		sum += u * money.Amount(l.Qty)
	}
	return sum, nil
}

// ApplyCoupon subtracts a flat coupon value given as text.
func ApplyCoupon(total money.Amount, coupon string) (money.Amount, error) {
	c, err := money.Parse(coupon)
	if err != nil {
		return 0, err
	}
	if c > total {
		return 0, nil
	}
	return total - c, nil
}
