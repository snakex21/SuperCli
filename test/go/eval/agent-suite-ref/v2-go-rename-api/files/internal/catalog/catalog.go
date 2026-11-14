// Package catalog loads product rows.
package catalog

import (
	"fmt"

	"shopcore/internal/money"
)

// Row is one raw catalog line.
type Row struct {
	SKU   string
	Price string
	Sale  string
}

// Product is a parsed catalog entry.
type Product struct {
	SKU   string
	Price money.Amount
	Sale  money.Amount
}

// Load parses rows; Sale is optional.
func Load(rows []Row) ([]Product, error) {
	out := make([]Product, 0, len(rows))
	for _, r := range rows {
		p, err := money.Parse(r.Price)
		if err != nil {
			return nil, fmt.Errorf("sku %s: %w", r.SKU, err)
		}
		prod := Product{SKU: r.SKU, Price: p}
		if r.Sale != "" {
			s, err := money.Parse(r.Sale)
			if err != nil {
				return nil, fmt.Errorf("sku %s sale: %w", r.SKU, err)
			}
			prod.Sale = s
		}
		out = append(out, prod)
	}
	return out, nil
}
