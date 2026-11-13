package cart

import "testing"

func TestSubtotal(t *testing.T) {
	items := []Item{{"a", 250, 2}, {"b", 100, 3}}
	if got := Subtotal(items); got != 800 {
		t.Fatalf("Subtotal = %d, want 800", got)
	}
}

func TestDiscountThreshold(t *testing.T) {
	cases := map[int]int{9999: 0, 10000: 1000, 12345: 1234}
	for in, want := range cases {
		if got := Discount(in); got != want {
			t.Errorf("Discount(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestTotal(t *testing.T) {
	items := []Item{{"a", 5000, 1}, {"b", 2500, 2}}
	if got := Total(items); got != 9000 {
		t.Fatalf("Total = %d, want 9000", got)
	}
}
