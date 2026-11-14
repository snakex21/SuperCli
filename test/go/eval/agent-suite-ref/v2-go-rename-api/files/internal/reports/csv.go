package reports

import (
	"encoding/csv"
	"io"

	"shopcore/internal/money"
)

// SumCSV sums column col of a CSV stream (header skipped).
func SumCSV(r io.Reader, col int) (money.Amount, error) {
	rows, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return 0, err
	}
	var sum money.Amount
	for i, row := range rows {
		if i == 0 || col >= len(row) {
			continue
		}
		a, err := money.Parse(row[col])
		if err != nil {
			return 0, err
		}
		sum += a
	}
	return sum, nil
}
