package orders

import "testing"

func TestTotal(t *testing.T) {
	o := Order{Lines: []Line{{Qty: 2, UnitText: "1.25"}, {Qty: 1, UnitText: "10"}}}
	if got, err := Total(o); err != nil || got != 1250 {
		t.Fatalf("Total = %v, %v", got, err)
	}
	if got, _ := ApplyCoupon(1000, "2.50"); got != 750 {
		t.Fatalf("ApplyCoupon = %v", got)
	}
	if _, err := Refund(100, "2.00"); err == nil {
		t.Fatal("refund above paid accepted")
	}
	if got, _ := PartialRefund(100, "2"); got != 100 {
		t.Fatalf("PartialRefund = %v", got)
	}
}
