// Package invoice models invoices and computes their totals.
package invoice

import (
	"time"

	"invoicer/discount"
	"invoicer/money"
	"invoicer/tax"
)

// Line is one invoice line.
type Line struct {
	No   int
	SKU  string
	Desc string
	Qty  int
	Unit money.Cents
}

// Invoice is a customer invoice.
type Invoice struct {
	Number  string
	Country string
	Issued  time.Time
	NetDays int
	Lines   []Line
}

// Totals returns the net amount (after the quantity discount), the VAT
// and the gross amount of inv.
func Totals(inv Invoice) (net, vat, gross money.Cents, err error) {
	var sub money.Cents
	qty := 0
	for _, l := range inv.Lines {
		sub += money.Cents(l.Qty) * l.Unit
		qty += l.Qty
	}
	rate, err := tax.RateFor(inv.Country)
	if err != nil {
		return 0, 0, 0, err
	}
	net = sub - discount.Amount(sub, discount.PercentFor(qty))
	vat = tax.Tax(net, rate)
	return net, vat, net + vat, nil
}
