package invoice

import "testing"

func TestTotalsNoDiscount(t *testing.T) {
	inv := Invoice{Country: "DE", Lines: []Line{{No: 1, Qty: 2, Unit: 1000}}}
	net, vat, gross, err := Totals(inv)
	if err != nil || net != 2000 || vat != 380 || gross != 2380 {
		t.Fatalf("Totals = %d %d %d %v", net, vat, gross, err)
	}
}
