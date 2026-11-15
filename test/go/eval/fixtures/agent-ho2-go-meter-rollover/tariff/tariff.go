// Package tariff prices monthly consumption.
package tariff

// Tier charges Rate grosze per kWh for consumption up to UpTo kWh
// (UpTo 0 = no upper bound; must be last).
type Tier struct {
	UpTo int64
	Rate int64
}

// Tariff is a tiered price list plus a fixed monthly fee in grosze.
type Tariff struct {
	Tiers    []Tier
	FixedFee int64
}

// Household is the regulated household tariff G11.
var Household = Tariff{
	Tiers:    []Tier{{UpTo: 300, Rate: 62}, {UpTo: 1000, Rate: 71}, {Rate: 85}},
	FixedFee: 1850,
}

// Price returns the monthly amount in grosze for kwh.
func (t Tariff) Price(kwh int64) int64 {
	amount := t.FixedFee
	var prev int64
	for _, tier := range t.Tiers {
		top := kwh
		if tier.UpTo != 0 && tier.UpTo < kwh {
			top = tier.UpTo
		}
		if top > prev {
			amount += (top - prev) * tier.Rate
			prev = top
		}
	}
	return amount
}
