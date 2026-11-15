package money

import "testing"

func TestHiddenNegative(t *testing.T) {
	for c, want := range map[Cents]string{-5: "-0.05", -105: "-1.05", -123450: "-1234.50", 0: "0.00", 7: "0.07", 100: "1.00"} {
		if got := Format(c); got != want {
			t.Errorf("Format(%d) = %q, want %q", c, got, want)
		}
	}
}
