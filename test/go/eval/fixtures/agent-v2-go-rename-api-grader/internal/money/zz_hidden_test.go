package money

import "testing"

func TestHiddenRenamedAPI(t *testing.T) {
	if a, err := Parse("1,234.5"); err != nil || a != 123450 {
		t.Fatalf("Parse = %v, %v", a, err)
	}
	if _, err := Parse(""); err == nil || err.Error() != "money.ParseAmount: empty input" {
		t.Fatalf("empty error text changed: %v", err)
	}
	if _, err := Parse("1.234"); err == nil || err.Error() != `money.ParseAmount: too many decimals in "1.234"` {
		t.Fatalf("decimals error text changed: %v", err)
	}
	if _, err := ParseAmountStrict("1,0.00"); err == nil || err.Error() != `money.ParseAmountStrict: separator in "1,0.00"` {
		t.Fatalf("strict error text changed: %v", err)
	}
	if a, err := ParseAmountStrict("7.07"); err != nil || a != 707 {
		t.Fatalf("ParseAmountStrict = %v, %v", a, err)
	}
}
