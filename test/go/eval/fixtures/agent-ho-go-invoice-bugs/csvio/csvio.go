// Package csvio imports invoice lines from CSV.
package csvio

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"invoicer/invoice"
	"invoicer/money"
)

// ReadLines reads "no,sku,desc,qty,unit" rows after a header row.
func ReadLines(r io.Reader) ([]invoice.Line, error) {
	sc := bufio.NewScanner(r)
	var out []invoice.Line
	n := 0
	for sc.Scan() {
		n++
		if n == 1 || strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		f := strings.Split(sc.Text(), ",")
		if len(f) != 5 {
			return nil, fmt.Errorf("csvio: line %d: want 5 fields, got %d", n, len(f))
		}
		no, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
		}
		qty, err := strconv.Atoi(f[3])
		if err != nil {
			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
		}
		unit, err := money.Parse(f[4])
		if err != nil {
			return nil, fmt.Errorf("csvio: line %d: %w", n, err)
		}
		out = append(out, invoice.Line{No: no, SKU: f[1], Desc: f[2], Qty: qty, Unit: unit})
	}
	return out, sc.Err()
}
