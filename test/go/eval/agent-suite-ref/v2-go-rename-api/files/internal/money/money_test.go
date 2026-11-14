package money

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Amount{"12.34": 1234, "12": 1200, "-0.5": -50, "1,234.50": 123450}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := Parse("  "); err == nil || err.Error() != "money.ParseAmount: empty input" {
		t.Errorf("empty input error = %v", err)
	}
}

func TestParseAmountStrict(t *testing.T) {
	if _, err := ParseAmountStrict("1,000.00"); err == nil {
		t.Error("separator accepted")
	}
	if got, err := ParseAmountStrict("3.05"); err != nil || got != 305 {
		t.Errorf("ParseAmountStrict = %v, %v", got, err)
	}
}
