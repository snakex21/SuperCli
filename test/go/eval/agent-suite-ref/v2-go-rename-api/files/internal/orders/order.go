// Package orders computes order totals.
package orders

import "shopcore/internal/money"

// Line is an order line as submitted by the storefront.
type Line struct {
	SKU      string
	Qty      int
	UnitText string
}

// Order groups lines.
type Order struct {
	ID    string
	Lines []Line
	Note  string
}

// UnitPrice parses the line's unit price.
func (l Line) UnitPrice() (money.Amount, error) {
	return money.Parse(l.UnitText)
}
