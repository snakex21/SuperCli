// Package csvio imports invoice lines from CSV.
package csvio

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"invoicer/invoice"
	"invoicer/money"
)

// ReadLines reads "no,sku,desc,qty,unit" rows after a header row.
func ReadLines(r io.Reader) ([]invoice.Line, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	var out []invoice.Line
	first := true
	for {
		f, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csvio: %w", err)
		}
		n, _ := cr.FieldPos(0)
		if first {
			first = false
			continue
		}
		if len(f) == 1 && strings.TrimSpace(f[0]) == "" {
			continue
		}
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
	return out, nil
}
