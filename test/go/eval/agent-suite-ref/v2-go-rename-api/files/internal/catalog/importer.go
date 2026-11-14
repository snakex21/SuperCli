package catalog

import (
	"strings"

	"shopcore/internal/money"
)

// Importer reads supplier feeds whose prices carry a currency prefix.
type Importer struct {
	Currency string
}

// ParseAmount strips the importer's currency prefix and parses the
// rest. It is the supplier-feed entry point and keeps its name.
func (im *Importer) ParseAmount(field string) (money.Amount, error) {
	field = strings.TrimSpace(field)
	field = strings.TrimPrefix(field, im.Currency)
	return money.Parse(field)
}

// ParseStrictFeed is used for the audited feed, which must have two decimals.
func (im *Importer) ParseStrictFeed(field string) (money.Amount, error) {
	return money.ParseAmountStrict(strings.TrimPrefix(strings.TrimSpace(field), im.Currency))
}
