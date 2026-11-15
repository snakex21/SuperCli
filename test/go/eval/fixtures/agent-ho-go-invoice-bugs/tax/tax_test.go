package tax

import "testing"

func TestRates(t *testing.T) {
	if r, err := RateFor("DE"); err != nil || r != 1900 {
		t.Fatalf("DE = %d, %v", r, err)
	}
	if _, err := RateFor("XX"); err == nil {
		t.Fatal("XX should be unknown")
	}
	if got := Tax(10000, 1900); got != 1900 {
		t.Fatalf("Tax = %d", got)
	}
}
