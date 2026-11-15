package render

import (
	"strings"
	"testing"
	"time"

	"invoicer/invoice"
)

func TestHiddenNumericOrder(t *testing.T) {
	var ls []invoice.Line
	for _, n := range []int{10, 2, 1, 11, 3} {
		ls = append(ls, invoice.Line{No: n, SKU: "S" + string(rune('A'+n)), Qty: 1, Unit: 100})
	}
	out, err := Text(invoice.Invoice{Number: "INV-000001", Country: "DE", Issued: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), NetDays: 13, Lines: ls})
	if err != nil {
		t.Fatal(err)
	}
	prev := -1
	for _, n := range []int{1, 2, 3, 10, 11} {
		i := strings.Index(out, " S"+string(rune('A'+n))+" ")
		if i < prev {
			t.Fatalf("line %d out of order:\n%s", n, out)
		}
		prev = i
	}
}
