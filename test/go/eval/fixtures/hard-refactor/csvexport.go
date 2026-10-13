package invoicing

import (
	"fmt"
	"strings"
)

// ExportCSV renders the lines as semicolon-separated values.
func ExportCSV(lines []Line) string {
	var b strings.Builder
	b.WriteString("name;qty;unit;total\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "%s;%d;%s;%s\n", l.Name, l.Qty, amountText(l.UnitCents), amountText(l.TotalCents()))
	}
	fmt.Fprintf(&b, "TOTAL;;;%s\n", amountText(total(lines)))
	return b.String()
}

func amountText(c int64) string {
	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}
	return fmt.Sprintf("%s%d.%d", sign, c/100, c%100)
}
