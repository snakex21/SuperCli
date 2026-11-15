// Command billrun prints the invoices for one month of exports.
package main

import (
	"fmt"
	"os"

	"meterd/billing"
	"meterd/tariff"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: billrun meters.csv readings.csv")
		os.Exit(2)
	}
	m, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer m.Close()
	r, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer r.Close()
	invs, err := billing.Build(m, r, tariff.Household)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, inv := range invs {
		fmt.Printf("%s\t%d kWh\t%s\n", inv.Customer, inv.KWh, billing.FormatPLN(inv.Amount))
	}
}
