// Package invoicing renders invoice data in several output formats.
package invoicing

// Line is one invoice position. UnitCents is the unit price in cents.
type Line struct {
	Name      string
	Qty       int64
	UnitCents int64
}

// TotalCents returns Qty * UnitCents.
func (l Line) TotalCents() int64 { return l.Qty * l.UnitCents }

func total(lines []Line) int64 {
	var sum int64
	for _, l := range lines {
		sum += l.TotalCents()
	}
	return sum
}
