package reports

import (
	"strings"
	"testing"
)

func TestSums(t *testing.T) {
	if got, err := SumDaily([]string{"a;1.00", "b;2.5"}); err != nil || got != 350 {
		t.Fatalf("SumDaily = %v, %v", got, err)
	}
	if got, err := SumCSV(strings.NewReader("sku,amt\na,\"1,000.00\"\n"), 1); err != nil || got != 100000 {
		t.Fatalf("SumCSV = %v, %v", got, err)
	}
}
