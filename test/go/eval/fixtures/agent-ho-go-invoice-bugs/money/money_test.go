package money

import "testing"

func TestFormatParse(t *testing.T) {
	if got := Format(123450); got != "1234.50" {
		t.Fatalf("Format = %q", got)
	}
	if c, err := Parse("19.9"); err != nil || c != 1990 {
		t.Fatalf("Parse = %d, %v", c, err)
	}
}
