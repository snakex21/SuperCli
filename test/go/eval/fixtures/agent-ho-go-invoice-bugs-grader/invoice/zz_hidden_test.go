package invoice

import "testing"

func TestHiddenDiscountBeforeVAT(t *testing.T) {
	inv := Invoice{Country: "DE", Lines: []Line{{No: 1, Qty: 60, Unit: 1000}, {No: 2, Qty: 60, Unit: 1000}}}
	net, vat, gross, err := Totals(inv)
	if err != nil || net != 102000 || vat != 19380 || gross != 121380 {
		t.Fatalf("Totals = %d %d %d %v, want 102000 19380 121380", net, vat, gross, err)
	}
	inv = Invoice{Country: "PL", Lines: []Line{{No: 1, Qty: 12, Unit: 333}}}
	net, vat, gross, err = Totals(inv)
	if err != nil || net != 3796 || vat != 873 || gross != 4669 {
		t.Fatalf("Totals = %d %d %d %v, want 3796 873 4669", net, vat, gross, err)
	}
}
