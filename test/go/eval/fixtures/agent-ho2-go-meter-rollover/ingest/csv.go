// Package ingest parses the CSV exports of the head-end system.
package ingest

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"meterd/readings"
)

// Meter is one installed meter.
type Meter struct {
	ID, Customer, Model string
}

// Readings parses "meter,at,value" rows (header required).
func Readings(r io.Reader) ([]readings.Reading, error) {
	rows, err := records(r, "meter,at,value")
	if err != nil {
		return nil, err
	}
	out := make([]readings.Reading, 0, len(rows))
	for i, row := range rows {
		at, err := time.Parse(time.RFC3339, row[1])
		if err != nil {
			return nil, fmt.Errorf("readings line %d: %w", i+2, err)
		}
		v, err := strconv.ParseInt(row[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("readings line %d: %w", i+2, err)
		}
		out = append(out, readings.Reading{Meter: row[0], At: at, Value: v})
	}
	return out, nil
}

// Meters parses "meter,customer,model" rows (header required).
func Meters(r io.Reader) ([]Meter, error) {
	rows, err := records(r, "meter,customer,model")
	if err != nil {
		return nil, err
	}
	out := make([]Meter, 0, len(rows))
	for _, row := range rows {
		out = append(out, Meter{ID: row[0], Customer: row[1], Model: row[2]})
	}
	return out, nil
}

func records(r io.Reader, header string) ([][]string, error) {
	cr := csv.NewReader(r)
	all, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 || strings.Join(all[0], ",") != header {
		return nil, fmt.Errorf("want header %q", header)
	}
	return all[1:], nil
}
