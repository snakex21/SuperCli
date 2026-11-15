package render

import (
	"strings"
	"testing"
	"time"

	"invoicer/invoice"
)

func TestText(t *testing.T) {
	inv := invoice.Invoice{Number: "INV-1", Country: "DE", Issued: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), NetDays: 14,
		Lines: []invoice.Line{{No: 2, SKU: "B", Qty: 1, Unit: 100}, {No: 1, SKU: "A", Qty: 1, Unit: 100}}}
	out, err := Text(inv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Due 2026-03-16") || strings.Index(out, " A ") > strings.Index(out, " B ") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
