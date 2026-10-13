package invoicing

import (
	"fmt"
	"strings"
)

// RenderInvoice renders a fixed-width plain-text invoice.
func RenderInvoice(number string, lines []Line) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Invoice %s\n", number)
	for _, l := range lines {
		fmt.Fprintf(&b, "%-20s %4d x %12s = %14s\n", l.Name, l.Qty, formatCents(l.UnitCents), formatCents(l.TotalCents()))
	}
	fmt.Fprintf(&b, "%-20s %35s\n", "TOTAL", formatCents(total(lines)))
	return b.String()
}

func formatCents(c int64) string {
	return fmt.Sprintf("%d.%02d", c/100, c%100)
}
