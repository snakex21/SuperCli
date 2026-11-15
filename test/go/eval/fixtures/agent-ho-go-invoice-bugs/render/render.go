// Package render prints invoices as plain text.
package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"invoicer/dates"
	"invoicer/invoice"
	"invoicer/money"
)

// Text renders inv; lines are printed in line-number order.
func Text(inv invoice.Invoice) (string, error) {
	net, vat, gross, err := invoice.Totals(inv)
	if err != nil {
		return "", err
	}
	lines := append([]invoice.Line(nil), inv.Lines...)
	sort.Slice(lines, func(i, j int) bool {
		return strconv.Itoa(lines[i].No) < strconv.Itoa(lines[j].No)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "Invoice %s\n", inv.Number)
	fmt.Fprintf(&b, "Due %s\n", dates.DueDate(inv.Issued, inv.NetDays).Format("2006-01-02"))
	for _, l := range lines {
		fmt.Fprintf(&b, "%3d %-8s %-20s %4d x %s\n", l.No, l.SKU, l.Desc, l.Qty, money.Format(l.Unit))
	}
	fmt.Fprintf(&b, "Net %s\nVAT %s\nTotal %s\n", money.Format(net), money.Format(vat), money.Format(gross))
	return b.String(), nil
}
