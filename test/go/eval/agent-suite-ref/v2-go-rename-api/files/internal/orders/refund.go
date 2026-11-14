package orders

import (
	"fmt"

	"shopcore/internal/money"
)

// Refund validates a refund request against the paid amount.
func Refund(paid money.Amount, requested string) (money.Amount, error) {
	r, err := money.ParseAmountStrict(requested)
	if err != nil {
		return 0, err
	}
	if r > paid {
		return 0, fmt.Errorf("refund %s exceeds paid %s", r, paid)
	}
	return r, nil
}

// PartialRefund accepts loose input (e.g. from support tooling).
func PartialRefund(paid money.Amount, requested string) (money.Amount, error) {
	r, err := money.Parse(requested)
	if err != nil {
		return 0, err
	}
	if r > paid {
		r = paid
	}
	return r, nil
}
