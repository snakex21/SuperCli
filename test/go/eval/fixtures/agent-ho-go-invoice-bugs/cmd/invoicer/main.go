// Command invoicer renders an invoice from a CSV of lines.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"invoicer/csvio"
	"invoicer/ids"
	"invoicer/invoice"
	"invoicer/render"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: invoicer <seq> <country> <lines.csv>")
		os.Exit(2)
	}
	seq, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	f, err := os.Open(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	lines, err := csvio.ReadLines(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	inv := invoice.Invoice{Number: ids.InvoiceNumber(seq), Country: os.Args[2], Issued: time.Now(), NetDays: 30, Lines: lines}
	out, err := render.Text(inv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(out)
}
